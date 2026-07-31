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
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

const (
	ProviderClinePass  = "clinepass"
	ProviderOpenCodeGo = "opencode_go"

	defaultClinePassUsageLimitsURL   = "https://api.cline.bot/api/v1/users/me/plan/usage-limits"
	defaultOpenCodeServerURL         = "https://opencode.ai/_server"
	defaultOpenCodeServerFunctionID  = "c7389bd0e731f80f49593e5ee53835475f4e28594dd6bd83eb229bab753498cd"
	maxCredentialBytes               = 16 * 1024
	maxUpstreamBodyBytes             = 128 * 1024
	maxOpenCodeServerFunctionIDBytes = 256
	maxOpenCodeWorkspaceIDBytes      = 256
)

var (
	ErrAccountNotFound = errors.New("third-party quota account not found")
	workspaceIDPattern = regexp.MustCompile(`^wrk_[A-Za-z0-9_-]+$`)
	serverIDPattern    = regexp.MustCompile(`^[a-fA-F0-9]{32,256}$`)
)

type accountStore interface {
	ListThirdPartyQuotaAccounts(ctx context.Context) ([]model.ThirdPartyQuotaAccount, error)
	GetThirdPartyQuotaAccount(ctx context.Context, id string) (model.ThirdPartyQuotaAccount, bool, error)
	UpsertThirdPartyQuotaAccount(ctx context.Context, account model.ThirdPartyQuotaAccount) error
	DeleteThirdPartyQuotaAccount(ctx context.Context, id string) (bool, error)
}

type Option func(*Service)

func WithHTTPClient(client *http.Client) Option {
	return func(service *Service) {
		if client != nil {
			service.client = client
		}
	}
}

// WithUsageLimitsURL is retained for compatibility with existing ClinePass tests.
func WithUsageLimitsURL(rawURL string) Option {
	return WithClinePassUsageLimitsURL(rawURL)
}

func WithClinePassUsageLimitsURL(rawURL string) Option {
	return func(service *Service) {
		if value := strings.TrimSpace(rawURL); value != "" {
			service.clinePassUsageLimitsURL = value
		}
	}
}

func WithOpenCodeServerURL(rawURL string) Option {
	return func(service *Service) {
		if value := strings.TrimSpace(rawURL); value != "" {
			service.openCodeServerURL = value
		}
	}
}

type Service struct {
	store                   accountStore
	client                  *http.Client
	clinePassUsageLimitsURL string
	openCodeServerURL       string
}

type Account struct {
	ID               string `json:"id"`
	Provider         string `json:"provider"`
	Label            string `json:"label"`
	WorkspaceID      string `json:"workspaceId,omitempty"`
	ServerFunctionID string `json:"serverFunctionId,omitempty"`
	CreatedAtMS      int64  `json:"createdAtMs"`
	UpdatedAtMS      int64  `json:"updatedAtMs"`
}

type CreateAccountRequest struct {
	Provider         string `json:"provider"`
	Label            string `json:"label"`
	Credential       string `json:"credential"`
	WorkspaceID      string `json:"workspaceId,omitempty"`
	ServerFunctionID string `json:"serverFunctionId,omitempty"`
}

type UpdateAccountRequest struct {
	Label            *string `json:"label,omitempty"`
	Credential       *string `json:"credential,omitempty"`
	WorkspaceID      *string `json:"workspaceId,omitempty"`
	ServerFunctionID *string `json:"serverFunctionId,omitempty"`
}

type UsageLimit struct {
	Type        string  `json:"type"`
	PercentUsed float64 `json:"percentUsed"`
	ResetsAt    string  `json:"resetsAt,omitempty"`
}

