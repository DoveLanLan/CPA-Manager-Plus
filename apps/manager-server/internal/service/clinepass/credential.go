package clinepass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

const (
	// clineSessionCookieName is the browser login cookie the legacy flow stored.
	clineSessionCookieName = "cline_session_id"
	// clineWorkOSPrefix is the stored form of a WorkOS access token. Cline's API
	// accepts `Bearer workos:<jwt>` and rejects the bare JWT, while a personal API
	// key is the mirror image: it is accepted bare and rejected when prefixed.
	clineWorkOSPrefix = "workos:"
	// clinePersonalKeyPrefix is the shape of a Cline personal API key. Keys are
	// created at https://app.cline.bot/dashboard/account and never rotate.
	clinePersonalKeyPrefix = "sk_"
	// clineRefreshSkew renews an access token slightly before it lapses so a
	// request is not raced by its own credential expiring mid-flight.
	clineRefreshSkew = 5 * time.Minute
)

// credentialInput is one operator-supplied credential after it has been read
// apart. A pasted Cline settings document can carry more than one secret, which
// is why this is not a single string.
type credentialInput struct {
	secret       string
	accessToken  string
	refreshToken string
	expiresAtMS  int64
	accountID    string
	email        string
	// hint is the kind implied by the value's shape, empty when the shape alone
	// cannot tell the kinds apart.
	hint string
}

// clineAuth is the credential actually attached to one upstream request.
type clineAuth struct {
	authorization string
	cookie        *http.Cookie
}

func (a clineAuth) apply(req *http.Request) {
	if a.authorization != "" {
		req.Header.Set("Authorization", a.authorization)
	}
	if a.cookie != nil {
		req.AddCookie(a.cookie)
	}
}

// formatClineAccessToken mirrors the formatter Cline's own clients use: a token
// already stored as `workos:<jwt>`, or a personal key, is sent as it stands.
func formatClineAccessToken(token string) string {
	if strings.HasPrefix(token, clinePersonalKeyPrefix) ||
		strings.HasPrefix(strings.ToLower(token), clineWorkOSPrefix) {
		return token
	}
	return clineWorkOSPrefix + token
}

// clineAuthFor builds the credential for one account. accessToken is only
// meaningful for the refresh kind, whose bearer is minted by the server.
func clineAuthFor(item model.ThirdPartyQuotaAccount, accessToken string) (clineAuth, error) {
	switch item.CredentialKind {
	case model.ThirdPartyQuotaCredentialAPIKey:
		if item.Credential == "" {
			return clineAuth{}, errors.New("clinepass API key is missing")
		}
		return clineAuth{authorization: "Bearer " + item.Credential}, nil
	case model.ThirdPartyQuotaCredentialAccessToken:
		if item.Credential == "" {
			return clineAuth{}, errors.New("clinepass access token is missing")
		}
		return clineAuth{authorization: "Bearer " + formatClineAccessToken(item.Credential)}, nil
	case model.ThirdPartyQuotaCredentialRefreshToken:
		if accessToken == "" {
			return clineAuth{}, errors.New("clinepass access token has not been resolved yet")
		}
		return clineAuth{authorization: "Bearer " + formatClineAccessToken(accessToken)}, nil
	case model.ThirdPartyQuotaCredentialSession, "":
		if item.Credential == "" {
			return clineAuth{}, errors.New("clinepass session value is missing")
		}
		return clineAuth{cookie: &http.Cookie{Name: clineSessionCookieName, Value: item.Credential}}, nil
	default:
		return clineAuth{}, fmt.Errorf("unsupported clinepass credential kind %q", item.CredentialKind)
	}
}

// parseClineCredential reads a pasted credential into its parts. A Cline
// settings document is recognised as JSON, everything else is read as one
// secret with whatever shape hints its kind.
func parseClineCredential(raw string, requestedKind string) (credentialInput, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		if input, ok := clineCredentialFromJSON(trimmed); ok {
			if requestedKind != "" {
				input.hint = requestedKind
			}
			return input, nil
		}
		// Falling through would report this as a malformed cookie, which hides the
		// real problem: the paste was JSON, but not the JSON that carries a token.
		return credentialInput{}, errors.New("clinepass settings JSON must contain accessToken or refreshToken")
	}
	// A paste that still carries the cookie name is a session cookie by the
	// operator's own words, which saves a probe to discover it.
	explicitSession := strings.Contains(trimmed, clineSessionCookieName+"=")
	secret, err := normalizeCredential(ProviderClinePass, trimmed)
	if err != nil {
		return credentialInput{}, err
	}
	hint := requestedKind
	switch {
	case hint != "":
	case explicitSession:
		hint = model.ThirdPartyQuotaCredentialSession
	default:
		hint = clineCredentialShapeHint(secret)
	}
	return credentialInput{secret: secret, hint: hint}, nil
}

