package clinepass

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type clineStubRequest struct {
	method        string
	path          string
	query         url.Values
	authorization string
	cookieValue   string
}

type clineStub struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []clineStubRequest
	counts   map[string]int
}

// newClineStub serves one fixture Cline API. The handler receives the 1-based
// call number for the path, so a test can make the first attempt fail.
func newClineStub(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, call int)) *clineStub {
	t.Helper()
	stub := &clineStub{counts: map[string]int{}}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.counts[r.URL.Path]++
		call := stub.counts[r.URL.Path]
		cookieValue := ""
		if cookie, err := r.Cookie(clineSessionCookieName); err == nil {
			cookieValue = cookie.Value
		}
		stub.requests = append(stub.requests, clineStubRequest{
			method:        r.Method,
			path:          r.URL.Path,
			query:         r.URL.Query(),
			authorization: r.Header.Get("Authorization"),
			cookieValue:   cookieValue,
		})
		stub.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		handler(w, r, call)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *clineStub) baseURL() string      { return s.server.URL + "/api/v1" }
func (s *clineStub) client() *http.Client { return s.server.Client() }

func (s *clineStub) calls(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[path]
}

func (s *clineStub) snapshot() []clineStubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]clineStubRequest(nil), s.requests...)
}

func writeClineJSON(w http.ResponseWriter, payload any) {
	_ = json.NewEncoder(w).Encode(payload)
}

// clineTestService wires a service onto a stub with a one-chunk lifetime window,
// so a token walk costs one request unless a test widens it.
func clineTestService(t *testing.T, store accountStore, stub *clineStub) *Service {
	t.Helper()
	return New(
		store,
		WithHTTPClient(stub.client()),
		WithClineAPIBaseURL(stub.baseURL()),
		WithClineLifetimeWindow(1, time.Hour),
	)
}

func TestParseClineCredentialClassifiesShapes(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9." + strings.Repeat("A", 24) + "." + strings.Repeat("B", 24)
	cases := []struct {
		name     string
		input    string
		kind     string
		expected string
	}{
		{name: "personal API key", input: "sk_cline_abcdef123456", kind: model.ThirdPartyQuotaCredentialAPIKey, expected: "sk_cline_abcdef123456"},
		{name: "prefixed workos token", input: "workos:" + jwt, kind: model.ThirdPartyQuotaCredentialAccessToken, expected: "workos:" + jwt},
		{name: "bare workos JWT", input: jwt, kind: model.ThirdPartyQuotaCredentialAccessToken, expected: jwt},
		{name: "cookie header", input: "other=1; cline_session_id=session-value; x=2", kind: model.ThirdPartyQuotaCredentialSession, expected: "session-value"},
		{name: "curl paste", input: "curl https://x -b 'cline_session_id=curl-value'", kind: model.ThirdPartyQuotaCredentialSession, expected: "curl-value"},
		{name: "opaque value is left to the probe", input: "opaque-refresh-value", kind: "", expected: "opaque-refresh-value"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input, err := parseClineCredential(testCase.input, "")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if input.hint != testCase.kind || input.secret != testCase.expected {
				t.Fatalf("hint=%q secret=%q, want hint=%q secret=%q", input.hint, input.secret, testCase.kind, testCase.expected)
			}
		})
	}
}