type UsageLimits struct {
	AccountID   string       `json:"accountId"`
	Provider    string       `json:"provider"`
	FetchedAtMS int64        `json:"fetchedAtMs"`
	Limits      []UsageLimit `json:"limits"`
	Mine        *bool        `json:"mine,omitempty"`
	UseBalance  *bool        `json:"useBalance,omitempty"`
	Regions     []string     `json:"regions,omitempty"`
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
		store:                   store,
		client:                  client,
		clinePassUsageLimitsURL: defaultClinePassUsageLimitsURL,
		openCodeServerURL:       defaultOpenCodeServerURL,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) ListAccounts(ctx context.Context) ([]Account, error) {
	items, err := s.store.ListThirdPartyQuotaAccounts(ctx)
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
	provider, err := normalizeProvider(request.Provider)
	if err != nil {
		return Account{}, err
	}
	label, err := normalizeLabel(request.Label)
	if err != nil {
		return Account{}, err
	}
	credential, err := normalizeCredential(provider, request.Credential)
	if err != nil {
		return Account{}, err
	}
	workspaceID, serverFunctionID, err := normalizeProviderConfig(
		provider,
		request.WorkspaceID,
		request.ServerFunctionID,
		true,
	)
	if err != nil {
		return Account{}, err
	}
	id, err := newAccountID()
	if err != nil {
		return Account{}, err
	}
	now := time.Now().UnixMilli()
	item := model.ThirdPartyQuotaAccount{
		ID:               id,
		Provider:         provider,
		Label:            label,
		Credential:       credential,
		WorkspaceID:      workspaceID,
		ServerFunctionID: serverFunctionID,
		CreatedAtMS:      now,
		UpdatedAtMS:      now,
	}
	if err := s.store.UpsertThirdPartyQuotaAccount(ctx, item); err != nil {
		return Account{}, err
	}
	return accountView(item), nil
}

