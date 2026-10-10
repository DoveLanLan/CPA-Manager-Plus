package model

// Credential kinds a third-party quota account can carry. The kind decides how
// requests to the vendor are authenticated and whether the credential can renew
// itself without operator action.
const (
	// ThirdPartyQuotaCredentialAPIKey is a vendor-issued personal key. It is
	// long lived and does not rotate, so it is the durable choice.
	ThirdPartyQuotaCredentialAPIKey = "api_key"
	// ThirdPartyQuotaCredentialAccessToken is a short-lived bearer token.
	ThirdPartyQuotaCredentialAccessToken = "access_token"
	// ThirdPartyQuotaCredentialRefreshToken can mint fresh access tokens and is
	// renewed by the server before it expires.
	ThirdPartyQuotaCredentialRefreshToken = "refresh_token"
	// ThirdPartyQuotaCredentialSession is a browser login cookie. It expires on
	// the vendor's own schedule and cannot be renewed here.
	ThirdPartyQuotaCredentialSession = "session"
)

// ThirdPartyQuotaAccount stores the server-side credential used to read one
// external subscription. Credential, AccessToken and RefreshToken must never be
// serialized to panel clients.
//
// The backing table keeps its legacy ClinePass name so existing installations
// can be migrated in place without copying encrypted credentials.
type ThirdPartyQuotaAccount struct {
	ID               string `json:"id"`
	Provider         string `json:"provider"`
	Label            string `json:"label"`
	Credential       string `json:"-"`
	CredentialKind   string `json:"credentialKind"`
	AccessToken      string `json:"-"`
	RefreshToken     string `json:"-"`
	AccountUserID    string `json:"accountUserId,omitempty"`
	AccountEmail     string `json:"accountEmail,omitempty"`
	WorkspaceID      string `json:"workspaceId,omitempty"`
	ServerFunctionID string `json:"serverFunctionId,omitempty"`

	// AccessTokenExpiresAtMS is when AccessToken stops working. Zero means the
	// vendor did not report an expiry, so the token is used until it is refused.
	AccessTokenExpiresAtMS int64 `json:"accessTokenExpiresAtMs,omitempty"`

	// LifetimeTokens records the account's all-time token total so the panel does
	// not re-walk the vendor's usage history on every refresh. Partial marks a
	// total that is a lower bound because part of the history was unreadable.
	LifetimeTokens           int64 `json:"lifetimeTokens"`
	LifetimeTokensPartial    bool  `json:"lifetimeTokensPartial"`
	LifetimeTokensSyncedAtMS int64 `json:"lifetimeTokensSyncedAtMs"`

	CreatedAtMS int64 `json:"createdAtMs"`
	UpdatedAtMS int64 `json:"updatedAtMs"`
}
