package clinepass

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

const (
	defaultUsageLimitsURL = "https://api.cline.bot/api/v1/users/me/plan/usage-limits"
	maxSessionIDBytes     = 16 * 1024
	maxUpstreamBodyBytes  = 128 * 1024
)

var ErrAccountNotFound = errors.New("ClinePass account not found")

type accountStore interface {
	ListClinePassAccounts(ctx context.Context) ([]model.ClinePassAccount, error)
	GetClinePassAccount(ctx context.Context, id string) (model.ClinePassAccount, bool, error)
	UpsertClinePassAccount(ctx context.Context, account model.ClinePassAccount) error
	DeleteClinePassAccount(ctx context.Context, id string) (bool, error)
}

type Option func(*Service)

func WithHTTPClient(client *http.Client) Option {
	return func(service *Service) {
		if client != nil {
			service.client = client
		}
	}
}

func WithUsageLimitsURL(rawURL string) Option {
	return func(service *Service) {
		if value := strings.TrimSpace(rawURL); value != "" {
			service.usageLimitsURL = value
		}
	}
}

type Service struct {
	store          accountStore
	client         *http.Client
	usageLimitsURL string
}

type Account struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	CreatedAtMS int64  `json:"createdAtMs"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

type CreateAccountRequest struct {
	Label     string `json:"label"`
	SessionID string `json:"sessionId"`
}

type UpdateAccountRequest struct {
	Label     *string `json:"label,omitempty"`
	SessionID *string `json:"sessionId,omitempty"`
}

type UsageLimit struct {
	Type        string  `json:"type"`
	PercentUsed float64 `json:"percentUsed"`
	ResetsAt    string  `json:"resetsAt,omitempty"`
}

type UsageLimits struct {
	AccountID   string       `json:"accountId"`
	FetchedAtMS int64        `json:"fetchedAtMs"`
	Limits      []UsageLimit `json:"limits"`
}

type UsageError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *UsageError) Error() string {
	return e.Message
}

func New(store accountStore, options ...Option) *Service {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	service := &Service{
		store:          store,
		client:         client,
		usageLimitsURL: defaultUsageLimitsURL,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) ListAccounts(ctx context.Context) ([]Account, error) {
	items, err := s.store.ListClinePassAccounts(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Account, 0, len(items))
	for _, item := range items {
		result = append(result, accountView(item))
	}
	return result, nil
}

func (s *Service) CreateAccount(ctx context.Context, request CreateAccountRequest) (Account, error) {
	label, err := normalizeLabel(request.Label)
	if err != nil {
		return Account{}, err
	}
	sessionID, err := normalizeSessionID(request.SessionID)
	if err != nil {
		return Account{}, err
	}
	id, err := newAccountID()
	if err != nil {
		return Account{}, err
	}
	now := time.Now().UnixMilli()
	item := model.ClinePassAccount{
		ID:          id,
		Label:       label,
		SessionID:   sessionID,
		CreatedAtMS: now,
		UpdatedAtMS: now,
	}
	if err := s.store.UpsertClinePassAccount(ctx, item); err != nil {
		return Account{}, err
	}
	return accountView(item), nil
}

func (s *Service) UpdateAccount(ctx context.Context, id string, request UpdateAccountRequest) (Account, error) {
	id = strings.TrimSpace(id)
	item, ok, err := s.store.GetClinePassAccount(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	if request.Label == nil && request.SessionID == nil {
		return Account{}, errors.New("label or sessionId is required")
	}
	if request.Label != nil {
		item.Label, err = normalizeLabel(*request.Label)
		if err != nil {
			return Account{}, err
		}
	}
	if request.SessionID != nil {
		item.SessionID, err = normalizeSessionID(*request.SessionID)
		if err != nil {
			return Account{}, err
		}
	}
	item.UpdatedAtMS = time.Now().UnixMilli()
	if err := s.store.UpsertClinePassAccount(ctx, item); err != nil {
		return Account{}, err
	}
	return accountView(item), nil
}

func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	deleted, err := s.store.DeleteClinePassAccount(ctx, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !deleted {
		return ErrAccountNotFound
	}
	return nil
}

func (s *Service) FetchUsageLimits(ctx context.Context, id string) (UsageLimits, error) {
	item, ok, err := s.store.GetClinePassAccount(ctx, strings.TrimSpace(id))
	if err != nil {
		return UsageLimits{}, err
	}
	if !ok {
		return UsageLimits{}, ErrAccountNotFound
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.usageLimitsURL, nil)
	if err != nil {
		return UsageLimits{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://app.cline.bot")
	req.Header.Set("Referer", "https://app.cline.bot/")
	req.Header.Set("User-Agent", "CPA-Manager-Plus ClinePass quota monitor")
	req.AddCookie(&http.Cookie{Name: "cline_session_id", Value: item.SessionID})

	res, err := s.client.Do(req)
	if err != nil {
		return UsageLimits{}, &UsageError{
			StatusCode: http.StatusBadGateway,
			Code:       "clinepass_upstream_unavailable",
			Message:    "Cline usage service is unavailable",
		}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxUpstreamBodyBytes+1))
	if err != nil {
		return UsageLimits{}, &UsageError{
			StatusCode: http.StatusBadGateway,
			Code:       "clinepass_upstream_read_failed",
			Message:    "Failed to read Cline usage response",
		}
	}
	if len(body) > maxUpstreamBodyBytes {
		return UsageLimits{}, &UsageError{
			StatusCode: http.StatusBadGateway,
			Code:       "clinepass_upstream_response_too_large",
			Message:    "Cline usage response is too large",
		}
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return UsageLimits{}, usageStatusError(res.StatusCode)
	}

	var envelope struct {
		Data *struct {
			Limits []struct {
				Type        string   `json:"type"`
				PercentUsed *float64 `json:"percentUsed"`
				ResetsAt    string   `json:"resetsAt"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return UsageLimits{}, &UsageError{
			StatusCode: http.StatusBadGateway,
			Code:       "clinepass_upstream_invalid_response",
			Message:    "Cline returned an invalid usage response",
		}
	}

	limits := make([]UsageLimit, 0, len(envelope.Data.Limits))
	for _, raw := range envelope.Data.Limits {
		limitType := strings.TrimSpace(raw.Type)
		if limitType == "" {
			continue
		}
		percent := 0.0
		if raw.PercentUsed != nil && !math.IsNaN(*raw.PercentUsed) && !math.IsInf(*raw.PercentUsed, 0) {
			percent = math.Max(0, math.Min(100, *raw.PercentUsed))
		}
		limits = append(limits, UsageLimit{
			Type:        limitType,
			PercentUsed: percent,
			ResetsAt:    strings.TrimSpace(raw.ResetsAt),
		})
	}
	sort.SliceStable(limits, func(i, j int) bool {
		return usageLimitOrder(limits[i].Type) < usageLimitOrder(limits[j].Type)
	})
	return UsageLimits{
		AccountID:   item.ID,
		FetchedAtMS: time.Now().UnixMilli(),
		Limits:      limits,
	}, nil
}

