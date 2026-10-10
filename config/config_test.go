package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// requiredEnv is the minimum set of values config validation insists on.
var requiredEnv = map[string]string{
	"GOPDS_SECRET_KEY":       "env-secret-key",
	"GOPDS_POSTGRES_DBUSER":  "env-db-user",
	"GOPDS_POSTGRES_DBNAME":  "env-db-name",
	"GOPDS_SESSIONS_KEY":     "env-session-key",
	"GOPDS_SESSIONS_REFRESH": "env-refresh-key",
}

// isolate gives a test a clean viper instance, a scratch working directory and
// an environment with no GOPDS_* variables, so config discovery, the directories
// validation creates, and the precedence assertions all stay contained.
//
// Clearing the environment matters: the caller may legitimately have GOPDS_*
// set — `make test-integration` exports the database credentials — and those
// would override config-file values under test.
func isolate(t *testing.T) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	clearGopdsEnv(t)

	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("restoring working directory: %v", err)
		}
	})
}

// clearGopdsEnv removes every GOPDS_* variable for the duration of the test and
// restores the previous environment afterwards.
func clearGopdsEnv(t *testing.T) {
	t.Helper()

	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GOPDS_") {
			continue
		}
		// t.Setenv registers the restore; unset right after so the variable is
		// absent rather than empty.
		t.Setenv(key, value)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unsetting %s: %v", key, err)
		}
	}
}

// setEnv sets environment variables for the duration of the test.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for key, value := range env {
		t.Setenv(key, value)
	}
}

// writeConfigFile drops a config.yaml into the current working directory.
func writeConfigFile(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(".", "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing config.yaml: %v", err)
	}
}

// TestLoadReadsRequiredSecretsFromEnv pins that the application can be configured
// entirely through GOPDS_* environment variables, as .env.example documents.
// None of these keys have defaults, which is exactly the case viper.AutomaticEnv
// does not cover on its own.
func TestLoadReadsRequiredSecretsFromEnv(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	checks := map[string]struct{ got, want string }{
		"SecretKey":        {cfg.SecretKey, requiredEnv["GOPDS_SECRET_KEY"]},
		"Postgres.DBUser":  {cfg.Postgres.DBUser, requiredEnv["GOPDS_POSTGRES_DBUSER"]},
		"Postgres.DBName":  {cfg.Postgres.DBName, requiredEnv["GOPDS_POSTGRES_DBNAME"]},
		"Sessions.Key":     {cfg.Sessions.Key, requiredEnv["GOPDS_SESSIONS_KEY"]},
		"Sessions.Refresh": {cfg.Sessions.Refresh, requiredEnv["GOPDS_SESSIONS_REFRESH"]},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", field, c.got, c.want)
		}
	}
}

// TestLoadReadsOptionalSecretsFromEnv covers the remaining defaultless keys that
// carry credentials or deployment-specific URLs.
func TestLoadReadsOptionalSecretsFromEnv(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	setEnv(t, map[string]string{
		"GOPDS_POSTGRES_DBPASS":  "env-db-password",
		"GOPDS_REDIS_PASSWORD":   "env-redis-password",
		"GOPDS_APP_BOOK_CDN_KEY": "env-cdn-key",
		"GOPDS_PROJECT_DOMAIN":   "env.example.com",
		"GOPDS_PROJECT_URL":      "https://env.example.com",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	checks := map[string]struct{ got, want string }{
		"Postgres.DBPass": {cfg.Postgres.DBPass, "env-db-password"},
		"Redis.Password":  {cfg.Redis.Password, "env-redis-password"},
		"App.BookCDNKey":  {cfg.App.BookCDNKey, "env-cdn-key"},
		"Domain":          {cfg.Domain, "env.example.com"},
		"ProjectURL":      {cfg.ProjectURL, "https://env.example.com"},
	}
	for field, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", field, c.got, c.want)
		}
	}
}

