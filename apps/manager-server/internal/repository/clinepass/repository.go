package clinepass

import (
	"context"
	"database/sql"
	"errors"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

var ErrSecretProtectorRequired = errors.New("third-party quota credentials require an encrypted store")

type Repository interface {
	List(ctx context.Context) ([]model.ThirdPartyQuotaAccount, error)
	Get(ctx context.Context, id string) (model.ThirdPartyQuotaAccount, bool, error)
	Upsert(ctx context.Context, account model.ThirdPartyQuotaAccount) error
	Delete(ctx context.Context, id string) (bool, error)
}

type repository struct {
	db        *sql.DB
	protector *security.Protector
}

func New(db *sql.DB, protector ...*security.Protector) Repository {
	var p *security.Protector
	if len(protector) > 0 {
		p = protector[0]
	}
	return &repository{db: db, protector: p}
}

const accountColumns = `id, provider, label, session_id, credential_kind, access_token,
	refresh_token, access_token_expires_at_ms, account_user_id, account_email,
	workspace_id, server_function_id, lifetime_tokens, lifetime_tokens_partial,
	lifetime_tokens_synced_at_ms, created_at_ms, updated_at_ms`

func scanAccount(scanner interface{ Scan(...any) error }) (model.ThirdPartyQuotaAccount, error) {
	var item model.ThirdPartyQuotaAccount
	var lifetimePartial int
	err := scanner.Scan(
		&item.ID,
		&item.Provider,
		&item.Label,
		&item.Credential,
		&item.CredentialKind,
		&item.AccessToken,
		&item.RefreshToken,
		&item.AccessTokenExpiresAtMS,
		&item.AccountUserID,
		&item.AccountEmail,
		&item.WorkspaceID,
		&item.ServerFunctionID,
		&item.LifetimeTokens,
		&lifetimePartial,
		&item.LifetimeTokensSyncedAtMS,
		&item.CreatedAtMS,
		&item.UpdatedAtMS,
	)
	item.LifetimeTokensPartial = lifetimePartial != 0
	return item, err
}

func (r *repository) List(ctx context.Context) ([]model.ThirdPartyQuotaAccount, error) {
	rows, err := r.db.QueryContext(ctx, `select `+accountColumns+`
		from clinepass_accounts order by created_at_ms asc, id asc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.ThirdPartyQuotaAccount, 0)
	for rows.Next() {
		item, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		item, err = r.unprotect(item)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *repository) Get(ctx context.Context, id string) (model.ThirdPartyQuotaAccount, bool, error) {
	item, err := scanAccount(r.db.QueryRowContext(ctx, `select `+accountColumns+`
		from clinepass_accounts where id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.ThirdPartyQuotaAccount{}, false, nil
	}
	if err != nil {
		return model.ThirdPartyQuotaAccount{}, false, err
	}
	item, err = r.unprotect(item)
	if err != nil {
		return model.ThirdPartyQuotaAccount{}, false, err
	}
	return item, true, nil
}

func (r *repository) Upsert(ctx context.Context, account model.ThirdPartyQuotaAccount) error {
	protected, err := r.protect(account)
	if err != nil {
		return err
	}
	lifetimePartial := 0
	if protected.LifetimeTokensPartial {
		lifetimePartial = 1
	}
	_, err = r.db.ExecContext(ctx, `insert into clinepass_accounts (
		id, provider, label, session_id, credential_kind, access_token, refresh_token,
		access_token_expires_at_ms, account_user_id, account_email, workspace_id,
		server_function_id, lifetime_tokens, lifetime_tokens_partial,
		lifetime_tokens_synced_at_ms, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(id) do update set
		provider = excluded.provider,
		label = excluded.label,
		session_id = excluded.session_id,
		credential_kind = excluded.credential_kind,
		access_token = excluded.access_token,
		refresh_token = excluded.refresh_token,
		access_token_expires_at_ms = excluded.access_token_expires_at_ms,
		account_user_id = excluded.account_user_id,
		account_email = excluded.account_email,
		workspace_id = excluded.workspace_id,
		server_function_id = excluded.server_function_id,
		lifetime_tokens = excluded.lifetime_tokens,
		lifetime_tokens_partial = excluded.lifetime_tokens_partial,
		lifetime_tokens_synced_at_ms = excluded.lifetime_tokens_synced_at_ms,
		updated_at_ms = excluded.updated_at_ms`,
		protected.ID,
		protected.Provider,
		protected.Label,
		protected.Credential,
		protected.CredentialKind,
		protected.AccessToken,
		protected.RefreshToken,
		protected.AccessTokenExpiresAtMS,
		protected.AccountUserID,
		protected.AccountEmail,
		protected.WorkspaceID,
		protected.ServerFunctionID,
		protected.LifetimeTokens,
		lifetimePartial,
		protected.LifetimeTokensSyncedAtMS,
		protected.CreatedAtMS,
		protected.UpdatedAtMS,
	)
	return err
}

func (r *repository) Delete(ctx context.Context, id string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `delete from clinepass_accounts where id = ?`, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// protect encrypts every secret the account carries. Each column is encrypted
// independently because a protector is free to change format per value.
func (r *repository) protect(account model.ThirdPartyQuotaAccount) (model.ThirdPartyQuotaAccount, error) {
	if r.protector == nil {
		return model.ThirdPartyQuotaAccount{}, ErrSecretProtectorRequired
	}
	var err error
	if account.Credential, err = r.protector.ProtectString(account.Credential); err != nil {
		return model.ThirdPartyQuotaAccount{}, err
	}
	if account.AccessToken, err = r.protector.ProtectString(account.AccessToken); err != nil {
		return model.ThirdPartyQuotaAccount{}, err
	}
	if account.RefreshToken, err = r.protector.ProtectString(account.RefreshToken); err != nil {
		return model.ThirdPartyQuotaAccount{}, err
	}
	return account, nil
}

func (r *repository) unprotect(account model.ThirdPartyQuotaAccount) (model.ThirdPartyQuotaAccount, error) {
	if r.protector == nil {
		return model.ThirdPartyQuotaAccount{}, ErrSecretProtectorRequired
	}
	var err error
	if account.Credential, err = r.protector.UnprotectString(account.Credential); err != nil {
		return model.ThirdPartyQuotaAccount{}, err
	}
	if account.AccessToken, err = r.protector.UnprotectString(account.AccessToken); err != nil {
		return model.ThirdPartyQuotaAccount{}, err
	}
	if account.RefreshToken, err = r.protector.UnprotectString(account.RefreshToken); err != nil {
		return model.ThirdPartyQuotaAccount{}, err
	}
	return account, nil
}
