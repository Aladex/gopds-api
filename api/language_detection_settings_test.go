package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopds-api/config"
	"gopds-api/logging"

	"github.com/spf13/viper"

	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

// The production toggle for the LLM language arbiter is the canonical
// GOPDS_SCANNING_ENABLE_LLM_LANG_DETECTION: viper's GOPDS_ prefix means an
// unprefixed ENABLE_LLM_LANG_DETECTION is read by nothing. A config file that
// still carries the obsolete openai-named key must earn its warning and stay
// ignored, while the canonical variable switches the arbiter on through the
// same settings read here.
func TestCanonicalEnvEnablesLLMLangDetectionAlongsideObsoleteKey(t *testing.T) {
	dir := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("restoring working directory: %v", err)
		}
	})
	viper.Reset()
	t.Cleanup(viper.Reset)

	if err := os.WriteFile(filepath.Join(".", "config.yaml"),
		[]byte("scanning:\n  enable_openai_lang_detection: true\n"), 0o600); err != nil {
		t.Fatalf("writing config.yaml: %v", err)
	}

	for key, value := range map[string]string{
		"GOPDS_SECRET_KEY":       "test-secret",
		"GOPDS_POSTGRES_DBUSER":  "test-user",
		"GOPDS_POSTGRES_DBNAME":  "test-db",
		"GOPDS_SESSIONS_KEY":     "test-session",
		"GOPDS_SESSIONS_REFRESH": "test-refresh",

		"GOPDS_SCANNING_ENABLE_LANGUAGE_DETECTION": "true",
		"GOPDS_SCANNING_ENABLE_LLM_LANG_DETECTION": "true",
		// A distinctive value: the arbiter timeout must resolve through the
		// canonical key, not by coincidence of the 5s default.
		"GOPDS_SCANNING_LLM_LANG_DETECTION_TIMEOUT": "7s",
	} {
		t.Setenv(key, value)
	}

	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	if _, err := config.Load(); err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	enableDetection, enableLLM, timeout := getLanguageDetectionSettings()
	if !enableDetection {
		t.Error("language detection itself must be on through its canonical variable")
	}
	if !enableLLM {
		t.Error("the LLM arbiter must be ON through GOPDS_SCANNING_ENABLE_LLM_LANG_DETECTION")
	}
	if timeout != 7*time.Second {
		t.Errorf("arbiter timeout = %v, want the canonical 7s", timeout)
	}

	var logged strings.Builder
	for _, entry := range hook.AllEntries() {
		logged.WriteString(entry.Message)
		logged.WriteByte('\n')
	}
	out := logged.String()
	if !strings.Contains(out, "scanning.enable_openai_lang_detection") ||
		!strings.Contains(out, "scanning.enable_llm_lang_detection") {
		t.Errorf("the obsolete-key warning is missing; logged:\n%s", out)
	}
}
