package model

// ClinePassAccount stores the server-side credential used to read one
// ClinePass subscription. SessionID must never be serialized to panel clients.
type ClinePassAccount struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	SessionID   string `json:"-"`
	CreatedAtMS int64  `json:"createdAtMs"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}
