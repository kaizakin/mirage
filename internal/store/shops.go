package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kaizakin/mirage/internal/secretbox"
)

// ShopStatus is where a provisioned store is in its life:
//
//	provisioning ──ready──▶ ready ──delete──▶ deleting ──gone──▶ (row removed)
//	     │                                       ▲
//	     └──timeout or error──▶ failed ──delete──┘
//	                              └──retry──▶ provisioning
//
// Each transition is an UPDATE guarded by the status it leaves, so the API and the
// provisioner never overwrite each other's decision.
type ShopStatus string

// Shop statuses.
const (
	ShopProvisioning ShopStatus = "provisioning"
	ShopReady        ShopStatus = "ready"
	ShopFailed       ShopStatus = "failed"
	ShopDeleting     ShopStatus = "deleting"
)

// ErrShopLimit is returned when an owner already has the most stores allowed.
var ErrShopLimit = errors.New("store limit reached")

// Shop is a provisioned WooCommerce store. It is called Shop because this package, store,
// is the database layer.
type Shop struct {
	ID                int64
	Slug              string
	Name              string
	OwnerID           int64 // 0 once the owner is deleted
	OwnerEmail        string
	Host              string
	Status            ShopStatus
	Reason            string
	SecretsEnc        []byte
	ProvisioningSince time.Time
	CreatedAt         time.Time
	ReadyAt           *time.Time
}

// ShopPrefix names everything that belongs to a store: its namespace and its MCP server, so
// tool names read store-<slug>__<tool>.
const ShopPrefix = "store-"

// Namespace is the Kubernetes namespace that holds the store.
func (s Shop) Namespace() string { return ShopPrefix + s.Slug }

// ServerSlug is the slug of the store's MCP server in the gateway.
func (s Shop) ServerSlug() string { return ShopPrefix + s.Slug }

const shopColumns = `s.id, s.slug, s.name, coalesce(s.owner_id, 0), coalesce(u.email, ''), s.host, s.status,
	coalesce(s.reason, ''), s.secrets_enc, s.provisioning_since, s.created_at, s.ready_at`

func scanShop(row interface{ Scan(...any) error }) (Shop, error) {
	var s Shop
	err := row.Scan(&s.ID, &s.Slug, &s.Name, &s.OwnerID, &s.OwnerEmail, &s.Host, &s.Status,
		&s.Reason, &s.SecretsEnc, &s.ProvisioningSince, &s.CreatedAt, &s.ReadyAt)
	return s, err
}

// NewShop is a request to create a store.
type NewShop struct {
	Slug, Name, Host string
	OwnerID          int64
	MaxPerOwner      int
}

// CreateShop creates a store, or returns the caller's existing store with the same slug, so
// a repeated request is safe: a provisioning or ready store comes back unchanged, and a failed
// one starts provisioning again. created reports whether a new row was inserted. A slug owned
// by someone else, or one being deleted, is ErrConflict; an owner at the limit gets
// ErrShopLimit.
func (s *Store) CreateShop(ctx context.Context, n NewShop) (shop Shop, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Shop{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	// One owner's creates run one at a time, so two at once cannot both pass the limit.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('shops'), $1::int)`, n.OwnerID); err != nil {
		return Shop{}, false, err
	}
	existing, err := scanShop(tx.QueryRowContext(ctx, `
		SELECT `+shopColumns+` FROM shops s LEFT JOIN users u ON u.id = s.owner_id
		WHERE s.slug = $1 FOR UPDATE OF s`, n.Slug))
	switch {
	case err == nil:
		return retryShop(ctx, tx, existing, n.OwnerID)
	case !errors.Is(err, sql.ErrNoRows):
		return Shop{}, false, err
	}

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM shops WHERE owner_id = $1 AND status <> 'deleting'`, n.OwnerID).Scan(&count); err != nil {
		return Shop{}, false, err
	}
	if count >= n.MaxPerOwner {
		return Shop{}, false, fmt.Errorf("%w: you already have %d stores", ErrShopLimit, count)
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO shops (slug, name, owner_id, host) VALUES ($1, $2, $3, $4) RETURNING id`,
		n.Slug, n.Name, n.OwnerID, n.Host).Scan(&id); err != nil {
		return Shop{}, false, classify(err)
	}
	shop, err = scanShop(tx.QueryRowContext(ctx, `SELECT `+shopColumns+` FROM shops s LEFT JOIN users u ON u.id = s.owner_id WHERE s.id = $1`, id))
	if err != nil {
		return Shop{}, false, err
	}
	return shop, true, tx.Commit()
}

