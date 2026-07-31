package model

// ThirdPartyQuotaAccount stores the server-side credential used to read one
// external subscription. Credential must never be serialized to panel clients.
//
// The backing table keeps its legacy ClinePass name so existing installations
// can be migrated in place without copying encrypted credentials.
type ThirdPartyQuotaAccount struct {
	ID               string `json:"id"`
	Provider         string `json:"provider"`
	Label            string `json:"label"`
	Credential       string `json:"-"`
	WorkspaceID      string `json:"workspaceId,omitempty"`
	ServerFunctionID string `json:"serverFunctionId,omitempty"`
	CreatedAtMS      int64  `json:"createdAtMs"`
	UpdatedAtMS      int64  `json:"updatedAtMs"`
}
