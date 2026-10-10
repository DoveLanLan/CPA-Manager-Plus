package clinepass

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type memoryAccountStore struct {
	mu    sync.Mutex
	items map[string]model.ThirdPartyQuotaAccount
}

func (s *memoryAccountStore) ListThirdPartyQuotaAccounts(context.Context) ([]model.ThirdPartyQuotaAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]model.ThirdPartyQuotaAccount, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	return items, nil
}

func (s *memoryAccountStore) GetThirdPartyQuotaAccount(_ context.Context, id string) (model.ThirdPartyQuotaAccount, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	return item, ok, nil
}

func (s *memoryAccountStore) UpsertThirdPartyQuotaAccount(_ context.Context, account model.ThirdPartyQuotaAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[account.ID] = account
	return nil
}

func (s *memoryAccountStore) DeleteThirdPartyQuotaAccount(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return false, nil
	}
	delete(s.items, id)
	return true, nil
}

func (s *memoryAccountStore) account(t *testing.T, id string) model.ThirdPartyQuotaAccount {
	t.Helper()
	item, ok, err := s.GetThirdPartyQuotaAccount(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("account %q: ok=%v err=%v", id, ok, err)
	}
	return item
}

func TestFetchUsageLimitsSendsOnlySessionCookieAndNormalizesResponse(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/v1/users/me/plan/usage-limits":
			if r.Header.Get("Origin") != "https://app.cline.bot" {
				t.Errorf("origin = %q", r.Header.Get("Origin"))
			}
			writeClineJSON(w, map[string]any{
				"data": map[string]any{
					"limits": []map[string]any{
						{"type": "monthly", "percentUsed": 101.5, "resetsAt": "2026-08-01T00:00:00Z"},
						{"type": "five_hour", "percentUsed": 17.25, "resetsAt": "2026-07-31T10:00:00Z"},
						{"type": "weekly", "percentUsed": nil},
					},
				},
			})
		default:
			// The token totals are best effort, so this fixture simply has no
			// profile endpoint and the reading stays limits-only.
			w.WriteHeader(http.StatusNotFound)
		}
	})

	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Work",
			Credential:     "session-value",
			CredentialKind: model.ThirdPartyQuotaCredentialSession,
		},
	}}
	service := New(store, WithHTTPClient(stub.client()), WithClineAPIBaseURL(stub.baseURL()))
	result, err := service.FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch usage: %v", err)
	}
	if result.Limits[0].Type != "five_hour" || result.Limits[0].PercentUsed != 17.25 {
		t.Fatalf("five-hour limit = %#v", result.Limits[0])
	}
	if result.Limits[1].Type != "weekly" || result.Limits[1].PercentUsed != 0 {
		t.Fatalf("weekly limit = %#v", result.Limits[1])
	}
	if result.Limits[2].Type != "monthly" || result.Limits[2].PercentUsed != 100 {
		t.Fatalf("monthly limit = %#v", result.Limits[2])
	}
	if result.Provider != ProviderClinePass {
		t.Fatalf("provider = %q", result.Provider)
	}
	// Only the session cookie may go upstream, and only on the quota call.
	for _, request := range stub.snapshot() {
		if request.cookieValue != "session-value" {
			t.Fatalf("request %s carried cookie %q", request.path, request.cookieValue)
		}
		if request.authorization != "" {
			t.Fatalf("request %s carried authorization %q", request.path, request.authorization)
		}
	}
}

func TestFetchUsageLimitsMapsExpiredSessionWithoutReturningManagerUnauthorized(t *testing.T) {
	stub := newClineStub(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:             "account-1",
			Provider:       ProviderClinePass,
			Label:          "Work",
			Credential:     "expired",
			CredentialKind: model.ThirdPartyQuotaCredentialSession,
		},
	}}
	service := New(store, WithHTTPClient(stub.client()), WithClineAPIBaseURL(stub.baseURL()))
	_, err := service.FetchUsageLimits(context.Background(), "account-1")
	usageError, ok := err.(*UsageError)
	if !ok {
		t.Fatalf("error = %#v", err)
	}
	if usageError.StatusCode != http.StatusUnprocessableEntity || usageError.Code != "clinepass_session_expired" {
		t.Fatalf("usage error = %#v", usageError)
	}
}

