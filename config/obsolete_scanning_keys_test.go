package config

import (
	"strings"
	"testing"

	"gopds-api/logging"

	//nolint:depguard // asserting on emitted log output needs logrus' own test hook, and logging wraps logrus
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

// The production config file lives in Vault and still carries the pre-rename
// scanning keys. They must be ignored — never honored as aliases — and each
// one present earns a single startup warning naming its replacement, so a
// stale file says what to fix instead of silently switching the LLM language
// arbiter off. Key names only: no configured value reaches the log.

func TestLoadWarnsOncePerObsoleteScanningKey(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, "scanning:\n  enable_openai_lang_detection: true\n  openai_lang_detection_timeout: 17s\n")

	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil: an obsolete key warns, it does not fail", err)
	}

	// The values are ignored: the new keys stay at their defaults.
	if cfg.Scanning.EnableLLMLangDetection {
		t.Error("the obsolete enable key must be ignored, not honored")
	}
	if cfg.Scanning.LLMLangDetectionTimeout != "5s" {
		t.Errorf("LLMLangDetectionTimeout = %q, want the default 5s: the obsolete key must be ignored",
			cfg.Scanning.LLMLangDetectionTimeout)
	}

	var logged strings.Builder
	for _, entry := range hook.AllEntries() {
		logged.WriteString(entry.Message)
		logged.WriteByte('\n')
	}
	out := logged.String()

	for _, key := range []string{
		"scanning.enable_openai_lang_detection", "scanning.enable_llm_lang_detection",
		"scanning.openai_lang_detection_timeout", "scanning.llm_lang_detection_timeout",
	} {
		if !strings.Contains(out, key) {
			t.Errorf("no warning names %q; logged:\n%s", key, out)
		}
	}
	if n := strings.Count(out, "scanning.enable_openai_lang_detection"); n != 1 {
		t.Errorf("the enable warning fired %d times, want exactly 1", n)
	}
	if n := strings.Count(out, "scanning.openai_lang_detection_timeout"); n != 1 {
		t.Errorf("the timeout warning fired %d times, want exactly 1", n)
	}
	if strings.Contains(out, "17s") {
		t.Error("the obsolete key's value reached the log; warnings must carry key names only")
	}
}

// A file without the old keys stays silent about them.
func TestLoadSilentWithoutObsoleteScanningKeys(t *testing.T) {
	isolate(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, "scanning:\n  enable_llm_lang_detection: true\n")

	hook := logrustest.NewLocal(logging.GetLogger())
	t.Cleanup(hook.Reset)

	if _, err := Load(); err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "obsolete") {
			t.Errorf("unexpected obsolete-key warning: %s", entry.Message)
		}
	}
}
