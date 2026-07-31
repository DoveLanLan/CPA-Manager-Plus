package clinepass

import (
	"context"
	"database/sql"
	"errors"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

var ErrSecretProtectorRequired = errors.New("ClinePass credentials require an encrypted store")

type Repository interface {
	List(ctx context.Context) ([]model.ClinePassAccount, error)
	Get(ctx context.Context, id string) (model.ClinePassAccount, bool, error)
	Upsert(ctx context.Context, account model.ClinePassAccount) error
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

func (r *repository) List(ctx context.Context) ([]model.ClinePassAccount, error) {
	rows, err := r.db.QueryContext(ctx, `select id, label, session_id, created_at_ms, updated_at_ms
		from clinepass_accounts order by created_at_ms asc, id asc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.ClinePassAccount, 0)
	for rows.Next() {
		var item model.ClinePassAccount
		if err := rows.Scan(&item.ID, &item.Label, &item.SessionID, &item.CreatedAtMS, &item.UpdatedAtMS); err != nil {
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

func (r *repository) Get(ctx context.Context, id string) (model.ClinePassAccount, bool, error) {
	var item model.ClinePassAccount
	err := r.db.QueryRowContext(ctx, `select id, label, session_id, created_at_ms, updated_at_ms
		from clinepass_accounts where id = ?`, id).Scan(
		&item.ID,
		&item.Label,
		&item.SessionID,
		&item.CreatedAtMS,
		&item.UpdatedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ClinePassAccount{}, false, nil
	}
	if err != nil {
		return model.ClinePassAccount{}, false, err
	}
	item, err = r.unprotect(item)
	if err != nil {
		return model.ClinePassAccount{}, false, err
	}
	return item, true, nil
}

func (r *repository) Upsert(ctx context.Context, account model.ClinePassAccount) error {
	if r.protector == nil {
		return ErrSecretProtectorRequired
	}
	protected, err := r.protector.ProtectString(account.SessionID)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `insert into clinepass_accounts (
		id, label, session_id, created_at_ms, updated_at_ms
	) values (?, ?, ?, ?, ?)
	on conflict(id) do update set
		label = excluded.label,
		session_id = excluded.session_id,
		updated_at_ms = excluded.updated_at_ms`,
		account.ID,
		account.Label,
		protected,
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

func (r *repository) unprotect(account model.ClinePassAccount) (model.ClinePassAccount, error) {
	if r.protector == nil {
		return model.ClinePassAccount{}, ErrSecretProtectorRequired
	}
	value, err := r.protector.UnprotectString(account.SessionID)
	if err != nil {
		return model.ClinePassAccount{}, err
	}
	account.SessionID = value
	return account, nil
}