// clineCredentialFromJSON accepts either a whole `providers.json` document or the
// bare auth object it nests, so an operator can paste whichever they have.
func clineCredentialFromJSON(raw string) (credentialInput, bool) {
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return credentialInput{}, false
	}
	auth := document
	if providers, ok := mapField(document, "providers"); ok {
		// Cline writes the ClinePass selection into the `cline` entry as well, so
		// `cline` is authoritative and `cline-pass` only a fallback for older files.
		for _, providerID := range []string{"cline", "cline-pass"} {
			entry, ok := mapField(providers, providerID)
			if !ok {
				continue
			}
			settings, ok := mapField(entry, "settings")
			if !ok {
				continue
			}
			candidate, ok := mapField(settings, "auth")
			if !ok {
				continue
			}
			auth = candidate
			break
		}
	}
	input := credentialInput{
		accessToken:  firstStringField(auth, "accessToken", "idToken"),
		refreshToken: firstStringField(auth, "refreshToken"),
		accountID:    firstStringField(auth, "accountId"),
	}
	if input.accessToken == "" && input.refreshToken == "" {
		return credentialInput{}, false
	}
	if userInfo, ok := mapField(auth, "metadata"); ok {
		if info, ok := mapField(userInfo, "userInfo"); ok {
			if input.accountID == "" {
				input.accountID = firstStringField(info, "clineUserId", "id")
			}
			input.email = firstStringField(info, "email")
		}
	}
	if value, ok := auth["expiresAt"]; ok {
		input.expiresAtMS = parseClineExpiry(value)
	}
	// The refresh token is the durable half, so it is what gets stored as the
	// account credential when the document carries one.
	if input.refreshToken != "" {
		input.secret = input.refreshToken
		input.hint = model.ThirdPartyQuotaCredentialRefreshToken
	} else {
		input.secret = input.accessToken
		input.hint = model.ThirdPartyQuotaCredentialAccessToken
	}
	return input, true
}

// clineCredentialShapeHint classifies a secret from its shape alone. An opaque
// value returns "", which is the case that needs a probe to tell a refresh token
// from a session cookie.
func clineCredentialShapeHint(secret string) string {
	switch {
	case strings.HasPrefix(secret, clinePersonalKeyPrefix):
		return model.ThirdPartyQuotaCredentialAPIKey
	case strings.HasPrefix(strings.ToLower(secret), clineWorkOSPrefix):
		return model.ThirdPartyQuotaCredentialAccessToken
	case isJWTShape(secret):
		return model.ThirdPartyQuotaCredentialAccessToken
	default:
		return ""
	}
}

// isJWTShape reports whether value is a three-segment base64url JWT. The first
// segment of a JWT header is always the base64url of `{"`, which is "eyJ".
func isJWTShape(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "eyJ") {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool {
			return !isBase64URLRune(r)
		}) {
			return false
		}
	}
	return true
}

func isBase64URLRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_':
		return true
	default:
		return false
	}
}

func mapField(container map[string]any, name string) (map[string]any, bool) {
	value, ok := container[name]
	if !ok {
		return nil, false
	}
	mapped, ok := value.(map[string]any)
	return mapped, ok
}

func firstStringField(container map[string]any, names ...string) string {
	for _, name := range names {
		value, ok := container[name]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

// parseClineExpiry accepts the shapes Cline has used for a token expiry: an
// RFC3339 string, or a number in seconds, milliseconds or microseconds.
func parseClineExpiry(value any) int64 {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return 0
		}
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			return parsed.UnixMilli()
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0
		}
		return normalizeClineEpoch(number)
	case float64:
		return normalizeClineEpoch(typed)
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0
		}
		return normalizeClineEpoch(number)
	default:
		return 0
	}
}

func normalizeClineEpoch(value float64) int64 {
	switch {
	case value >= 1e15:
		return int64(value / 1000)
	case value >= 1e12:
		return int64(value)
	case value >= 1e9:
		return int64(value * 1000)
	default:
		return 0
	}
}

// clineCredentialFromRefreshToken probes an opaque secret against the refresh
// endpoint. A shape that could be either a session cookie or a refresh token is
// decided by this call: only a refresh token is accepted here.
func (s *Service) clineCredentialFromRefreshToken(ctx context.Context, secret string) (clineRefreshResponse, bool) {
	response, err := s.requestClineRefresh(ctx, secret)
	if err != nil {
		return clineRefreshResponse{}, false
	}
	return response, true
}

type clineRefreshResponse struct {
	accessToken  string
	refreshToken string
	expiresAtMS  int64
	accountID    string
	email        string
}

