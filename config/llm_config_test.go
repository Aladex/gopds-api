package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The llm section is the single provider setting for every feature that talks
// to a model: it must resolve one endpoint, one optional key, one model and
// one timeout from the file and its own environment names — nothing else. The
// obsolete OPENAI_* variables are deliberately not read anywhere.

// clearLLMEnv removes every variable that could feed the llm section: its own
// LLM_* names, their GOPDS_-prefixed twins, and the obsolete OPENAI_* names —
// cleared so a test asserting those are ignored cannot be confused by a
// leftover, and so a host value cannot flip a precedence case.
func clearLLMEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"LLM_BASE_URL", "LLM_API_KEY", "LLM_MODEL",
		"GOPDS_LLM_BASE_URL", "GOPDS_LLM_API_KEY", "GOPDS_LLM_MODEL",
		"OPENAI_API_KEY", "OPENAI_MODEL",
	} {
		// t.Setenv registers the restore; unset right after so the variable
		// is absent rather than empty (an empty value reads as unset anyway,
		// but absence is what the tests mean).
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unsetting %s: %v", key, err)
		}
	}
}

// isolateLLM gives an llm-section test the clean config test environment plus
// cleared llm environment variables.
func isolateLLM(t *testing.T) {
	t.Helper()
	isolate(t)
	clearLLMEnv(t)
}

func TestLoadLLMDefaults(t *testing.T) {
	isolateLLM(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	want := LLMConfig{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "",
		Model:   LLMDefaultModel,
		Timeout: 30 * time.Second,
	}
	if cfg.LLM != want {
		t.Errorf("LLM = %+v, want %+v", cfg.LLM, want)
	}
	if cfg.LLM.Configured() {
		t.Error("default OpenAI endpoint without a key must read as not configured")
	}
}

func TestLoadLLMFromFileAndEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  map[string]string
		want LLMConfig
	}{
		{
			name: "file values",
			file: "llm:\n  base_url: http://gateway.internal:8317/v1\n  api_key: file-key\n  model: file-model\n  timeout: 45s\n",
			want: LLMConfig{BaseURL: "http://gateway.internal:8317/v1", APIKey: "file-key", Model: "file-model", Timeout: 45 * time.Second},
		},
		{
			name: "unprefixed env overrides file",
			file: "llm:\n  base_url: http://gateway.internal:8317/v1\n  api_key: file-key\n  model: file-model\n  timeout: 45s\n",
			env:  map[string]string{"LLM_BASE_URL": "http://env-gateway:9/v1", "LLM_API_KEY": "env-key", "LLM_MODEL": "env-model"},
			want: LLMConfig{BaseURL: "http://env-gateway:9/v1", APIKey: "env-key", Model: "env-model", Timeout: 45 * time.Second},
		},
		{
			name: "GOPDS-prefixed env beats the unprefixed name",
			file: "llm:\n  base_url: http://gateway.internal:8317/v1\n",
			env:  map[string]string{"LLM_BASE_URL": "http://env-gateway:9/v1", "GOPDS_LLM_BASE_URL": "http://prefixed-gateway:10/v1"},
			want: LLMConfig{BaseURL: "http://prefixed-gateway:10/v1", Model: LLMDefaultModel, Timeout: 30 * time.Second},
		},
		{
			name: "prefixed env timeout",
			env:  map[string]string{"GOPDS_LLM_TIMEOUT": "10s"},
			want: LLMConfig{BaseURL: "https://api.openai.com/v1", Model: LLMDefaultModel, Timeout: 10 * time.Second},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateLLM(t)
			setEnv(t, requiredEnv)
			setEnv(t, tc.env)
			if tc.file != "" {
				writeConfigFile(t, tc.file)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() = %v, want nil", err)
			}
			if cfg.LLM != tc.want {
				t.Errorf("LLM = %+v, want %+v", cfg.LLM, tc.want)
			}
		})
	}
}