func retryShop(ctx context.Context, tx *sql.Tx, existing Shop, ownerID int64) (Shop, bool, error) {
	switch {
	case existing.OwnerID != ownerID:
		return Shop{}, false, fmt.Errorf("%w: another store already uses %s", ErrConflict, existing.Slug)
	case existing.Status == ShopDeleting:
		return Shop{}, false, fmt.Errorf("%w: %s is still being deleted", ErrConflict, existing.Slug)
	case existing.Status == ShopFailed:
		if _, err := tx.ExecContext(ctx, `
			UPDATE shops SET status = 'provisioning', reason = NULL, provisioning_since = now()
			WHERE id = $1 AND status = 'failed'`, existing.ID); err != nil {
			return Shop{}, false, err
		}
		existing.Status, existing.Reason = ShopProvisioning, ""
	}
	return existing, false, tx.Commit()
}

// Shops lists stores, newest first: one owner's, or everyone's when ownerID is 0.
func (s *Store) Shops(ctx context.Context, ownerID int64) ([]Shop, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+shopColumns+` FROM shops s LEFT JOIN users u ON u.id = s.owner_id
		WHERE ($1 = 0 OR s.owner_id = $1) ORDER BY s.id DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Shop
	for rows.Next() {
		shop, err := scanShop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, shop)
	}
	return out, rows.Err()
}

// ShopBySlug returns one store, or ErrNotFound.
func (s *Store) ShopBySlug(ctx context.Context, slug string) (Shop, error) {
	shop, err := scanShop(s.db.QueryRowContext(ctx, `
		SELECT `+shopColumns+` FROM shops s LEFT JOIN users u ON u.id = s.owner_id WHERE s.slug = $1`, slug))
	return shop, notFound(err)
}

// MarkShopDeleting asks for a store to be torn down. Asking again is not an error.
func (s *Store) MarkShopDeleting(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE shops SET status = 'deleting' WHERE id = $1`, id)
	return affected(res, err)
}

// SetShopSecrets stores a store's sealed secrets unless it already has some, and returns
// whichever are stored, so two writers racing on a new store agree on one set.
func (s *Store) SetShopSecrets(ctx context.Context, id int64, sealed []byte) ([]byte, error) {
	var stored []byte
	err := s.db.QueryRowContext(ctx, `
		UPDATE shops SET secrets_enc = coalesce(secrets_enc, $2) WHERE id = $1 RETURNING secrets_enc`, id, sealed).Scan(&stored)
	return stored, notFound(err)
}

// ShopServer is the MCP server a store registers in the gateway.
type ShopServer struct {
	Slug       string
	URL        string
	Credential string // a bearer token
}

// RegisterShopServer registers the store's server unless it already is, so a ready store
// whose registration went missing gets it back. An existing registration is left exactly as
// it is: rewriting it would reseal the credential, and the gateway reconnects to any server
// whose stored configuration changes. A server with the same slug that the store does not
// own is ErrConflict.
func (s *Store) RegisterShopServer(ctx context.Context, box *secretbox.Box, shopID int64, srv ShopServer) error {
	return registerShopServer(ctx, s.db, box, shopID, srv)
}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func registerShopServer(ctx context.Context, db querier, box *secretbox.Box, shopID int64, srv ShopServer) error {
	credential, err := seal(box, srv.Slug, srv.Credential)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO servers (slug, url, auth_type, credential_enc, shop_id, internal) VALUES ($1, $2, 'bearer', $3, $4, true)
		ON CONFLICT (slug) DO NOTHING`, srv.Slug, srv.URL, credential, shopID)
	if err != nil {
		return classify(err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var owner sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT shop_id FROM servers WHERE slug = $1`, srv.Slug).Scan(&owner); err != nil {
		return err
	}
	if owner.Int64 != shopID {
		return fmt.Errorf("%w: server %s exists and does not belong to this store", ErrConflict, srv.Slug)
	}
	return nil
}

// MarkShopReady registers the store's server and marks it ready, together. It returns
// ErrNotFound if the store stopped provisioning meanwhile, such as by being deleted.
func (s *Store) MarkShopReady(ctx context.Context, box *secretbox.Box, shopID int64, srv ShopServer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		UPDATE shops SET status = 'ready', reason = NULL, ready_at = now()
		WHERE id = $1 AND status = 'provisioning'`, shopID)
	if err := affected(res, err); err != nil {
		return err
	}
	if err := registerShopServer(ctx, tx, box, shopID, srv); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkShopFailed records why provisioning failed. It returns ErrNotFound if the store was
// no longer provisioning.
func (s *Store) MarkShopFailed(ctx context.Context, shopID int64, reason string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE shops SET status = 'failed', reason = $2 WHERE id = $1 AND status = 'provisioning'`, shopID, reason)
	return affected(res, err)
}

// UnregisterShopServer removes the store's MCP server, so its tools leave the gateway
// before its namespace goes.
func (s *Store) UnregisterShopServer(ctx context.Context, shopID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE shop_id = $1`, shopID)
	return err
}

// DeleteShop removes a store's row once its resources are gone. Only a store being deleted
// can be removed.
func (s *Store) DeleteShop(ctx context.Context, shopID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM shops WHERE id = $1 AND status = 'deleting'`, shopID)
	return affected(res, err)
}
