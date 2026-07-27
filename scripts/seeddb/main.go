// Command seeddb creates synthetic users in a local development database and
// reattaches imported catalog rows to them.
//
// The development dataset produced by scripts/dump-prod-catalog.sh deliberately
// omits auth_user, because production holds real email addresses, password
// hashes and live Telegram bot tokens. That leaves collections pointing at user
// ids that do not exist locally, so this command creates known-credential users
// and repoints those rows.
//
// Passwords are hashed with the application's own utils.CreatePasswordHash, so
// the seeded accounts log in through the normal flow.
//
// It refuses to run against anything that looks like production.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gopds-api/utils"

	"github.com/go-pg/pg/v10"
)

type seedUser struct {
	login       string
	password    string
	email       string
	isSuperUser bool
}

// Credentials are intentionally trivial: this only ever runs against a local
// database, and the point is that they are easy to type while developing.
const (
	adminLogin      = "admin"
	adminPassword   = "admin"
	regularPassword = "test123"
)

var seedUsers = []seedUser{
	{login: adminLogin, password: adminPassword, email: "admin@localhost", isSuperUser: true},
	{login: "user1", password: regularPassword, email: "user1@localhost"},
	{login: "user2", password: regularPassword, email: "user2@localhost"},
}

func main() {
	addr := flag.String("addr", "127.0.0.1:5432", "PostgreSQL address")
	user := flag.String("user", "gopds", "PostgreSQL user")
	password := flag.String("password", "gopds_password", "PostgreSQL password")
	database := flag.String("database", "gopds", "PostgreSQL database")
	flag.Parse()

	if err := run(*addr, *user, *password, *database); err != nil {
		fmt.Fprintf(os.Stderr, "seeddb: %v\n", err)
		os.Exit(1)
	}
}

func run(addr, user, password, database string) error {
	if err := refuseRemoteHost(addr); err != nil {
		return err
	}

	db := pg.Connect(&pg.Options{
		Addr:     addr,
		User:     user,
		Password: password,
		Database: database,
	})
	defer db.Close()

	if _, err := db.Exec("SELECT 1"); err != nil {
		return fmt.Errorf("connecting to %s/%s: %w", addr, database, err)
	}

	// A production dump would carry real accounts; seeding on top of them is
	// almost certainly a mistake, so stop instead of guessing.
	var existing int
	if _, err := db.QueryOne(pg.Scan(&existing), "SELECT count(*) FROM auth_user"); err != nil {
		return fmt.Errorf("counting existing users: %w", err)
	}
	if existing > len(seedUsers) {
		return fmt.Errorf("auth_user already holds %d rows, which does not look like a seeded database; refusing to touch it", existing)
	}

	adminID, err := seed(db)
	if err != nil {
		return err
	}

	return reattachCatalog(db, adminID)
}

// refuseRemoteHost is a guard against pointing this at the production cluster,
// for example through a forgotten port-forward.
func refuseRemoteHost(addr string) error {
	host, _, found := strings.Cut(addr, ":")
	if !found {
		host = addr
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "postgres":
		return nil
	default:
		return fmt.Errorf("refusing to seed %q: only a local database may be seeded", host)
	}
}

// seed inserts the synthetic users and returns the id of the admin account.
func seed(db *pg.DB) (int64, error) {
	var adminID int64

	for _, u := range seedUsers {
		var id int64
		_, err := db.QueryOne(pg.Scan(&id), `
			INSERT INTO auth_user (username, password, email, is_superuser,
			                       first_name, last_name, books_lang,
			                       date_joined, last_login, active,
			                       bot_token, webhook_uuid)
			VALUES (?, ?, ?, ?, '', '', 'ru', ?, ?, true, '', '')
			ON CONFLICT (username) DO UPDATE SET password = EXCLUDED.password,
			                                     is_superuser = EXCLUDED.is_superuser,
			                                     active = true
			RETURNING id`,
			u.login, utils.CreatePasswordHash(u.password), u.email, u.isSuperUser,
			time.Now(), time.Now())
		if err != nil {
			return 0, fmt.Errorf("seeding user %q: %w", u.login, err)
		}

		fmt.Printf("user %-6s id=%-4d password=%-8s superuser=%v\n", u.login, id, u.password, u.isSuperUser)
		if u.isSuperUser {
			adminID = id
		}
	}

	if adminID == 0 {
		return 0, fmt.Errorf("no superuser among seed users")
	}
	return adminID, nil
}

// reattachCatalog repoints imported rows whose owner did not come across with
// the catalog dump.
func reattachCatalog(db *pg.DB, adminID int64) error {
	statements := []struct {
		what   string
		sql    string
		params []interface{}
	}{
		{
			what: "book_collections.user_id",
			sql: `UPDATE book_collections SET user_id = ?
			      WHERE user_id IS NOT NULL
			        AND user_id NOT IN (SELECT id FROM auth_user)`,
			params: []interface{}{adminID},
		},
		{
			what: "book_match_decisions.decided_by_user_id",
			sql: `UPDATE book_match_decisions SET decided_by_user_id = NULL
			      WHERE decided_by_user_id IS NOT NULL
			        AND decided_by_user_id NOT IN (SELECT id FROM auth_user)`,
		},
	}

	for _, s := range statements {
		res, err := db.Exec(s.sql, s.params...)
		if err != nil {
			return fmt.Errorf("reattaching %s: %w", s.what, err)
		}
		fmt.Printf("reattached %-40s %d rows\n", s.what, res.RowsAffected())
	}

	return nil
}