// requestClineRefresh exchanges a refresh token for a fresh access token. Cline
// rotates the refresh token, so the caller must persist what comes back or the
// credential the server just retired stays on disk.
func (s *Service) requestClineRefresh(ctx context.Context, refreshToken string) (clineRefreshResponse, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return clineRefreshResponse{}, errors.New("clinepass refresh token is empty")
	}
	body, err := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	})
	if err != nil {
		return clineRefreshResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.clineEndpoint("/auth/refresh"), strings.NewReader(string(body)))
	if err != nil {
		return clineRefreshResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://app.cline.bot")
	req.Header.Set("Referer", "https://app.cline.bot/")
	req.Header.Set("User-Agent", clineUserAgent)

	res, err := s.client.Do(req)
	if err != nil {
		return clineRefreshResponse{}, upstreamUnavailable(ProviderClinePass)
	}
	defer res.Body.Close()
	payload, err := readUpstreamBody(res.Body)
	if err != nil {
		return clineRefreshResponse{}, providerReadError(ProviderClinePass, err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return clineRefreshResponse{}, usageStatusError(ProviderClinePass, res.StatusCode)
	}

	var envelope struct {
		Success bool `json:"success"`
		Data    *struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    any    `json:"expiresAt"`
			UserInfo     *struct {
				ClineUserID string `json:"clineUserId"`
				Email       string `json:"email"`
			} `json:"userInfo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || !envelope.Success || envelope.Data == nil {
		return clineRefreshResponse{}, invalidResponse(ProviderClinePass)
	}
	accessToken := strings.TrimSpace(envelope.Data.AccessToken)
	if accessToken == "" {
		return clineRefreshResponse{}, invalidResponse(ProviderClinePass)
	}
	response := clineRefreshResponse{
		accessToken:  accessToken,
		refreshToken: strings.TrimSpace(envelope.Data.RefreshToken),
		expiresAtMS:  parseClineExpiry(envelope.Data.ExpiresAt),
	}
	if envelope.Data.UserInfo != nil {
		response.accountID = strings.TrimSpace(envelope.Data.UserInfo.ClineUserID)
		response.email = strings.TrimSpace(envelope.Data.UserInfo.Email)
	}
	return response, nil
}

// clineAccessToken resolves the bearer for an account whose credential is a
// refresh token. `stale` is the token the caller already found refused, or ""
// when the caller is simply asking for a usable one.
//
// Rotation makes this the one place a race can destroy a credential: two
// concurrent refreshes each retire the other's token. The per-account lock plus
// the re-read inside it mean only one refresh is ever in flight.
func (s *Service) clineAccessToken(ctx context.Context, item *model.ThirdPartyQuotaAccount, stale string) (string, error) {
	if item.CredentialKind != model.ThirdPartyQuotaCredentialRefreshToken {
		return "", nil
	}
	if stale == "" && clineAccessTokenUsable(item, s.now()) {
		return item.AccessToken, nil
	}
	unlock := s.credentialLocks.lock(item.ID)
	defer unlock()
	if latest, ok, err := s.store.GetThirdPartyQuotaAccount(ctx, item.ID); err == nil && ok {
		if latest.AccessToken != "" && latest.AccessToken != stale && clineAccessTokenUsable(&latest, s.now()) {
			*item = latest
			return latest.AccessToken, nil
		}
	}
	return s.refreshClineAccessToken(ctx, item)
}

func clineAccessTokenUsable(item *model.ThirdPartyQuotaAccount, now time.Time) bool {
	if item.AccessToken == "" {
		return false
	}
	// No reported expiry means the token is used until the server refuses it.
	if item.AccessTokenExpiresAtMS == 0 {
		return true
	}
	return now.Add(clineRefreshSkew).UnixMilli() < item.AccessTokenExpiresAtMS
}

// refreshClineAccessToken performs one refresh and persists the rotated
// credential. It must be called with the account lock held.
func (s *Service) refreshClineAccessToken(ctx context.Context, item *model.ThirdPartyQuotaAccount) (string, error) {
	refreshToken := item.RefreshToken
	if refreshToken == "" {
		refreshToken = item.Credential
	}
	response, err := s.requestClineRefresh(ctx, refreshToken)
	if err != nil {
		return "", err
	}
	item.AccessToken = response.accessToken
	if response.expiresAtMS > 0 {
		item.AccessTokenExpiresAtMS = response.expiresAtMS
	}
	// Cline retires the token it just replaced, so the new one has to be written
	// back before anything else can use the account.
	if response.refreshToken != "" {
		item.RefreshToken = response.refreshToken
		item.Credential = response.refreshToken
	}
	if response.accountID != "" {
		item.AccountUserID = response.accountID
	}
	if response.email != "" {
		item.AccountEmail = response.email
	}
	item.UpdatedAtMS = s.now().UnixMilli()
	if err := s.store.UpsertThirdPartyQuotaAccount(ctx, *item); err != nil {
		return "", err
	}
	return item.AccessToken, nil
}

// keyedMutex serialises work per key without holding a global lock, and drops
// each entry once no caller holds it.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedMutexEntry
}

type keyedMutexEntry struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*keyedMutexEntry)
	}
	entry, ok := k.locks[key]
	if !ok {
		entry = &keyedMutexEntry{}
		k.locks[key] = entry
	}
	entry.refs++
	k.mu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
