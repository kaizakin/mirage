// Package store owns the gateway's Postgres database: connecting, migrating, and
// (as the gateway grows) the repositories over its tables.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Open connects to the Postgres database at dsn and checks that it answers.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return db, nil
}

// Migrate applies every pending migration. It is safe to call on every startup and from
// several replicas at once: a Postgres advisory lock lets one run them while the others wait.
func Migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := fs.Sub(embedded, "migrations")
	if err != nil {
		return err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("store: load migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	for _, r := range results {
		slog.Info("applied migration", "version", r.Source.Version, "file", r.Source.Path, "took", r.Duration)
	}
	return nil
}

// OpenWhenReady connects to Postgres, retrying with backoff until ctx is done. On a fresh
// install Postgres is still initializing, and a new pod's address takes a moment to be
// admitted by Postgres's NetworkPolicy; failing fast would only crash-loop the pod.
func OpenWhenReady(ctx context.Context, dsn string) (*sql.DB, error) {
	wait := time.Second
	for {
		db, err := Open(ctx, dsn)
		if err == nil {
			return db, nil
		}
		slog.Warn("waiting for database", "err", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database never became reachable: %w", err)
		case <-time.After(wait):
		}
		wait = min(wait*2, 10*time.Second)
	}
}
