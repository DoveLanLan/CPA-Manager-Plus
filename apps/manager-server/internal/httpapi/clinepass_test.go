package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	clinepasssvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/clinepass"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestThirdPartyQuotaClinePassAccountAndUsageManagementFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("cline_session_id")
		if err != nil || cookie.Value != "session-value" {
			t.Errorf("cline session cookie = %#v, %v", cookie, err)
		}
		if len(r.Cookies()) != 1 {
			t.Errorf("unexpected upstream cookies: %#v", r.Cookies())
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/users/me/plan/usage-limits":
			_, _ = w.Write([]byte(`{"data":{"limits":[{"type":"five_hour","percentUsed":25,"resetsAt":"2026-07-31T12:00:00Z"}]}}`))
		case "/api/v1/users/me":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"usr-flow","email":"flow@example.test"}}`))
		case "/api/v1/users/usr-flow/usages/daily":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"totalTokens":1200,"promptTokens":1000,"completionTokens":200}]}}`))
		default:
			t.Errorf("unexpected upstream path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	cfg := testutil.NewConfig(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("new protector: %v", err)
	}
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	testutil.EnsureAdminCredential(t, db)

	server := New(cfg, db, collector.NewManager(cfg, db))
	server.AppContext().ThirdPartyQuotaService = clinepasssvc.New(
		db,
		clinepasssvc.WithHTTPClient(upstream.Client()),
		clinepasssvc.WithClineAPIBaseURL(upstream.URL+"/api/v1"),
		clinepasssvc.WithClineLifetimeWindow(1, time.Hour),
	)

	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/v0/management/third-party-quota/accounts",
		bytes.NewBufferString(`{"provider":"clinepass","label":"Work","credential":"other=value; cline_session_id=session-value; analytics=ignored"}`),
	)
	createRequest.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	createResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	if strings.Contains(createResponse.Body.String(), "session-value") || strings.Contains(createResponse.Body.String(), "analytics") {
		t.Fatalf("create response leaked credential: %s", createResponse.Body.String())
	}
	var account struct {
		ID             string `json:"id"`
		Provider       string `json:"provider"`
		CredentialKind string `json:"credentialKind"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &account); err != nil || account.ID == "" || account.Provider != "clinepass" {
		t.Fatalf("decode account: id=%q err=%v", account.ID, err)
	}
	if account.CredentialKind != "session" {
		t.Fatalf("credential kind = %q, want session", account.CredentialKind)
	}

	usageRequest := httptest.NewRequest(
		http.MethodGet,
		"/v0/management/third-party-quota/accounts/"+account.ID+"/usage-limits",
		nil,
	)
	usageRequest.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	usageResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(usageResponse, usageRequest)
	if usageResponse.Code != http.StatusOK {
		t.Fatalf("usage status = %d, body = %s", usageResponse.Code, usageResponse.Body.String())
	}
	if !strings.Contains(usageResponse.Body.String(), `"type":"five_hour"`) ||
		!strings.Contains(usageResponse.Body.String(), `"percentUsed":25`) {
		t.Fatalf("usage response = %s", usageResponse.Body.String())
	}
	// The token totals ride along with the quota reading.
	if !strings.Contains(usageResponse.Body.String(), `"monthToDate":1200`) ||
		!strings.Contains(usageResponse.Body.String(), `"lifetime":1200`) {
		t.Fatalf("usage response is missing token totals: %s", usageResponse.Body.String())
	}
}

func TestThirdPartyQuotaManagementRequiresPanelAuthentication(t *testing.T) {
	handler := newTestHandler(t, "", false)
	request := httptest.NewRequest(http.MethodGet, "/v0/management/third-party-quota/accounts", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestLegacyClinePassManagementPathRemainsAvailable(t *testing.T) {
	handler := newTestHandler(t, "", false)
	request := httptest.NewRequest(http.MethodGet, "/v0/management/clinepass/accounts", nil)
	request.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
