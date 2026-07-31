package clinepass

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func TestRepositoryEncryptsCredentialAndRestoresProviderFields(t *testing.T) {
	db := openTestDB(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("new protector: %v", err)
	}
	repo := New(db, protector)
	account := model.ThirdPartyQuotaAccount{
		ID:               "account-1",
		Provider:         "opencode_go",
		Label:            "Work",
		Credential:       "session-secret-value",
		WorkspaceID:      "wrk_account",
		ServerFunctionID: strings.Repeat("a", 64),
		CreatedAtMS:      10,
		UpdatedAtMS:      20,
	}
	if err := repo.Upsert(context.Background(), account); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var raw string
	if err := db.QueryRow(`select session_id from clinepass_accounts where id = ?`, account.ID).Scan(&raw); err != nil {
		t.Fatalf("read raw session: %v", err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, account.Credential) {
		t.Fatalf("session was not encrypted at rest: %q", raw)
	}

	loaded, ok, err := repo.Get(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok || loaded != account {
		t.Fatalf("loaded account = %#v, ok=%t", loaded, ok)
	}
}

func TestRepositoryRefusesPlaintextCredentialStorage(t *testing.T) {
	db := openTestDB(t)
	repo := New(db)
	err := repo.Upsert(context.Background(), model.ThirdPartyQuotaAccount{
		ID: "account-1", Provider: "clinepass", Label: "Work", Credential: "secret",
	})
	if !errors.Is(err, ErrSecretProtectorRequired) {
		t.Fatalf("error = %v, want ErrSecretProtectorRequired", err)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