// TestLoadEnvOverridesConfigFile pins the precedence order: an explicit
// environment variable beats a value present in config.yaml.
func TestLoadEnvOverridesConfigFile(t *testing.T) {
	isolate(t)
	writeConfigFile(t, `secret_key: "file-secret-key"
postgres:
  dbuser: file-db-user
  dbname: file-db-name
sessions:
  key: file-session-key
  refresh: file-refresh-key
`)
	setEnv(t, map[string]string{"GOPDS_SECRET_KEY": "env-secret-key"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.SecretKey != "env-secret-key" {
		t.Errorf("SecretKey = %q, want the environment value %q", cfg.SecretKey, "env-secret-key")
	}
	if cfg.Postgres.DBUser != "file-db-user" {
		t.Errorf("Postgres.DBUser = %q, want the file value %q", cfg.Postgres.DBUser, "file-db-user")
	}
}

// TestLoadStillReadsConfigFile is the regression guard for the existing
// deployment style, where everything comes from config.yaml.
func TestLoadStillReadsConfigFile(t *testing.T) {
	isolate(t)
	writeConfigFile(t, `secret_key: "file-secret-key"
postgres:
  dbuser: file-db-user
  dbname: file-db-name
sessions:
  key: file-session-key
  refresh: file-refresh-key
server:
  port: 9090
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.SecretKey != "file-secret-key" {
		t.Errorf("SecretKey = %q, want %q", cfg.SecretKey, "file-secret-key")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 9090)
	}
}

// TestLoadKeepsDefaultsWhenUnset guards that binding environment variables does
// not clobber the configured defaults when nothing is set.
func TestLoadKeepsDefaultsWhenUnset(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.Server.Port != 8085 {
		t.Errorf("Server.Port = %d, want the default %d", cfg.Server.Port, 8085)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q, want the default %q", cfg.Server.Host, "127.0.0.1")
	}
	if cfg.Postgres.MaxConns != 10 {
		t.Errorf("Postgres.MaxConns = %d, want the default %d", cfg.Postgres.MaxConns, 10)
	}
}

// The donate methods are the one part of the configuration a reader sees, and
// the one an operator is most likely to get wrong: a fork that forgets to
// change them would otherwise advertise the original author's wallet.
func TestLoadReadsDonateMethods(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, `
donate:
  - id: tinkoff
    label: Tinkoff
    kind: card
    value: "5536913994186852"
    link: https://tbank.ru/cf/abc
  - id: bitcoin
    label: Bitcoin
    kind: address
    value: bc1qexample
    qr: true
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Donate) != 2 {
		t.Fatalf("expected 2 donate methods, got %d", len(cfg.Donate))
	}

	first := cfg.Donate[0]
	if first.ID != "tinkoff" || first.Kind != "card" || first.Value != "5536913994186852" {
		t.Errorf("first method read wrongly: %+v", first)
	}
	if first.Link != "https://tbank.ru/cf/abc" {
		t.Errorf("link not read: %q", first.Link)
	}
	if first.QR {
		t.Error("a card number is nothing to scan; qr should stay off unless asked for")
	}
	if !cfg.Donate[1].QR {
		t.Error("qr: true was not read")
	}
}

// Nothing configured is the ordinary case for anyone running their own copy,
// and it has to mean "offer nothing" rather than "fall back to someone else's".
func TestLoadLeavesDonateEmptyWhenUnset(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, "server:\n  port: 8085\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Donate) != 0 {
		t.Errorf("expected no donate methods, got %+v", cfg.Donate)
	}
}

// Phase 15: the embedded author metadata workers. Every limit has a default,
// so the section is usually absent; a value that would make a worker claim
// nothing, never time out or retry forever is refused at load, by key.

func TestLoadAuthorMetadataDefaults(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	want := AuthorMetadataConfig{
		Enabled:          false,
		MetadataMaxBytes: 4 << 20,
		PollInterval:     5 * time.Second,
		Extraction: AuthorMetadataStageConfig{
			Concurrency: 1, ClaimSize: 50, Lease: 2 * time.Minute, MaxAttempts: 5,
		},
		LocalNormalization: AuthorMetadataStageConfig{
			Concurrency: 1, ClaimSize: 100, Lease: time.Minute, MaxAttempts: 5,
		},
	}
	if !reflect.DeepEqual(cfg.AuthorMetadata, want) {
		t.Errorf("AuthorMetadata = %+v, want %+v", cfg.AuthorMetadata, want)
	}
	if AuthorMetadataMaxBytes != 4<<20 {
		t.Errorf("AuthorMetadataMaxBytes = %d, want 4 MiB", AuthorMetadataMaxBytes)
	}
}

func TestLoadAuthorMetadataFromFileAndEnv(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, `author_metadata:
  enabled: true
  poll_interval: 750ms
  local_normalization:
    claim_size: 7
    lease: 90s
`)
	setEnv(t, map[string]string{
		"GOPDS_AUTHOR_METADATA_METADATA_MAX_BYTES":              "1048576",
		"GOPDS_AUTHOR_METADATA_EXTRACTION_MAX_ATTEMPTS":         "3",
		"GOPDS_AUTHOR_METADATA_LOCAL_NORMALIZATION_CONCURRENCY": "2",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	got := cfg.AuthorMetadata
	if !got.Enabled || got.PollInterval != 750*time.Millisecond || got.MetadataMaxBytes != 1<<20 {
		t.Errorf("enabled/poll/max bytes = %v/%v/%d", got.Enabled, got.PollInterval, got.MetadataMaxBytes)
	}
	if got.LocalNormalization.ClaimSize != 7 || got.LocalNormalization.Lease != 90*time.Second ||
		got.LocalNormalization.Concurrency != 2 {
		t.Errorf("local normalization = %+v", got.LocalNormalization)
	}
	if got.Extraction.MaxAttempts != 3 || got.Extraction.ClaimSize != 50 {
		t.Errorf("extraction = %+v", got.Extraction)
	}
}

func TestLoadAuthorMetadataRejectsNonPositiveValues(t *testing.T) {
	keys := []struct {
		env, key string
		duration bool
	}{
		{"GOPDS_AUTHOR_METADATA_METADATA_MAX_BYTES", "author_metadata.metadata_max_bytes", false},
		{"GOPDS_AUTHOR_METADATA_POLL_INTERVAL", "author_metadata.poll_interval", true},
		{"GOPDS_AUTHOR_METADATA_EXTRACTION_CONCURRENCY", "author_metadata.extraction.concurrency", false},
		{"GOPDS_AUTHOR_METADATA_EXTRACTION_CLAIM_SIZE", "author_metadata.extraction.claim_size", false},
		{"GOPDS_AUTHOR_METADATA_EXTRACTION_LEASE", "author_metadata.extraction.lease", true},
		{"GOPDS_AUTHOR_METADATA_EXTRACTION_MAX_ATTEMPTS", "author_metadata.extraction.max_attempts", false},
		{"GOPDS_AUTHOR_METADATA_LOCAL_NORMALIZATION_CONCURRENCY", "author_metadata.local_normalization.concurrency", false},
		{"GOPDS_AUTHOR_METADATA_LOCAL_NORMALIZATION_CLAIM_SIZE", "author_metadata.local_normalization.claim_size", false},
		{"GOPDS_AUTHOR_METADATA_LOCAL_NORMALIZATION_LEASE", "author_metadata.local_normalization.lease", true},
		{"GOPDS_AUTHOR_METADATA_LOCAL_NORMALIZATION_MAX_ATTEMPTS", "author_metadata.local_normalization.max_attempts", false},
	}
	for _, k := range keys {
		values := []string{"0", "-1"}
		if k.duration {
			values = []string{"0s", "-1s"}
		}
		for _, value := range values {
			t.Run(k.key+"="+value, func(t *testing.T) {
				isolate(t)
				setEnv(t, requiredEnv)
				// Disabled workers still get their limits checked: a bad value
				// must not wait for the day someone turns them on.
				setEnv(t, map[string]string{k.env: value})

				_, err := Load()
				if err == nil {
					t.Fatalf("Load() = nil, want an error naming %s", k.key)
				}
				if !strings.Contains(err.Error(), k.key) {
					t.Errorf("Load() = %v, want the key %s named", err, k.key)
				}
			})
		}
	}
}

// A server start applies pending migrations unless the operator turns it off:
// on by default, off through the file or GOPDS_DATABASE_AUTO_MIGRATE.
func TestLoadDatabaseAutoMigrate(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  map[string]string
		want bool
	}{
		{name: "default", want: true},
		{name: "file off", file: "database:\n  auto_migrate: false\n", want: false},
		{name: "env off", env: map[string]string{"GOPDS_DATABASE_AUTO_MIGRATE": "false"}, want: false},
		{name: "env on over file off", file: "database:\n  auto_migrate: false\n",
			env: map[string]string{"GOPDS_DATABASE_AUTO_MIGRATE": "true"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			setEnv(t, requiredEnv)
			setEnv(t, tc.env)
			if tc.file != "" {
				writeConfigFile(t, tc.file)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() = %v, want nil", err)
			}
			if cfg.Database.AutoMigrate != tc.want {
				t.Errorf("Database.AutoMigrate = %v, want %v", cfg.Database.AutoMigrate, tc.want)
			}
		})
	}
}

// Where the book list's author line comes from: the legacy catalog until
// the operator switches it to the author layer, through the file or
// GOPDS_AUTHORS_DISPLAY_SOURCE; any other value is refused at load, by key.
func TestLoadAuthorsDisplaySource(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  map[string]string
		want string
	}{
		{name: "default", want: AuthorsDisplayLegacy},
		{name: "file", file: "authors:\n  display_source: layer\n", want: AuthorsDisplayLayer},
		{name: "env over file", file: "authors:\n  display_source: layer\n",
			env: map[string]string{"GOPDS_AUTHORS_DISPLAY_SOURCE": "legacy"}, want: AuthorsDisplayLegacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			setEnv(t, requiredEnv)
			setEnv(t, tc.env)
			if tc.file != "" {
				writeConfigFile(t, tc.file)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() = %v, want nil", err)
			}
			if cfg.Authors.DisplaySource != tc.want {
				t.Errorf("Authors.DisplaySource = %q, want %q", cfg.Authors.DisplaySource, tc.want)
			}
			if cfg.Authors.DisplayFromLayer() != (tc.want == AuthorsDisplayLayer) {
				t.Errorf("DisplayFromLayer() = %v for %q", cfg.Authors.DisplayFromLayer(), tc.want)
			}
		})
	}

	for _, value := range []string{"Layer", "both", " "} {
		t.Run("refuses "+value, func(t *testing.T) {
			isolate(t)
			setEnv(t, requiredEnv)
			setEnv(t, map[string]string{"GOPDS_AUTHORS_DISPLAY_SOURCE": value})

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "authors.display_source") {
				t.Fatalf("Load() = %v, want an error naming authors.display_source", err)
			}
		})
	}
}