func TestParseClineCredentialReadsSettingsDocument(t *testing.T) {
	document := `{"providers":{"cline":{"settings":{"auth":{
		"accessToken":"access-from-file",
		"refreshToken":"refresh-from-file",
		"expiresAt":1783000000,
		"metadata":{"userInfo":{"clineUserId":"usr-file","email":"file@example.test"}}
	}}}}}`
	input, err := parseClineCredential(document, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if input.hint != model.ThirdPartyQuotaCredentialRefreshToken {
		t.Fatalf("hint = %q", input.hint)
	}
	if input.secret != "refresh-from-file" || input.refreshToken != "refresh-from-file" {
		t.Fatalf("secret=%q refresh=%q", input.secret, input.refreshToken)
	}
	if input.accessToken != "access-from-file" || input.accountID != "usr-file" || input.email != "file@example.test" {
		t.Fatalf("input = %#v", input)
	}
	if input.expiresAtMS != 1783000000_000 {
		t.Fatalf("expiresAtMS = %d", input.expiresAtMS)
	}
}

func TestResolveClineCredentialProbesOpaqueValues(t *testing.T) {
	t.Run("refresh token", func(t *testing.T) {
		stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
			if r.URL.Path != "/api/v1/auth/refresh" || r.Method != http.MethodPost {
				t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			}
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"accessToken":  "access-probed",
				"refreshToken": "refresh-rotated",
				"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			}})
		})
		service := clineTestService(t, &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{}}, stub)
		input, err := service.resolveClineCredentialInput(context.Background(), "opaque-value", "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if input.hint != model.ThirdPartyQuotaCredentialRefreshToken {
			t.Fatalf("hint = %q", input.hint)
		}
		if input.secret != "refresh-rotated" || input.accessToken != "access-probed" {
			t.Fatalf("input = %#v", input)
		}
	})

	t.Run("session cookie", func(t *testing.T) {
		stub := newClineStub(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusBadRequest)
		})
		service := clineTestService(t, &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{}}, stub)
		input, err := service.resolveClineCredentialInput(context.Background(), "opaque-value", "")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if input.hint != model.ThirdPartyQuotaCredentialSession || input.secret != "opaque-value" {
			t.Fatalf("input = %#v", input)
		}
	})
}

func TestClineAPIKeySendsBearerAndNoCookie(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/users/me/plan/usage-limits":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"limits": []map[string]any{{"type": "five_hour", "percentUsed": 10}},
			}})
		case "/api/v1/users/me":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"id": "usr-key", "email": "key@example.test"}})
		case "/api/v1/users/usr-key/usages/daily":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"items": []map[string]any{{"totalTokens": 42}},
			}})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Key",
			Credential:     "sk_cline_key",
			CredentialKind: model.ThirdPartyQuotaCredentialAPIKey,
		},
	}}
	result, err := clineTestService(t, store, stub).FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Tokens == nil || result.Tokens.MonthToDate != 42 || result.Tokens.Lifetime != 42 {
		t.Fatalf("tokens = %#v", result.Tokens)
	}
	for _, request := range stub.snapshot() {
		if request.authorization != "Bearer sk_cline_key" {
			t.Fatalf("request %s authorization = %q", request.path, request.authorization)
		}
		if request.cookieValue != "" {
			t.Fatalf("request %s carried cookie %q", request.path, request.cookieValue)
		}
	}
	saved := store.account(t, "account-1")
	if saved.AccountUserID != "usr-key" || saved.AccountEmail != "key@example.test" {
		t.Fatalf("identity not recorded: %#v", saved)
	}
}

