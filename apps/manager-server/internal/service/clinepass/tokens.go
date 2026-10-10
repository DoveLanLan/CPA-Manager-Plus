package clinepass

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

const (
	// defaultLifetimeMonths bounds how far back the all-time walk reaches. Cline
	// exposes no account creation date through this API, so the window is fixed
	// and wide enough to cover a ClinePass subscription's whole life.
	defaultLifetimeMonths = 24
	// defaultLifetimeTTL is how long a recorded all-time total is trusted. The
	// walk costs one request per month, so it is not repeated on every refresh.
	defaultLifetimeTTL = 6 * time.Hour
	// lifetimeChunkConcurrency bounds how many month chunks are read at once.
	lifetimeChunkConcurrency = 4
	// defaultLifetimeTimeout caps the whole walk. Chunks that do not finish are
	// reported as partial rather than holding a page load open behind an upstream
	// that has stopped answering.
	defaultLifetimeTimeout = 20 * time.Second
)

// TokenUsage is how many tokens the vendor recorded for one account.
//
// The two totals come from the same per-day aggregate endpoint. The all-time
// total is walked month by month rather than read from the row-level ledger
// because that ledger silently caps at 200 rows per page and would under-report
// a busy account without saying so.
type TokenUsage struct {
	MonthToDate        int64 `json:"monthToDate"`
	Lifetime           int64 `json:"lifetime"`
	LifetimePartial    bool  `json:"lifetimePartial,omitempty"`
	LifetimeSyncedAtMS int64 `json:"lifetimeSyncedAtMs,omitempty"`
	LifetimeCached     bool  `json:"lifetimeCached,omitempty"`
	MonthToDateFailed  bool  `json:"monthToDateFailed,omitempty"`
	LifetimeFailed     bool  `json:"lifetimeFailed,omitempty"`
}

