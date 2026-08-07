// Package testdb decides one thing for every integration suite in this
// repository: whether the run asked for a database.
//
// The distinction matters because the two cases used to look identical. A
// suite that skipped when no database was configured also skipped when one was
// configured and unreachable, so `make test-integration` could finish green
// having verified nothing — no ranking fixture, no migration, no scope. A gate
// that reports success for missing infrastructure is worse than no gate, since
// it is the one people trust.
//
// Not configured is a developer running the unit tests: skip. Configured and
// unreachable is a run that asked for integration coverage and did not get it:
// fail.
package testdb

import (
	"context"
	"fmt"
	"os"

	"github.com/go-pg/pg/v10"
)

// Config is the connection the environment asks for.
type Config struct {
	Host     string
	User     string
	Password string
	Name     string
}

// Address renders the target for messages.
func (c Config) Address() string { return c.Host + "/" + c.Name }

// Configured reports whether the environment names a database, and returns it.
// A partially named one — host without user, say — counts as not configured:
// it cannot be connected to either way, and treating it as a request would turn
// a typo in one variable into a failing suite everywhere.
func Configured() (Config, bool) {
	cfg := Config{
		Host:     os.Getenv("GOPDS_POSTGRES_DBHOST"),
		User:     os.Getenv("GOPDS_POSTGRES_DBUSER"),
		Password: os.Getenv("GOPDS_POSTGRES_DBPASS"),
		Name:     os.Getenv("GOPDS_POSTGRES_DBNAME"),
	}
	if cfg.Host == "" || cfg.User == "" || cfg.Name == "" {
		return Config{}, false
	}
	return cfg, true
}

// SkipReason is what a suite says when no database was asked for.
const SkipReason = "no database configured: set GOPDS_POSTGRES_DBHOST/DBUSER/DBNAME " +
	"(see `make db-reset`), or run with -short to skip integration tests"

// Connect opens the configured connection and verifies it answers. onConnect
// is passed through to pg.Options so callers can apply their own session
// settings; it may be nil.
func Connect(cfg Config, onConnect func(context.Context, *pg.Conn) error) (*pg.DB, error) {
	db := pg.Connect(&pg.Options{
		Addr:      cfg.Host,
		User:      cfg.User,
		Password:  cfg.Password,
		Database:  cfg.Name,
		OnConnect: onConnect,
	})
	if _, err := db.Exec("SELECT 1"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to %s: %w", cfg.Address(), err)
	}
	return db, nil
}