func TestClineRefreshTokenRotatesAndPersists(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"refreshToken":"refresh-old"`) ||
				!strings.Contains(string(body), `"grantType":"refresh_token"`) {
				t.Errorf("refresh body = %s", body)
			}
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"accessToken":  "access-new",
				"refreshToken": "refresh-new",
				"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				"userInfo":     map[string]any{"clineUserId": "usr-rot", "email": "rot@example.test"},
			}})
		case "/api/v1/users/me/plan/usage-limits":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"limits": []map[string]any{{"type": "weekly", "percentUsed": 5}},
			}})
		case "/api/v1/users/usr-rot/usages/daily":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"items": []map[string]any{{"totalTokens": 7}},
			}})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Rotating",
			Credential:     "refresh-old",
			CredentialKind: model.ThirdPartyQuotaCredentialRefreshToken,
			RefreshToken:   "refresh-old",
		},
	}}
	if _, err := clineTestService(t, store, stub).FetchUsageLimits(context.Background(), "account-1"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if stub.calls("/api/v1/auth/refresh") != 1 {
		t.Fatalf("refresh calls = %d", stub.calls("/api/v1/auth/refresh"))
	}
	saved := store.account(t, "account-1")
	if saved.AccessToken != "access-new" {
		t.Fatalf("access token = %q", saved.AccessToken)
	}
	// The rotated refresh token has to be on disk or the credential the server
	// just retired is the one left behind.
	if saved.RefreshToken != "refresh-new" || saved.Credential != "refresh-new" {
		t.Fatalf("refresh token not rotated: %#v", saved)
	}
	for _, request := range stub.snapshot() {
		if request.path == "/api/v1/users/me/plan/usage-limits" && request.authorization != "Bearer workos:access-new" {
			t.Fatalf("quota call authorization = %q", request.authorization)
		}
	}
}

func TestClineRefreshRotationIsSingleFlight(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			time.Sleep(20 * time.Millisecond)
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"accessToken":  "access-new",
				"refreshToken": "refresh-new",
				"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			}})
		case "/api/v1/users/me/plan/usage-limits":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"limits": []map[string]any{}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Concurrent",
			Credential:     "refresh-old",
			CredentialKind: model.ThirdPartyQuotaCredentialRefreshToken,
			RefreshToken:   "refresh-old",
		},
	}}
	service := clineTestService(t, store, stub)

	var wg sync.WaitGroup
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.FetchUsageLimits(context.Background(), "account-1"); err != nil {
				t.Errorf("concurrent fetch: %v", err)
			}
		}()
	}
	wg.Wait()

	// Each refresh retires the previous token, so more than one would leave the
	// account holding a credential the server no longer accepts.
	if calls := stub.calls("/api/v1/auth/refresh"); calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

func TestClineRefusedBearerRefreshesOnceAndRetries(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, call int) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"accessToken":  "access-new",
				"refreshToken": "refresh-new",
				"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			}})
		case "/api/v1/users/me/plan/usage-limits":
			if call == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer workos:access-new" {
				t.Errorf("retry authorization = %q", got)
			}
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"limits": []map[string]any{{"type": "monthly", "percentUsed": 33}},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:                     "account-1",
			Provider:               ProviderClinePass,
			Label:                  "Refused",
			Credential:             "refresh-old",
			CredentialKind:         model.ThirdPartyQuotaCredentialRefreshToken,
			RefreshToken:           "refresh-old",
			AccessToken:            "access-stale",
			AccessTokenExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(),
		},
	}}
	result, err := clineTestService(t, store, stub).FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(result.Limits) != 1 || result.Limits[0].PercentUsed != 33 {
		t.Fatalf("limits = %#v", result.Limits)
	}
	if calls := stub.calls("/api/v1/users/me/plan/usage-limits"); calls != 2 {
		t.Fatalf("quota calls = %d, want 2", calls)
	}
	if calls := stub.calls("/api/v1/auth/refresh"); calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

func TestClineTokenTotalsAreRecordedAndReused(t *testing.T) {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/users/me/plan/usage-limits":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"limits": []map[string]any{}}})
		case "/api/v1/users/me":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"id": "usr-tokens"}})
		case "/api/v1/users/usr-tokens/usages/daily":
			tokens := 100
			if r.URL.Query().Get("startDate") == monthStart {
				tokens = 1000
			}
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"items": []map[string]any{{"totalTokens": tokens}},
			}})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Tokens",
			Credential:     "sk_cline_key",
			CredentialKind: model.ThirdPartyQuotaCredentialAPIKey,
		},
	}}
	service := New(
		store,
		WithHTTPClient(stub.client()),
		WithClineAPIBaseURL(stub.baseURL()),
		WithClineLifetimeWindow(3, time.Hour),
	)

	result, err := service.FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Tokens == nil {
		t.Fatal("token usage missing")
	}
	if result.Tokens.MonthToDate != 1000 {
		t.Fatalf("month to date = %d", result.Tokens.MonthToDate)
	}
	// Three chunks: the two older months at 100 each plus this month at 1000.
	if result.Tokens.Lifetime != 1200 {
		t.Fatalf("lifetime = %d", result.Tokens.Lifetime)
	}
	if result.Tokens.LifetimePartial {
		t.Fatal("lifetime should be complete")
	}
	if result.Tokens.LifetimeCached {
		t.Fatal("first reading should not be a cache hit")
	}
	saved := store.account(t, "account-1")
	if saved.LifetimeTokens != 1200 || saved.LifetimeTokensSyncedAtMS == 0 {
		t.Fatalf("lifetime not recorded: %#v", saved)
	}

	firstWalkCalls := stub.calls("/api/v1/users/usr-tokens/usages/daily")
	second, err := service.FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if second.Tokens == nil || !second.Tokens.LifetimeCached || second.Tokens.Lifetime != 1200 {
		t.Fatalf("second reading = %#v", second.Tokens)
	}
	// A recorded total must not be re-walked: only the month-to-date call is made.
	if calls := stub.calls("/api/v1/users/usr-tokens/usages/daily"); calls != firstWalkCalls+1 {
		t.Fatalf("daily calls after cache hit = %d, want %d", calls, firstWalkCalls+1)
	}

	forced, err := service.FetchUsageLimitsWithOptions(context.Background(), "account-1", UsageLimitsOptions{ForceTokenRefresh: true})
	if err != nil {
		t.Fatalf("forced fetch: %v", err)
	}
	if forced.Tokens == nil || forced.Tokens.LifetimeCached {
		t.Fatalf("forced reading = %#v", forced.Tokens)
	}
	if calls := stub.calls("/api/v1/users/usr-tokens/usages/daily"); calls != firstWalkCalls+1+4 {
		t.Fatalf("daily calls after forced walk = %d, want %d", calls, firstWalkCalls+5)
	}
}

func TestClineLifetimeMarksPartialWhenAChunkFails(t *testing.T) {
	now := time.Now()
	earliest := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0).Format("2006-01-02")

	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/users/me/plan/usage-limits":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"limits": []map[string]any{}}})
		case "/api/v1/users/me":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"id": "usr-partial"}})
		case "/api/v1/users/usr-partial/usages/daily":
			if r.URL.Query().Get("startDate") == earliest {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"items": []map[string]any{{"totalTokens": 100}},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Partial",
			Credential:     "sk_cline_key",
			CredentialKind: model.ThirdPartyQuotaCredentialAPIKey,
		},
	}}
	service := New(
		store,
		WithHTTPClient(stub.client()),
		WithClineAPIBaseURL(stub.baseURL()),
		WithClineLifetimeWindow(3, time.Hour),
	)
	result, err := service.FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Tokens == nil {
		t.Fatal("token usage missing")
	}
	if !result.Tokens.LifetimePartial {
		t.Fatal("lifetime should be partial")
	}
	if result.Tokens.Lifetime != 200 {
		t.Fatalf("lifetime = %d, want the two readable chunks", result.Tokens.Lifetime)
	}
	saved := store.account(t, "account-1")
	if !saved.LifetimeTokensPartial {
		t.Fatalf("partial flag not recorded: %#v", saved)
	}
}

func TestClineLifetimeWalkTimesOutAsPartial(t *testing.T) {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	// The older chunks block until the test releases them, so the walk's own
	// deadline is what ends them rather than a sleep racing a scheduler.
	release := make(chan struct{})
	defer close(release)

	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/users/me/plan/usage-limits":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"limits": []map[string]any{}}})
		case "/api/v1/users/me":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{"id": "usr-slow"}})
		case "/api/v1/users/usr-slow/usages/daily":
			if r.URL.Query().Get("startDate") != monthStart {
				<-release
			}
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"items": []map[string]any{{"totalTokens": 100}},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Slow",
			Credential:     "sk_cline_key",
			CredentialKind: model.ThirdPartyQuotaCredentialAPIKey,
		},
	}}
	service := New(
		store,
		WithHTTPClient(stub.client()),
		WithClineAPIBaseURL(stub.baseURL()),
		WithClineLifetimeWindow(3, time.Hour),
	)
	service.lifetimeTimeout = 100 * time.Millisecond

	result, err := service.FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.Tokens == nil {
		t.Fatal("token usage missing")
	}
	if !result.Tokens.LifetimePartial {
		t.Fatal("a walk that ran out of time should be reported as partial")
	}
	// The chunk that answered is still counted; only the parts that did not are lost.
	if result.Tokens.Lifetime != 100 {
		t.Fatalf("lifetime = %d", result.Tokens.Lifetime)
	}
}

func TestClineTokenUsageStaysBestEffortWhenEndpointsFail(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/users/me/plan/usage-limits":
			writeClineJSON(w, map[string]any{"success": true, "data": map[string]any{
				"limits": []map[string]any{{"type": "five_hour", "percentUsed": 10}},
			}})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Degraded",
			Credential:     "sk_cline_key",
			CredentialKind: model.ThirdPartyQuotaCredentialAPIKey,
		},
	}}
	result, err := clineTestService(t, store, stub).FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	// The quota windows survive an unreadable usage report.
	if len(result.Limits) != 1 {
		t.Fatalf("limits = %#v", result.Limits)
	}
	if result.Tokens != nil {
		t.Fatalf("tokens = %#v, want unavailable", result.Tokens)
	}
}

func TestClineRowTokensPrefersVendorTotal(t *testing.T) {
	value := func(input float64) *float64 { return &input }
	if got := clineRowTokens(value(120), value(999), value(999)); got != 120 {
		t.Fatalf("total not preferred: %d", got)
	}
	if got := clineRowTokens(nil, value(150), value(50)); got != 200 {
		t.Fatalf("fallback sum = %d", got)
	}
	if got := clineRowTokens(value(0), value(10), nil); got != 10 {
		t.Fatalf("zero total fallback = %d", got)
	}
}

func TestClineUsageChunksCoverWholeMonths(t *testing.T) {
	chunks := clineUsageChunks(time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC), 3)
	want := [][2]string{
		{"2026-01-01", "2026-01-31"},
		{"2026-02-01", "2026-02-28"},
		{"2026-03-01", "2026-03-15"},
	}
	if fmt.Sprint(chunks) != fmt.Sprint(want) {
		t.Fatalf("chunks = %v, want %v", chunks, want)
	}
}

func TestParseClineExpiryAcceptsEveryShape(t *testing.T) {
	cases := []struct {
		input    any
		expected int64
	}{
		{"2026-07-31T10:00:00Z", 1785492000000},
		{float64(1785492000), 1785492000000},
		{float64(1785492000000), 1785492000000},
		{json.Number("1785492000"), 1785492000000},
		{"not-a-date", 0},
		{nil, 0},
		{float64(12), 0},
	}
	for _, testCase := range cases {
		if got := parseClineExpiry(testCase.input); got != testCase.expected {
			t.Fatalf("parseClineExpiry(%v) = %d, want %d", testCase.input, got, testCase.expected)
		}
	}
}

func TestFormatClineAccessTokenMirrorsVendorRules(t *testing.T) {
	if got := formatClineAccessToken("eyJhbGci"); got != "workos:eyJhbGci" {
		t.Fatalf("bare token = %q", got)
	}
	if got := formatClineAccessToken("workos:eyJhbGci"); got != "workos:eyJhbGci" {
		t.Fatalf("prefixed token = %q", got)
	}
	// A personal key is the mirror image of a WorkOS token: prefixing it is what
	// makes it fail.
	if got := formatClineAccessToken("sk_cline_key"); got != "sk_cline_key" {
		t.Fatalf("personal key = %q", got)
	}
}

func TestNormalizeCredentialKindMapsAliases(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"auto":     "",
		"api_key":  model.ThirdPartyQuotaCredentialAPIKey,
		"APIKEY":   model.ThirdPartyQuotaCredentialAPIKey,
		"token":    model.ThirdPartyQuotaCredentialAccessToken,
		"refresh":  model.ThirdPartyQuotaCredentialRefreshToken,
		"cookie":   model.ThirdPartyQuotaCredentialSession,
		"nonsense": "",
	}
	for input, expected := range cases {
		if got := normalizeCredentialKind(input); got != expected {
			t.Fatalf("normalizeCredentialKind(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestCreateClineAccountRecordsCredentialKind(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusBadRequest)
	})
	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{}}
	service := clineTestService(t, store, stub)

	account, err := service.CreateAccount(context.Background(), CreateAccountRequest{
		Provider:   ProviderClinePass,
		Label:      "Key account",
		Credential: "sk_cline_key",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if account.CredentialKind != model.ThirdPartyQuotaCredentialAPIKey {
		t.Fatalf("credential kind = %q", account.CredentialKind)
	}
	// The API key's shape is decisive, so no probe is spent on it.
	if calls := len(stub.snapshot()); calls != 0 {
		t.Fatalf("unexpected upstream calls: %#v", stub.snapshot())
	}

	updated, err := service.UpdateAccount(context.Background(), account.ID, UpdateAccountRequest{
		Label: stringPointer("Renamed"),
	})
	if err != nil {
		t.Fatalf("update label: %v", err)
	}
	if updated.Label != "Renamed" || updated.CredentialKind != model.ThirdPartyQuotaCredentialAPIKey {
		t.Fatalf("label-only update changed the credential: %#v", updated)
	}
	stored := store.account(t, account.ID)
	if stored.Credential != "sk_cline_key" {
		t.Fatalf("credential = %q", stored.Credential)
	}
}

func stringPointer(value string) *string { return &value }
