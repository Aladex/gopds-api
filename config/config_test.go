package config

import (
	"os"
	"path/filepath"
	"testing"

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

// isolate gives a test a clean viper instance and a scratch working directory,
// so config discovery and the directories validation creates stay contained.
func isolate(t *testing.T) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)

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
