package config

import (
	"testing"
	"time"
)

// The preview section used to exist only as viper keys with no struct fields,
// so nothing in production could read it. These tests pin that every value
// an operator sets actually lands in the loaded Config — and that the
// documented fallback (no preview.redis → the main Redis, never a hardcoded
// localhost) is what the address resolution does.

// TestLoadReadsPreviewSection pins that non-standard values from the config
// file reach every field of the Preview section.
func TestLoadReadsPreviewSection(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, `
preview:
  redis:
    host: preview-redis
    port: 6391
    password: preview-secret
    db: 7
  cache_ttl: 3h
  build_timeout: 45s
  max_concurrent_builds: 9
  max_fb2_bytes: 1234567
  max_binaries: 42
  max_binaries_bytes: 7654321
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.Preview.CacheTTL != 3*time.Hour {
		t.Errorf("Preview.CacheTTL = %v, want 3h", cfg.Preview.CacheTTL)
	}
	if cfg.Preview.BuildTimeout != 45*time.Second {
		t.Errorf("Preview.BuildTimeout = %v, want 45s", cfg.Preview.BuildTimeout)
	}
	if cfg.Preview.MaxConcurrentBuilds != 9 {
		t.Errorf("Preview.MaxConcurrentBuilds = %d, want 9", cfg.Preview.MaxConcurrentBuilds)
	}
	if cfg.Preview.MaxFB2Bytes != 1234567 {
		t.Errorf("Preview.MaxFB2Bytes = %d, want 1234567", cfg.Preview.MaxFB2Bytes)
	}
	if cfg.Preview.MaxBinaries != 42 {
		t.Errorf("Preview.MaxBinaries = %d, want 42", cfg.Preview.MaxBinaries)
	}
	if cfg.Preview.MaxBinariesBytes != 7654321 {
		t.Errorf("Preview.MaxBinariesBytes = %d, want 7654321", cfg.Preview.MaxBinariesBytes)
	}
	if cfg.Preview.Redis.Host != "preview-redis" {
		t.Errorf("Preview.Redis.Host = %q, want %q", cfg.Preview.Redis.Host, "preview-redis")
	}
	if cfg.Preview.Redis.Port != 6391 {
		t.Errorf("Preview.Redis.Port = %d, want 6391", cfg.Preview.Redis.Port)
	}
	if cfg.Preview.Redis.Password != "preview-secret" {
		t.Errorf("Preview.Redis.Password = %q, want %q", cfg.Preview.Redis.Password, "preview-secret")
	}
	if cfg.Preview.Redis.DB != 7 {
		t.Errorf("Preview.Redis.DB = %d, want 7", cfg.Preview.Redis.DB)
	}
}

// TestLoadPreviewDefaults guards the defaults the phase-0 measurement
// justified. They are pinned so a careless edit of setDefaults is caught
// here, not in production.
func TestLoadPreviewDefaults(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.Preview.CacheTTL != 24*time.Hour {
		t.Errorf("Preview.CacheTTL = %v, want the default 24h", cfg.Preview.CacheTTL)
	}
	if cfg.Preview.BuildTimeout != 2*time.Minute {
		t.Errorf("Preview.BuildTimeout = %v, want the default 2m", cfg.Preview.BuildTimeout)
	}
	if cfg.Preview.MaxConcurrentBuilds != 4 {
		t.Errorf("Preview.MaxConcurrentBuilds = %d, want the default 4", cfg.Preview.MaxConcurrentBuilds)
	}
	if cfg.Preview.MaxFB2Bytes != 32<<20 {
		t.Errorf("Preview.MaxFB2Bytes = %d, want the default 32 MiB", cfg.Preview.MaxFB2Bytes)
	}
	if cfg.Preview.MaxBinaries != 1000 {
		t.Errorf("Preview.MaxBinaries = %d, want the default 1000", cfg.Preview.MaxBinaries)
	}
	if cfg.Preview.MaxBinariesBytes != 32<<20 {
		t.Errorf("Preview.MaxBinariesBytes = %d, want the default 32 MiB", cfg.Preview.MaxBinariesBytes)
	}
}

// TestPreviewRedisFallsBackToMainRedis is the behavior the reviewer asked
// for: a config that says nothing about preview.redis must resolve to the
// main Redis connection — not to a hardcoded localhost. The DB number is the
// exception: it stays the preview's own, because sharing the instance is the
// default while sharing the keyspace never is.
func TestPreviewRedisFallsBackToMainRedis(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, `
redis:
  host: main-redis
  port: 6390
  password: main-secret
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if got := cfg.GetPreviewRedisAddress(); got != "main-redis:6390" {
		t.Errorf("GetPreviewRedisAddress() = %q, want the main Redis %q — "+
			"a hardcoded localhost here means an empty preview.redis ignores the configured Redis",
			got, "main-redis:6390")
	}
	if got := cfg.GetPreviewRedisPassword(); got != "main-secret" {
		t.Errorf("GetPreviewRedisPassword() = %q, want the main Redis password", got)
	}
	if cfg.Preview.Redis.DB != 3 {
		t.Errorf("Preview.Redis.DB = %d, want the preview's own default DB 3", cfg.Preview.Redis.DB)
	}
}

// TestPreviewRedisFallsBackPerField pins that the override is per field:
// setting only the preview host must not silently reset the port to the
// zero value — the unset port still comes from the main Redis.
func TestPreviewRedisFallsBackPerField(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, `
redis:
  host: main-redis
  port: 6390
preview:
  redis:
    host: preview-redis
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if got := cfg.GetPreviewRedisAddress(); got != "preview-redis:6390" {
		t.Errorf("GetPreviewRedisAddress() = %q, want %q — own host, main port",
			got, "preview-redis:6390")
	}
}

// TestPreviewRedisEnvOverride pins that GOPDS_PREVIEW_* environment
// variables reach the section too — bindEnvKeys only walks keys that exist
// in the struct, so a field missing from Config would be silently
// unreachable from the environment.
func TestPreviewRedisEnvOverride(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	setEnv(t, map[string]string{
		"GOPDS_PREVIEW_REDIS_HOST": "env-preview-redis",
		"GOPDS_PREVIEW_CACHE_TTL":  "90m",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.Preview.Redis.Host != "env-preview-redis" {
		t.Errorf("Preview.Redis.Host = %q, want %q", cfg.Preview.Redis.Host, "env-preview-redis")
	}
	if cfg.Preview.CacheTTL != 90*time.Minute {
		t.Errorf("Preview.CacheTTL = %v, want 90m", cfg.Preview.CacheTTL)
	}
}