func normalizeLabel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("account label is required")
	}
	if len([]rune(value)) > 100 {
		return "", errors.New("account label must be at most 100 characters")
	}
	return value, nil
}

func normalizeSessionID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if index := strings.Index(value, "cline_session_id="); index >= 0 {
		value = value[index+len("cline_session_id="):]
		if end := strings.IndexAny(value, ";'\" \t\r\n"); end >= 0 {
			value = value[:end]
		}
	}
	value = strings.Trim(strings.TrimSpace(value), "'\"")
	if value == "" {
		return "", errors.New("cline_session_id is required")
	}
	if len(value) > maxSessionIDBytes {
		return "", fmt.Errorf("cline_session_id must be at most %d bytes", maxSessionIDBytes)
	}
	for _, char := range value {
		if char <= 0x20 || char >= 0x7f || strings.ContainsRune("\";,\\", char) {
			return "", errors.New("cline_session_id contains invalid cookie characters")
		}
	}
	return value, nil
}

func newAccountID() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func accountView(item model.ClinePassAccount) Account {
	return Account{
		ID:          item.ID,
		Label:       item.Label,
		CreatedAtMS: item.CreatedAtMS,
		UpdatedAtMS: item.UpdatedAtMS,
	}
}

func usageStatusError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &UsageError{
			StatusCode: http.StatusUnprocessableEntity,
			Code:       "clinepass_session_expired",
			Message:    "Cline session is expired or invalid",
		}
	case http.StatusPaymentRequired, http.StatusNotFound:
		return &UsageError{
			StatusCode: http.StatusUnprocessableEntity,
			Code:       "clinepass_subscription_unavailable",
			Message:    "No active ClinePass subscription was found",
		}
	case http.StatusTooManyRequests:
		return &UsageError{
			StatusCode: http.StatusTooManyRequests,
			Code:       "clinepass_upstream_rate_limited",
			Message:    "Cline usage service is rate limited",
		}
	default:
		return &UsageError{
			StatusCode: http.StatusBadGateway,
			Code:       "clinepass_upstream_failed",
			Message:    fmt.Sprintf("Cline usage service returned HTTP %d", status),
		}
	}
}

func usageLimitOrder(value string) int {
	switch value {
	case "five_hour":
		return 0
	case "weekly":
		return 1
	case "monthly":
		return 2
	default:
		return 100
	}
}
