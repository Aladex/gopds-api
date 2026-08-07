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
	"strings"

	"github.com/go-pg/pg/v10"
)

// The environment variables every integration suite reads.
const (
	envHost = "GOPDS_POSTGRES_DBHOST"
	envUser = "GOPDS_POSTGRES_DBUSER"
	// #nosec G101 -- the name of an environment variable, not a credential
	envPassword = "GOPDS_POSTGRES_DBPASS"
	envName     = "GOPDS_POSTGRES_DBNAME"
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
//
// Any of the three required variables counts as intent. A partially named
// database used to read as "not configured", which meant a CI job or a command
// with one variable misspelled skipped its integration tests and reported
// success — the same fail-open this package exists to remove, one level up. A
// typo should be loud; a developer who set nothing is unaffected either way.
func Configured() (Config, bool) {
	cfg := Config{
		Host:     os.Getenv(envHost),
		User:     os.Getenv(envUser),
		Password: os.Getenv(envPassword),
		Name:     os.Getenv(envName),
	}
	if cfg.Host == "" && cfg.User == "" && cfg.Name == "" {
		return Config{}, false
	}
	return cfg, true
}

// Incomplete names the required variables the environment left empty, so a
// partial configuration fails with the reason rather than with a refused
// connection.
func (c Config) Incomplete() []string {
	var missing []string
	for _, v := range []struct {
		name  string
		value string
	}{
		{envHost, c.Host},
		{envUser, c.User},
		{envName, c.Name},
	} {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	return missing
}

// SkipReason is what a suite says when no database was asked for.
const SkipReason = "no database configured: set " + envHost + "/DBUSER/DBNAME " +
	"(see `make db-reset`), or run with -short to skip integration tests"

// Connect opens the configured connection and verifies it answers. onConnect
// is passed through to pg.Options so callers can apply their own session
// settings; it may be nil.
func Connect(cfg Config, onConnect func(context.Context, *pg.Conn) error) (*pg.DB, error) {
	if missing := cfg.Incomplete(); len(missing) > 0 {
		return nil, fmt.Errorf("database is half configured, missing %s", strings.Join(missing, ", "))
	}
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