// clineRequest performs one authenticated GET against an absolute Cline target
// and decodes its `data` payload. The envelope is not contractual, so a body
// that carries the payload directly is accepted too.
func (s *Service) clineRequest(ctx context.Context, auth clineAuth, target string, destination any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://app.cline.bot")
	req.Header.Set("Referer", "https://app.cline.bot/")
	req.Header.Set("User-Agent", clineUserAgent)
	auth.apply(req)

	res, err := s.client.Do(req)
	if err != nil {
		return upstreamUnavailable(ProviderClinePass)
	}
	defer res.Body.Close()
	payload, err := readUpstreamBody(res.Body)
	if err != nil {
		return providerReadError(ProviderClinePass, err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return usageStatusError(ProviderClinePass, res.StatusCode)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return invalidResponse(ProviderClinePass)
	}
	body := envelope.Data
	if len(body) == 0 || string(body) == "null" {
		body = payload
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return invalidResponse(ProviderClinePass)
	}
	return nil
}

// clineGetData reads one endpoint expressed as a path below the API base.
func (s *Service) clineGetData(ctx context.Context, auth clineAuth, endpoint string, query url.Values, destination any) error {
	target := s.clineEndpoint(endpoint)
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	return s.clineRequest(ctx, auth, target, destination)
}

// clineIdentity resolves the vendor account id the usage endpoints are keyed by,
// and the email worth showing beside the account when it is not already known.
func (s *Service) clineIdentity(ctx context.Context, auth clineAuth, cachedUserID string) (string, string, error) {
	if cached := strings.TrimSpace(cachedUserID); cached != "" {
		return cached, "", nil
	}
	var profile struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := s.clineGetData(ctx, auth, "/users/me", nil, &profile); err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(profile.ID)
	if id == "" {
		return "", "", invalidResponse(ProviderClinePass)
	}
	return id, strings.TrimSpace(profile.Email), nil
}

// clineDailyTokens sums one date range of the vendor's own per-day aggregation.
func (s *Service) clineDailyTokens(ctx context.Context, auth clineAuth, userID, startDate, endDate string) (int64, error) {
	var payload struct {
		Items []struct {
			TotalTokens      *float64 `json:"totalTokens"`
			PromptTokens     *float64 `json:"promptTokens"`
			CompletionTokens *float64 `json:"completionTokens"`
		} `json:"items"`
	}
	query := url.Values{}
	query.Set("startDate", startDate)
	query.Set("endDate", endDate)
	endpoint := "/users/" + url.PathEscape(userID) + "/usages/daily"
	if err := s.clineGetData(ctx, auth, endpoint, query, &payload); err != nil {
		return 0, err
	}
	var total int64
	for _, row := range payload.Items {
		total += clineRowTokens(row.TotalTokens, row.PromptTokens, row.CompletionTokens)
	}
	return total, nil
}

// clineRowTokens prefers the vendor's own total and falls back to the two halves
// when a row omits it.
func clineRowTokens(total, prompt, completion *float64) int64 {
	if total != nil && *total > 0 {
		return int64(*total)
	}
	sum := 0.0
	if prompt != nil && *prompt > 0 {
		sum += *prompt
	}
	if completion != nil && *completion > 0 {
		sum += *completion
	}
	return int64(sum)
}

// clineUsageChunks splits the walk into calendar months ending today, so one
// busy stretch cannot truncate the reading the way a single wide range might.
func clineUsageChunks(now time.Time, months int) [][2]string {
	if months < 1 {
		months = 1
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(months - 1), 0)
	chunks := make([][2]string, 0, months)
	for cursor := first; !cursor.After(today); cursor = cursor.AddDate(0, 1, 0) {
		end := cursor.AddDate(0, 1, -1)
		if end.After(today) {
			end = today
		}
		chunks = append(chunks, [2]string{
			cursor.Format("2006-01-02"),
			end.Format("2006-01-02"),
		})
	}
	return chunks
}

// walkClineDailyTokens reads every month chunk and reports whether the whole
// window was readable. A chunk that fails lowers the total's accuracy rather
// than losing the reading entirely, which is what partial records.
func (s *Service) walkClineDailyTokens(ctx context.Context, auth clineAuth, userID string) (int64, bool) {
	walkCtx, cancel := context.WithTimeout(ctx, s.lifetimeTimeout)
	defer cancel()

	chunks := clineUsageChunks(s.now(), s.lifetimeMonths)
	totals := make([]int64, len(chunks))
	failures := make([]bool, len(chunks))

	semaphore := make(chan struct{}, lifetimeChunkConcurrency)
	var wg sync.WaitGroup
	for index, chunk := range chunks {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(index int, start, end string) {
			defer wg.Done()
			defer func() { <-semaphore }()
			total, err := s.clineDailyTokens(walkCtx, auth, userID, start, end)
			if err != nil {
				failures[index] = true
				return
			}
			totals[index] = total
		}(index, chunk[0], chunk[1])
	}
	wg.Wait()

	var total int64
	complete := true
	for index := range totals {
		if failures[index] {
			complete = false
			continue
		}
		total += totals[index]
	}
	return total, complete
}

// clineLifetimeTokens returns the recorded all-time total, walking the history
// when the record is missing, forced, or past its time to live. The returned
// dirty flag tells the caller the account row needs writing back.
func (s *Service) clineLifetimeTokens(
	ctx context.Context,
	item *model.ThirdPartyQuotaAccount,
	auth clineAuth,
	userID string,
	force bool,
) (total int64, partial bool, syncedAtMS int64, cached bool, dirty bool) {
	if !force && s.lifetimeRecordUsable(item) {
		return item.LifetimeTokens, item.LifetimeTokensPartial, item.LifetimeTokensSyncedAtMS, true, false
	}

	// The walk is expensive and a refresh button is easy to press twice; the lock
	// keeps one account to one walk, and the re-read below reuses its result.
	unlock := s.lifetimeLocks.lock(item.ID)
	defer unlock()
	if latest, ok, err := s.store.GetThirdPartyQuotaAccount(ctx, item.ID); err == nil && ok {
		if !force && s.lifetimeRecordUsable(&latest) {
			item.LifetimeTokens = latest.LifetimeTokens
			item.LifetimeTokensPartial = latest.LifetimeTokensPartial
			item.LifetimeTokensSyncedAtMS = latest.LifetimeTokensSyncedAtMS
			return latest.LifetimeTokens, latest.LifetimeTokensPartial, latest.LifetimeTokensSyncedAtMS, true, false
		}
	}

	walked, complete := s.walkClineDailyTokens(ctx, auth, userID)
	item.LifetimeTokens = walked
	item.LifetimeTokensPartial = !complete
	item.LifetimeTokensSyncedAtMS = s.now().UnixMilli()
	return walked, !complete, item.LifetimeTokensSyncedAtMS, false, true
}

func (s *Service) lifetimeRecordUsable(item *model.ThirdPartyQuotaAccount) bool {
	if item.LifetimeTokensSyncedAtMS <= 0 {
		return false
	}
	return s.now().Sub(time.UnixMilli(item.LifetimeTokensSyncedAtMS)) < s.lifetimeTTL
}

// fetchClineTokenUsage reads both token totals. It is best effort: the quota
// windows are the account's primary reading, so a usage endpoint that is down
// must not take them down with it. The returned flag reports whether the account
// row learned anything worth persisting.
func (s *Service) fetchClineTokenUsage(
	ctx context.Context,
	item *model.ThirdPartyQuotaAccount,
	auth clineAuth,
	force bool,
) (*TokenUsage, bool) {
	userID, email, err := s.clineIdentity(ctx, auth, item.AccountUserID)
	if err != nil {
		return nil, false
	}
	dirty := false
	if item.AccountUserID != userID {
		item.AccountUserID = userID
		dirty = true
	}
	if email != "" && item.AccountEmail != email {
		item.AccountEmail = email
		dirty = true
	}

	now := s.now()
	usage := &TokenUsage{}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	if month, monthErr := s.clineDailyTokens(ctx, auth, userID, monthStart, today); monthErr == nil {
		usage.MonthToDate = month
	} else {
		usage.MonthToDateFailed = true
	}

	lifetime, partial, syncedAtMS, cached, lifetimeDirty := s.clineLifetimeTokens(ctx, item, auth, userID, force)
	usage.Lifetime = lifetime
	usage.LifetimePartial = partial
	usage.LifetimeSyncedAtMS = syncedAtMS
	usage.LifetimeCached = cached
	if lifetimeDirty {
		dirty = true
	}
	if partial && lifetime == 0 {
		usage.LifetimeFailed = true
	}

	if usage.MonthToDateFailed && usage.LifetimeFailed {
		return nil, dirty
	}
	return usage, dirty
}