// The OPENAI_* variables predate the section and are read nowhere: setting
// them must not configure a key, swap the model, or make the default OpenAI
// endpoint look configured.
func TestLoadLLMIgnoresObsoleteOpenAIEnv(t *testing.T) {
	isolateLLM(t)
	setEnv(t, requiredEnv)
	setEnv(t, map[string]string{"OPENAI_API_KEY": "obsolete-key", "OPENAI_MODEL": "obsolete-model"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	want := LLMConfig{BaseURL: "https://api.openai.com/v1", Model: LLMDefaultModel, Timeout: 30 * time.Second}
	if cfg.LLM != want {
		t.Errorf("LLM = %+v, want %+v", cfg.LLM, want)
	}
	if cfg.LLM.Configured() {
		t.Error("OPENAI_API_KEY alone must not make the default endpoint configured")
	}
}

// The language-detection keys renamed off the OpenAI name: defaults off with
// a 5s arbiter timeout, overridable through the file and the environment.
func TestLoadScanningLLMLangDetectionKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  map[string]string
		want ScanningConfig
	}{
		{
			name: "defaults",
			want: ScanningConfig{SkipDuplicates: true, EnableLanguageDetection: true,
				LLMLangDetectionTimeout: "5s", MaxConcurrentFiles: 1, BatchSize: 50},
		},
		{
			name: "file",
			file: "scanning:\n  enable_llm_lang_detection: true\n  llm_lang_detection_timeout: 9s\n",
			want: ScanningConfig{SkipDuplicates: true, EnableLanguageDetection: true,
				EnableLLMLangDetection: true, LLMLangDetectionTimeout: "9s",
				MaxConcurrentFiles: 1, BatchSize: 50},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateLLM(t)
			setEnv(t, requiredEnv)
			if tc.file != "" {
				writeConfigFile(t, tc.file)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() = %v, want nil", err)
			}
			if cfg.Scanning != tc.want {
				t.Errorf("Scanning = %+v, want %+v", cfg.Scanning, tc.want)
			}
		})
	}
}

// "Configured" is the gate every LLM feature consults before sending anything:
// a base URL must be there, and either a key is set or the endpoint is not the
// public OpenAI API — a keyless gateway in front of the models is a valid
// provider, while keyless requests to api.openai.com are always a mistake.
func TestLLMConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  LLMConfig
		want bool
	}{
		{name: "nothing set", cfg: LLMConfig{}, want: false},
		{name: "keyless gateway", cfg: LLMConfig{BaseURL: "http://cli-proxy-api.bots.svc:8317/v1"}, want: true},
		{name: "gateway with key", cfg: LLMConfig{BaseURL: "http://cli-proxy-api.bots.svc:8317/v1", APIKey: "k"}, want: true},
		{name: "public API without key", cfg: LLMConfig{BaseURL: "https://api.openai.com/v1"}, want: false},
		{name: "public API with key", cfg: LLMConfig{BaseURL: "https://api.openai.com/v1", APIKey: "k"}, want: true},
		{name: "host is case-insensitive", cfg: LLMConfig{BaseURL: "https://API.OPENAI.COM/v1"}, want: false},
		{name: "lookalike host is a gateway", cfg: LLMConfig{BaseURL: "https://api.openai.com.evil.example/v1"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Configured(); got != tc.want {
				t.Errorf("Configured() = %v, want %v for %+v", got, tc.want, tc.cfg)
			}
		})
	}
}

// An explicitly empty base_url switches every LLM feature off — the operator's
// way of saying "no model here" without uninstalling anything.
func TestLoadLLMEmptyBaseURLDisables(t *testing.T) {
	isolateLLM(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, "llm:\n  base_url: \"\"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.LLM.BaseURL != "" {
		t.Errorf("BaseURL = %q, want the explicit empty value to survive", cfg.LLM.BaseURL)
	}
	if cfg.LLM.Configured() {
		t.Error("an empty base_url must read as not configured")
	}
}

func TestLoadLLMRefusesNonPositiveTimeout(t *testing.T) {
	for _, value := range []string{"0s", "-1s"} {
		t.Run("timeout="+value, func(t *testing.T) {
			isolateLLM(t)
			setEnv(t, requiredEnv)
			writeConfigFile(t, "llm:\n  timeout: "+value+"\n")

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() = nil, want an error naming llm.timeout")
			}
			if !strings.Contains(err.Error(), "llm.timeout") {
				t.Errorf("Load() = %v, want the key llm.timeout named", err)
			}
		})
	}
}

// LLM() is what the zero-argument llm constructors read; it must return
// exactly what Load resolved.
func TestLLMAccessorReturnsLoadedSection(t *testing.T) {
	isolateLLM(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, "llm:\n  base_url: http://gateway.internal:8317/v1\n")
	setEnv(t, map[string]string{"LLM_API_KEY": "env-key"})

	if _, err := Load(); err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	got := LLM()
	want := LLMConfig{BaseURL: "http://gateway.internal:8317/v1", APIKey: "env-key", Model: LLMDefaultModel, Timeout: 30 * time.Second}
	if got != want {
		t.Errorf("LLM() = %+v, want %+v", got, want)
	}
}
