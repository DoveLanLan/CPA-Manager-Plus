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

func (r *repository) List(ctx context.Context) ([]model.ThirdPartyQuotaAccount, error) {
	rows, err := r.db.QueryContext(ctx, `select id, provider, label, session_id, workspace_id,
		server_function_id, created_at_ms, updated_at_ms
		from clinepass_accounts order by created_at_ms asc, id asc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.ThirdPartyQuotaAccount, 0)
	for rows.Next() {
		var item model.ThirdPartyQuotaAccount
		if err := rows.Scan(
			&item.ID,
			&item.Provider,
			&item.Label,
			&item.Credential,
			&item.WorkspaceID,
			&item.ServerFunctionID,
			&item.CreatedAtMS,
			&item.UpdatedAtMS,
		); err != nil {
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
	var item model.ThirdPartyQuotaAccount
	err := r.db.QueryRowContext(ctx, `select id, provider, label, session_id, workspace_id,
		server_function_id, created_at_ms, updated_at_ms
		from clinepass_accounts where id = ?`, id).Scan(
		&item.ID,
		&item.Provider,
		&item.Label,
		&item.Credential,
		&item.WorkspaceID,
		&item.ServerFunctionID,
		&item.CreatedAtMS,
		&item.UpdatedAtMS,
	)
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
	if r.protector == nil {
		return ErrSecretProtectorRequired
	}
	protected, err := r.protector.ProtectString(account.Credential)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `insert into clinepass_accounts (
		id, provider, label, session_id, workspace_id, server_function_id, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(id) do update set
		provider = excluded.provider,
		label = excluded.label,
		session_id = excluded.session_id,
		workspace_id = excluded.workspace_id,
		server_function_id = excluded.server_function_id,
		updated_at_ms = excluded.updated_at_ms`,
		account.ID,
		account.Provider,
		account.Label,
		protected,
		account.WorkspaceID,
		account.ServerFunctionID,
		account.CreatedAtMS,
		account.UpdatedAtMS,
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

func (r *repository) unprotect(account model.ThirdPartyQuotaAccount) (model.ThirdPartyQuotaAccount, error) {
	if r.protector == nil {
		return model.ThirdPartyQuotaAccount{}, ErrSecretProtectorRequired
	}
	value, err := r.protector.UnprotectString(account.Credential)
	if err != nil {
		return model.ThirdPartyQuotaAccount{}, err
	}
	account.Credential = value
	return account, nil
}