func (s *Service) UpdateAccount(ctx context.Context, id string, request UpdateAccountRequest) (Account, error) {
	id = strings.TrimSpace(id)
	item, ok, err := s.store.GetThirdPartyQuotaAccount(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	if request.Label == nil && request.Credential == nil && request.WorkspaceID == nil && request.ServerFunctionID == nil {
		return Account{}, errors.New("at least one account field is required")
	}
	if request.Label != nil {
		item.Label, err = normalizeLabel(*request.Label)
		if err != nil {
			return Account{}, err
		}
	}
	if request.Credential != nil {
		item.Credential, err = normalizeCredential(item.Provider, *request.Credential)
		if err != nil {
			return Account{}, err
		}
	}
	workspaceID := item.WorkspaceID
	serverFunctionID := item.ServerFunctionID
	if request.WorkspaceID != nil {
		workspaceID = *request.WorkspaceID
	}
	if request.ServerFunctionID != nil {
		serverFunctionID = *request.ServerFunctionID
	}
	item.WorkspaceID, item.ServerFunctionID, err = normalizeProviderConfig(
		item.Provider,
		workspaceID,
		serverFunctionID,
		false,
	)
	if err != nil {
		return Account{}, err
	}
	item.UpdatedAtMS = time.Now().UnixMilli()
	if err := s.store.UpsertThirdPartyQuotaAccount(ctx, item); err != nil {
		return Account{}, err
	}
	return accountView(item), nil
}

func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	deleted, err := s.store.DeleteThirdPartyQuotaAccount(ctx, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !deleted {
		return ErrAccountNotFound
	}
	return nil
}

func (s *Service) FetchUsageLimits(ctx context.Context, id string) (UsageLimits, error) {
	item, ok, err := s.store.GetThirdPartyQuotaAccount(ctx, strings.TrimSpace(id))
	if err != nil {
		return UsageLimits{}, err
	}
	if !ok {
		return UsageLimits{}, ErrAccountNotFound
	}
	switch item.Provider {
	case ProviderClinePass:
		return s.fetchClinePassUsageLimits(ctx, item)
	case ProviderOpenCodeGo:
		return s.fetchOpenCodeGoUsageLimits(ctx, item)
	default:
		return UsageLimits{}, errors.New("unsupported third-party quota provider")
	}
}

func (s *Service) fetchClinePassUsageLimits(ctx context.Context, item model.ThirdPartyQuotaAccount) (UsageLimits, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.clinePassUsageLimitsURL, nil)
	if err != nil {
		return UsageLimits{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://app.cline.bot")
	req.Header.Set("Referer", "https://app.cline.bot/")
	req.Header.Set("User-Agent", "CPA-Manager-Plus third-party quota monitor")
	req.AddCookie(&http.Cookie{Name: "cline_session_id", Value: item.Credential})

	res, err := s.client.Do(req)
	if err != nil {
		return UsageLimits{}, upstreamUnavailable(ProviderClinePass)
	}
	defer res.Body.Close()
	body, err := readUpstreamBody(res.Body)
	if err != nil {
		return UsageLimits{}, providerReadError(ProviderClinePass, err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return UsageLimits{}, usageStatusError(ProviderClinePass, res.StatusCode)
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
		return UsageLimits{}, invalidResponse(ProviderClinePass)
	}

	limits := make([]UsageLimit, 0, len(envelope.Data.Limits))
	for _, raw := range envelope.Data.Limits {
		limitType := strings.TrimSpace(raw.Type)
		if limitType == "" {
			continue
		}
		limits = append(limits, UsageLimit{
			Type:        limitType,
			PercentUsed: normalizePercent(raw.PercentUsed),
			ResetsAt:    strings.TrimSpace(raw.ResetsAt),
		})
	}
	sortUsageLimits(limits)
	return UsageLimits{
		AccountID:   item.ID,
		Provider:    item.Provider,
		FetchedAtMS: time.Now().UnixMilli(),
		Limits:      limits,
	}, nil
}

type openCodeUsageWindow struct {
	UsagePercent *float64 `json:"usagePercent"`
	ResetInSec   *float64 `json:"resetInSec"`
}

type openCodeSubscription struct {
	Mine         bool                `json:"mine"`
	UseBalance   bool                `json:"useBalance"`
	Region       []string            `json:"region"`
	RollingUsage openCodeUsageWindow `json:"rollingUsage"`
	WeeklyUsage  openCodeUsageWindow `json:"weeklyUsage"`
	MonthlyUsage openCodeUsageWindow `json:"monthlyUsage"`
}

func (s *Service) fetchOpenCodeGoUsageLimits(ctx context.Context, item model.ThirdPartyQuotaAccount) (UsageLimits, error) {
	serverURL, err := url.Parse(s.openCodeServerURL)
	if err != nil {
		return UsageLimits{}, err
	}
	args, err := openCodeServerArgs(item.WorkspaceID)
	if err != nil {
		return UsageLimits{}, err
	}
	query := serverURL.Query()
	query.Set("id", item.ServerFunctionID)
	query.Set("args", args)
	serverURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL.String(), nil)
	if err != nil {
		return UsageLimits{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://opencode.ai")
	req.Header.Set("Referer", "https://opencode.ai/workspace/"+item.WorkspaceID+"/members")
	req.Header.Set("User-Agent", "CPA-Manager-Plus third-party quota monitor")
	req.Header.Set("X-Server-ID", item.ServerFunctionID)
	// OpenCode's SolidStart no-JS path assumes its final argument is FormData,
	// while this query takes a string workspace ID. Use the normal server-function
	// transport and parse only the narrow data subset we expect from its JS stream.
	req.Header.Set("X-Server-Instance", "server-fn:2")
	req.AddCookie(&http.Cookie{Name: "auth", Value: item.Credential})

	res, err := s.client.Do(req)
	if err != nil {
		return UsageLimits{}, upstreamUnavailable(ProviderOpenCodeGo)
	}
	defer res.Body.Close()
	body, err := readUpstreamBody(res.Body)
	if err != nil {
		return UsageLimits{}, providerReadError(ProviderOpenCodeGo, err)
	}

	if res.StatusCode == http.StatusNotFound {
		return UsageLimits{}, &UsageError{
			StatusCode: http.StatusUnprocessableEntity,
			Code:       "opencode_go_server_function_outdated",
			Message:    "OpenCode Go server function ID is no longer valid",
		}
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return UsageLimits{}, usageStatusError(ProviderOpenCodeGo, res.StatusCode)
	}
	location := strings.ToLower(res.Header.Get("Location"))
	if strings.Contains(location, "/auth") || strings.TrimSpace(res.Header.Get("X-Error")) != "" {
		return UsageLimits{}, usageStatusError(ProviderOpenCodeGo, http.StatusUnauthorized)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return UsageLimits{}, usageStatusError(ProviderOpenCodeGo, res.StatusCode)
	}

	subscription, ok := parseOpenCodeJSResponse(body)
	if !ok {
		return UsageLimits{}, invalidResponse(ProviderOpenCodeGo)
	}
	fetchedAt := time.Now()
	limits := make([]UsageLimit, 0, 3)
	for _, window := range []struct {
		typeName string
		usage    openCodeUsageWindow
	}{
		{typeName: "five_hour", usage: subscription.RollingUsage},
		{typeName: "weekly", usage: subscription.WeeklyUsage},
		{typeName: "monthly", usage: subscription.MonthlyUsage},
	} {
		if window.usage.UsagePercent == nil || window.usage.ResetInSec == nil {
			continue
		}
		resetSeconds := math.Max(0, *window.usage.ResetInSec)
		limits = append(limits, UsageLimit{
			Type:        window.typeName,
			PercentUsed: normalizePercent(window.usage.UsagePercent),
			ResetsAt:    fetchedAt.Add(time.Duration(resetSeconds * float64(time.Second))).UTC().Format(time.RFC3339),
		})
	}
	if len(limits) == 0 {
		return UsageLimits{}, invalidResponse(ProviderOpenCodeGo)
	}
	mine := subscription.Mine
	useBalance := subscription.UseBalance
	return UsageLimits{
		AccountID:   item.ID,
		Provider:    item.Provider,
		FetchedAtMS: fetchedAt.UnixMilli(),
		Limits:      limits,
		Mine:        &mine,
		UseBalance:  &useBalance,
		Regions:     subscription.Region,
	}, nil
}

func openCodeServerArgs(workspaceID string) (string, error) {
	type stringNode struct {
		Type  int    `json:"t"`
		Value string `json:"s"`
	}
	type tupleNode struct {
		Type   int          `json:"t"`
		Index  int          `json:"i"`
		Length int          `json:"l"`
		Args   []stringNode `json:"a"`
		Offset int          `json:"o"`
	}
	payload := struct {
		Tuple    tupleNode `json:"t"`
		Features int       `json:"f"`
		Meta     []any     `json:"m"`
	}{
		Tuple: tupleNode{
			Type:   9,
			Index:  0,
			Length: 1,
			Args:   []stringNode{{Type: 1, Value: workspaceID}},
			Offset: 0,
		},
		Features: 31,
		Meta:     []any{},
	}
	body, err := json.Marshal(payload)
	return string(body), err
}

func parseOpenCodeJSResponse(body []byte) (openCodeSubscription, bool) {
	script, ok := firstServerFunctionChunk(body)
	if !ok {
		return openCodeSubscription{}, false
	}
	mine, ok := parseJSBoolProperty(script, "mine")
	if !ok {
		return openCodeSubscription{}, false
	}
	useBalance, ok := parseJSBoolProperty(script, "useBalance")
	if !ok {
		return openCodeSubscription{}, false
	}
	regions, ok := parseJSStringArrayProperty(script, "region")
	if !ok {
		return openCodeSubscription{}, false
	}
	rollingUsage, ok := parseJSUsageWindow(script, "rollingUsage")
	if !ok {
		return openCodeSubscription{}, false
	}
	weeklyUsage, ok := parseJSUsageWindow(script, "weeklyUsage")
	if !ok {
		return openCodeSubscription{}, false
	}
	monthlyUsage, ok := parseJSUsageWindow(script, "monthlyUsage")
	if !ok {
		return openCodeSubscription{}, false
	}
	return openCodeSubscription{
		Mine:         mine,
		UseBalance:   useBalance,
		Region:       regions,
		RollingUsage: rollingUsage,
		WeeklyUsage:  weeklyUsage,
		MonthlyUsage: monthlyUsage,
	}, true
}

func firstServerFunctionChunk(body []byte) (string, bool) {
	if len(body) < 12 || string(body[:3]) != ";0x" || body[11] != ';' {
		return "", false
	}
	length, err := strconv.ParseUint(string(body[3:11]), 16, 32)
	if err != nil || length == 0 || uint64(len(body)-12) < length {
		return "", false
	}
	return string(body[12 : 12+int(length)]), true
}

func parseJSBoolProperty(script, property string) (bool, bool) {
	value, ok := jsPropertyValue(script, property)
	if !ok {
		return false, false
	}
	switch {
	case strings.HasPrefix(value, "!0"), strings.HasPrefix(value, "true"):
		return true, true
	case strings.HasPrefix(value, "!1"), strings.HasPrefix(value, "false"):
		return false, true
	default:
		return false, false
	}
}

func parseJSStringArrayProperty(script, property string) ([]string, bool) {
	value, ok := jsPropertyValue(script, property)
	if !ok {
		return nil, false
	}
	value = skipSerovalReferenceAssignment(value)
	if !strings.HasPrefix(value, "[") {
		return nil, false
	}
	end := strings.IndexByte(value, ']')
	if end < 0 {
		return nil, false
	}
	var result []string
	if err := json.Unmarshal([]byte(value[:end+1]), &result); err != nil {
		return nil, false
	}
	return result, true
}

func parseJSUsageWindow(script, property string) (openCodeUsageWindow, bool) {
	value, ok := jsPropertyValue(script, property)
	if !ok {
		return openCodeUsageWindow{}, false
	}
	value = skipSerovalReferenceAssignment(value)
	if !strings.HasPrefix(value, "{") {
		return openCodeUsageWindow{}, false
	}
	end := strings.IndexByte(value, '}')
	if end < 0 {
		return openCodeUsageWindow{}, false
	}
	object := value[:end+1]
	resetInSec, ok := parseJSNumberProperty(object, "resetInSec")
	if !ok {
		return openCodeUsageWindow{}, false
	}
	usagePercent, ok := parseJSNumberProperty(object, "usagePercent")
	if !ok {
		return openCodeUsageWindow{}, false
	}
	return openCodeUsageWindow{ResetInSec: &resetInSec, UsagePercent: &usagePercent}, true
}

func parseJSNumberProperty(script, property string) (float64, bool) {
	value, ok := jsPropertyValue(script, property)
	if !ok {
		return 0, false
	}
	end := 0
	for end < len(value) && strings.ContainsRune("+-0123456789.eE", rune(value[end])) {
		end++
	}
	if end == 0 {
		return 0, false
	}
	number, err := strconv.ParseFloat(value[:end], 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func jsPropertyValue(script, property string) (string, bool) {
	needle := property + ":"
	index := strings.Index(script, needle)
	if index < 0 {
		return "", false
	}
	return script[index+len(needle):], true
}

func skipSerovalReferenceAssignment(value string) string {
	if !strings.HasPrefix(value, "$R[") {
		return value
	}
	end := strings.Index(value, "]=")
	if end < 0 {
		return value
	}
	return value[end+2:]
}

func normalizeProvider(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case ProviderClinePass, ProviderOpenCodeGo:
		return value, nil
	default:
		return "", errors.New("provider must be clinepass or opencode_go")
	}
}

func normalizeProviderConfig(provider, workspaceID, serverFunctionID string, useDefaultServerID bool) (string, string, error) {
	if provider == ProviderClinePass {
		return "", "", nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", "", errors.New("OpenCode workspaceId is required")
	}
	if len(workspaceID) > maxOpenCodeWorkspaceIDBytes || !workspaceIDPattern.MatchString(workspaceID) {
		return "", "", errors.New("OpenCode workspaceId is invalid")
	}
	serverFunctionID = strings.TrimSpace(serverFunctionID)
	if serverFunctionID == "" && useDefaultServerID {
		serverFunctionID = defaultOpenCodeServerFunctionID
	}
	if serverFunctionID == "" {
		return "", "", errors.New("OpenCode serverFunctionId is required")
	}
	if len(serverFunctionID) > maxOpenCodeServerFunctionIDBytes || !serverIDPattern.MatchString(serverFunctionID) {
		return "", "", errors.New("OpenCode serverFunctionId is invalid")
	}
	return workspaceID, strings.ToLower(serverFunctionID), nil
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

func normalizeCredential(provider, value string) (string, error) {
	cookieName := "cline_session_id"
	if provider == ProviderOpenCodeGo {
		cookieName = "auth"
	}
	value = strings.TrimSpace(value)
	if index := strings.Index(value, cookieName+"="); index >= 0 {
		value = value[index+len(cookieName)+1:]
		if end := strings.IndexAny(value, ";'\" \t\r\n"); end >= 0 {
			value = value[:end]
		}
	}
	value = strings.Trim(strings.TrimSpace(value), "'\"")
	if value == "" {
		return "", fmt.Errorf("%s is required", cookieName)
	}
	if len(value) > maxCredentialBytes {
		return "", fmt.Errorf("%s must be at most %d bytes", cookieName, maxCredentialBytes)
	}
	for _, char := range value {
		if char <= 0x20 || char >= 0x7f || strings.ContainsRune("\";,\\", char) {
			return "", fmt.Errorf("%s contains invalid cookie characters", cookieName)
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

func accountView(item model.ThirdPartyQuotaAccount) Account {
	return Account{
		ID:               item.ID,
		Provider:         item.Provider,
		Label:            item.Label,
		WorkspaceID:      item.WorkspaceID,
		ServerFunctionID: item.ServerFunctionID,
		CreatedAtMS:      item.CreatedAtMS,
		UpdatedAtMS:      item.UpdatedAtMS,
	}
}

func readUpstreamBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxUpstreamBodyBytes {
		return nil, errors.New("upstream response is too large")
	}
	return body, nil
}

func providerReadError(provider string, err error) error {
	codePrefix, label := providerErrorPrefix(provider)
	code := codePrefix + "_upstream_read_failed"
	message := "Failed to read " + label + " usage response"
	if strings.Contains(err.Error(), "too large") {
		code = codePrefix + "_upstream_response_too_large"
		message = label + " usage response is too large"
	}
	return &UsageError{StatusCode: http.StatusBadGateway, Code: code, Message: message}
}

func upstreamUnavailable(provider string) error {
	codePrefix, label := providerErrorPrefix(provider)
	return &UsageError{
		StatusCode: http.StatusBadGateway,
		Code:       codePrefix + "_upstream_unavailable",
		Message:    label + " usage service is unavailable",
	}
}

func invalidResponse(provider string) error {
	codePrefix, label := providerErrorPrefix(provider)
	return &UsageError{
		StatusCode: http.StatusBadGateway,
		Code:       codePrefix + "_upstream_invalid_response",
		Message:    label + " returned an invalid usage response",
	}
}

func usageStatusError(provider string, status int) error {
	codePrefix, label := providerErrorPrefix(provider)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusFound, http.StatusTemporaryRedirect:
		return &UsageError{
			StatusCode: http.StatusUnprocessableEntity,
			Code:       codePrefix + "_session_expired",
			Message:    label + " login session is expired or invalid",
		}
	case http.StatusPaymentRequired, http.StatusNotFound:
		return &UsageError{
			StatusCode: http.StatusUnprocessableEntity,
			Code:       codePrefix + "_subscription_unavailable",
			Message:    "No active " + label + " subscription was found",
		}
	case http.StatusTooManyRequests:
		return &UsageError{
			StatusCode: http.StatusTooManyRequests,
			Code:       codePrefix + "_upstream_rate_limited",
			Message:    label + " usage service is rate limited",
		}
	default:
		return &UsageError{
			StatusCode: http.StatusBadGateway,
			Code:       codePrefix + "_upstream_failed",
			Message:    fmt.Sprintf("%s usage service returned HTTP %d", label, status),
		}
	}
}

func providerErrorPrefix(provider string) (string, string) {
	if provider == ProviderOpenCodeGo {
		return "opencode_go", "OpenCode Go"
	}
	return "clinepass", "Cline"
}

func normalizePercent(value *float64) float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0
	}
	return math.Max(0, math.Min(100, *value))
}

func sortUsageLimits(limits []UsageLimit) {
	sort.SliceStable(limits, func(i, j int) bool {
		return usageLimitOrder(limits[i].Type) < usageLimitOrder(limits[j].Type)
	})
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