func TestNormalizeCredentialAcceptsProviderCookieHeaderAndCurlText(t *testing.T) {
	for _, input := range []string{
		"cline_session_id=session-value; other=value",
		"other=value; cline_session_id=session-value; analytics=value",
		"curl https://example.test -b 'other=value; cline_session_id=session-value; analytics=value'",
	} {
		got, err := normalizeCredential(ProviderClinePass, input)
		if err != nil || got != "session-value" {
			t.Fatalf("normalize %q = %q, %v", input, got, err)
		}
	}
	got, err := normalizeCredential(ProviderOpenCodeGo, "oc_locale=zh; auth=Fe26.2**secret*value; analytics=ignored")
	if err != nil || got != "Fe26.2**secret*value" {
		t.Fatalf("normalize OpenCode auth = %q, %v", got, err)
	}
}

func TestFetchOpenCodeGoUsageUsesOnlyAuthCookieAndParsesServerFunctionStream(t *testing.T) {
	workspaceID := "wrk_01TESTWORKSPACE000000000000"
	serverFunctionID := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_server" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("id") != serverFunctionID {
			t.Errorf("server function id = %q", r.URL.Query().Get("id"))
		}
		if !strings.Contains(r.URL.Query().Get("args"), workspaceID) {
			t.Errorf("args = %q", r.URL.Query().Get("args"))
		}
		if got := r.Header.Get("X-Server-ID"); got != serverFunctionID {
			t.Errorf("X-Server-ID = %q", got)
		}
		if got := r.Header.Get("X-Server-Instance"); got != "server-fn:2" {
			t.Errorf("X-Server-Instance = %q", got)
		}
		cookies := r.Cookies()
		if len(cookies) != 1 || cookies[0].Name != "auth" || cookies[0].Value != "Fe26.2**secret" {
			t.Errorf("cookies = %#v", cookies)
		}
		script := `((self.$R=self.$R||{})["server-fn:2"]=[],($R=>$R[0]={mine:!0,useBalance:!0,region:$R[1]=["us","sg"],rollingUsage:$R[2]={status:"ok",resetInSec:60,usagePercent:12.5},weeklyUsage:$R[3]={status:"ok",resetInSec:3600,usagePercent:25},monthlyUsage:$R[4]={status:"ok",resetInSec:7200,usagePercent:50}})($R["server-fn:2"]))`
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = fmt.Fprintf(w, ";0x%08x;%s", len([]byte(script)), script)
	}))
	t.Cleanup(server.Close)

	client := server.Client()
	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{
		"account-1": {
			ID:               "account-1",
			Provider:         ProviderOpenCodeGo,
			Credential:       "Fe26.2**secret",
			WorkspaceID:      workspaceID,
			ServerFunctionID: serverFunctionID,
		},
	}}
	service := New(store, WithHTTPClient(client), WithOpenCodeServerURL(server.URL+"/_server"))
	result, err := service.FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch usage: %v", err)
	}
	if result.Provider != ProviderOpenCodeGo || result.UseBalance == nil || !*result.UseBalance || len(result.Regions) != 2 {
		t.Fatalf("metadata = %#v", result)
	}
	if len(result.Limits) != 3 || result.Limits[0].Type != "five_hour" || result.Limits[0].PercentUsed != 12.5 {
		t.Fatalf("limits = %#v", result.Limits)
	}
}

func TestParseOpenCodeJSResponseRejectsUnexpectedCode(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"rollingUsage":{"usagePercent":1}}`),
		[]byte(`;0x0000000a;alert(1)()`),
		[]byte(`;0x00000020;({mine:!0,useBalance:!1})`),
	} {
		if _, ok := parseOpenCodeJSResponse(body); ok {
			t.Fatalf("unexpected parse success for %q", body)
		}
	}
}

func TestCreateOpenCodeAccountDefaultsCurrentServerFunctionID(t *testing.T) {
	store := &memoryAccountStore{items: map[string]model.ThirdPartyQuotaAccount{}}
	service := New(store)
	account, err := service.CreateAccount(context.Background(), CreateAccountRequest{
		Provider:    ProviderOpenCodeGo,
		Label:       "Go workspace",
		Credential:  "auth=Fe26.2**secret",
		WorkspaceID: "wrk_workspace",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if account.Provider != ProviderOpenCodeGo || account.ServerFunctionID != defaultOpenCodeServerFunctionID {
		t.Fatalf("account = %#v", account)
	}
}
