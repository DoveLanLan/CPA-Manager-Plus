package clinepass

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type memoryAccountStore struct {
	items map[string]model.ClinePassAccount
}

func (s *memoryAccountStore) ListClinePassAccounts(context.Context) ([]model.ClinePassAccount, error) {
	items := make([]model.ClinePassAccount, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	return items, nil
}

func (s *memoryAccountStore) GetClinePassAccount(_ context.Context, id string) (model.ClinePassAccount, bool, error) {
	item, ok := s.items[id]
	return item, ok, nil
}

func (s *memoryAccountStore) UpsertClinePassAccount(_ context.Context, account model.ClinePassAccount) error {
	s.items[account.ID] = account
	return nil
}

func (s *memoryAccountStore) DeleteClinePassAccount(_ context.Context, id string) (bool, error) {
	if _, ok := s.items[id]; !ok {
		return false, nil
	}
	delete(s.items, id)
	return true, nil
}

func TestFetchUsageLimitsSendsOnlySessionCookieAndNormalizesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/me/plan/usage-limits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		cookies := r.Cookies()
		if len(cookies) != 1 || cookies[0].Name != "cline_session_id" || cookies[0].Value != "session-value" {
			t.Errorf("cookies = %#v", cookies)
		}
		if r.Header.Get("Origin") != "https://app.cline.bot" {
			t.Errorf("origin = %q", r.Header.Get("Origin"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"limits": []map[string]any{
					{"type": "monthly", "percentUsed": 101.5, "resetsAt": "2026-08-01T00:00:00Z"},
					{"type": "five_hour", "percentUsed": 17.25, "resetsAt": "2026-07-31T10:00:00Z"},
					{"type": "weekly", "percentUsed": nil},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	store := &memoryAccountStore{items: map[string]model.ClinePassAccount{
		"account-1": {ID: "account-1", Label: "Work", SessionID: "session-value"},
	}}
	service := New(store, WithHTTPClient(server.Client()), WithUsageLimitsURL(server.URL+"/api/v1/users/me/plan/usage-limits"))
	result, err := service.FetchUsageLimits(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("fetch usage: %v", err)
	}
	if len(result.Limits) != 3 {
		t.Fatalf("limits = %#v", result.Limits)
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
}

func TestFetchUsageLimitsMapsExpiredSessionWithoutReturningManagerUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	store := &memoryAccountStore{items: map[string]model.ClinePassAccount{
		"account-1": {ID: "account-1", SessionID: "expired"},
	}}
	service := New(store, WithHTTPClient(server.Client()), WithUsageLimitsURL(server.URL))
	_, err := service.FetchUsageLimits(context.Background(), "account-1")
	usageError, ok := err.(*UsageError)
	if !ok {
		t.Fatalf("error = %#v", err)
	}
	if usageError.StatusCode != http.StatusUnprocessableEntity || usageError.Code != "clinepass_session_expired" {
		t.Fatalf("usage error = %#v", usageError)
	}
}

func TestNormalizeSessionIDAcceptsCookieHeaderAndCurlText(t *testing.T) {
	for _, input := range []string{
		"cline_session_id=session-value; other=value",
		"other=value; cline_session_id=session-value; analytics=value",
		"curl https://example.test -b 'other=value; cline_session_id=session-value; analytics=value'",
	} {
		got, err := normalizeSessionID(input)
		if err != nil || got != "session-value" {
			t.Fatalf("normalize %q = %q, %v", input, got, err)
		}
	}
}
