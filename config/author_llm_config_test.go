package config

import (
	"strings"
	"testing"
	"time"
)

// The authors-specific LLM settings: the four participants, their request
// parameters, batching, concurrency, attempts and the token budget. The
// endpoint and the key are the shared llm section's: an installation has one
// provider, and the author layer is switched off exactly when that section
// allows no requests or the four participants are not four distinct models.

func TestLoadAuthorLLMDefaults(t *testing.T) {
	isolateLLM(t)
	setEnv(t, requiredEnv)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	got := cfg.AuthorMetadata.LLM
	want := DefaultAuthorLLMConfig()
	if got.Models != want.Models {
		t.Errorf("models = %+v, want %+v", got.Models, want.Models)
	}
	if got.Models.ProductionA != "gpt-6-luna" || got.Models.ProductionB != AuthorLLMDefaultProductionB ||
		got.Models.JudgeA != AuthorLLMDefaultJudgeA || got.Models.JudgeB != AuthorLLMDefaultJudgeB {
		t.Errorf("default participants = %+v", got.Models)
	}
	if got.ItemsPerRequest != 1 || got.ConcurrencyStart != 4 || got.ConcurrencyMax != 16 || got.ConcurrencyTotal != 32 {
		t.Errorf("batching/concurrency = %d/%d/%d/%d", got.ItemsPerRequest, got.ConcurrencyStart, got.ConcurrencyMax, got.ConcurrencyTotal)
	}
	if got.RequestTimeout != 120*time.Second || got.RequestTimeoutPerItem != 30*time.Second {
		t.Errorf("timeouts = %v/%v", got.RequestTimeout, got.RequestTimeoutPerItem)
	}
	if got.Budget.OneOffTokens != 900_000_000 || got.Budget.RunOverrun != 1.5 {
		t.Errorf("budget = %+v", got.Budget)
	}
	if got.MaxOutput.PerCallTokens != 1000 || got.MaxOutput.PerItemTokens != 3000 ||
		got.OutputLimitField != AuthorLLMOutputMaxCompletionTokens {
		t.Errorf("output ceiling = %+v in %q", got.MaxOutput, got.OutputLimitField)
	}
	if got.FingerprintEvery != 500 || got.MaxAttempts != 5 || got.ItemTimeoutLimit != 2 {
		t.Errorf("fingerprint every/max attempts/item timeouts = %d/%d/%d", got.FingerprintEvery, got.MaxAttempts,
			got.ItemTimeoutLimit)
	}
	if got.Params.JudgeB.Temperature != nil {
		t.Error("a temperature is set by default")
	}
}

func TestAuthorLLMTimeoutEscalatesForOneItem(t *testing.T) {
	c := DefaultAuthorLLMConfig()
	for step, want := range []time.Duration{120 * time.Second, 300 * time.Second, 600 * time.Second, 600 * time.Second} {
		if got := c.EscalatedCallTimeout(1, step); got != want {
			t.Errorf("one item at step %d: %v, want %v", step, got, want)
		}
	}
	if got := c.EscalatedCallTimeout(10, 2); got != c.CallTimeout(10) {
		t.Errorf("a batch escalated: %v", got)
	}
	c.TimeoutEscalation = nil
	if got := c.EscalatedCallTimeout(1, 2); got != c.CallTimeout(1) {
		t.Errorf("no escalation configured: %v", got)
	}
}

func TestAuthorLLMRequestTimeoutScalesWithTheBatch(t *testing.T) {
	c := DefaultAuthorLLMConfig()
	if c.CallTimeout(1) != 120*time.Second {
		t.Errorf("one item: %v", c.CallTimeout(1))
	}
	if got := c.CallTimeout(10); got < 300*time.Second {
		t.Errorf("ten items: %v, want at least 300s (measured 120-211 s per 10-item call)", got)
	}
}

func TestLoadAuthorLLMEnvOverridesParticipants(t *testing.T) {
	lowEffort := AuthorLLMReasoningEfforts()[2]
	isolateLLM(t)
	setEnv(t, requiredEnv)
	writeConfigFile(t, `author_metadata:
  llm:
    models:
      judge_a: gpt-6.1-sol
    params:
      judge_a:
        reasoning_effort: high
`)
	setEnv(t, map[string]string{
		"GOPDS_AUTHOR_METADATA_LLM_MODELS_JUDGE_B":                       "glm-5.3",
		"GOPDS_AUTHOR_METADATA_LLM_PARAMS_JUDGE_B_TEMPERATURE":           "1",
		"GOPDS_AUTHOR_METADATA_LLM_ITEMS_PER_REQUEST":                    "10",
		"GOPDS_AUTHOR_METADATA_LLM_BUDGET_ONE_OFF_TOKENS":                "1000",
		"GOPDS_AUTHOR_METADATA_LLM_REQUEST_TIMEOUT_PER_ITEM":             "45s",
		"GOPDS_AUTHOR_METADATA_LLM_CONCURRENCY_TOTAL":                    "8",
		"GOPDS_AUTHOR_METADATA_LLM_OUTPUT_LIMIT_FIELD":                   "max_tokens",
		"GOPDS_AUTHOR_METADATA_LLM_ITEM_TIMEOUT_LIMIT":                   "5",
		"GOPDS_AUTHOR_METADATA_LLM_TIMEOUT_ESCALATION":                   "200s,400s,800s",
		"GOPDS_AUTHOR_METADATA_LLM_PARAMS_PRODUCTION_A_REASONING_EFFORT": lowEffort,
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	got := cfg.AuthorMetadata.LLM
	if got.Models.JudgeB != "glm-5.3" || got.Models.JudgeA != "gpt-6.1-sol" || got.Models.ProductionA != AuthorLLMDefaultProductionA {
		t.Errorf("models = %+v", got.Models)
	}
	if got.Params.JudgeB.Temperature == nil || *got.Params.JudgeB.Temperature != 1 {
		t.Errorf("judge_b temperature = %v", got.Params.JudgeB.Temperature)
	}
	if got.Params.JudgeA.ReasoningEffort != AuthorLLMEffortHigh || got.Params.ProductionA.ReasoningEffort != lowEffort {
		t.Errorf("efforts = %q/%q", got.Params.JudgeA.ReasoningEffort, got.Params.ProductionA.ReasoningEffort)
	}
	if got.ItemsPerRequest != 10 || got.Budget.OneOffTokens != 1000 || got.RequestTimeoutPerItem != 45*time.Second ||
		got.ConcurrencyTotal != 8 || got.OutputLimitField != AuthorLLMOutputMaxTokens || got.ItemTimeoutLimit != 5 ||
		len(got.TimeoutEscalation) != 3 || got.TimeoutEscalation[2] != 800*time.Second {
		t.Errorf("overrides = %+v", got)
	}
}

func TestAuthorLLMStatus(t *testing.T) {
	gateway := LLMConfig{BaseURL: "http://cli-proxy-api.bots.svc:8317/v1", Timeout: time.Second}
	cases := []struct {
		name   string
		shared LLMConfig
		mutate func(*AuthorLLMConfig)
		reason string
	}{
		{"keyless gateway", gateway, nil, ""},
		{"keyed gateway", LLMConfig{BaseURL: gateway.BaseURL, APIKey: "k"}, nil, ""},
		{"empty base url", LLMConfig{BaseURL: "", APIKey: "k"}, nil, AuthorLLMDisabledNotConfigured},
		{"public api without key", LLMConfig{BaseURL: "https://api.openai.com/v1"}, nil, AuthorLLMDisabledNotConfigured},
		{"missing model", gateway, func(c *AuthorLLMConfig) { c.Models.ProductionB = "" }, AuthorLLMDisabledModels},
		{"blank model", gateway, func(c *AuthorLLMConfig) { c.Models.JudgeA = "  " }, AuthorLLMDisabledModels},
		{"same models", gateway, func(c *AuthorLLMConfig) { c.Models.JudgeB = c.Models.ProductionA }, AuthorLLMDisabledModels},
		{"unknown effort", gateway, func(c *AuthorLLMConfig) { c.Params.JudgeA.ReasoningEffort = "max" }, AuthorLLMDisabledModels},
	}
	for _, tc := range cases {
		c := DefaultAuthorLLMConfig()
		if tc.mutate != nil {
			tc.mutate(&c)
		}
		st := c.Status(tc.shared)
		if st.Enabled != (tc.reason == "") || st.Reason != tc.reason {
			t.Errorf("%s: status = %+v, want reason %q", tc.name, st, tc.reason)
		}
	}
}

func TestLoadAuthorLLMRefusesBadLimits(t *testing.T) {
	for env, value := range map[string]string{
		"GOPDS_AUTHOR_METADATA_LLM_ITEMS_PER_REQUEST":          "0",
		"GOPDS_AUTHOR_METADATA_LLM_CONCURRENCY_START":          "0",
		"GOPDS_AUTHOR_METADATA_LLM_CONCURRENCY_MAX":            "2",
		"GOPDS_AUTHOR_METADATA_LLM_CONCURRENCY_TOTAL":          "0",
		"GOPDS_AUTHOR_METADATA_LLM_REQUEST_TIMEOUT":            "0s",
		"GOPDS_AUTHOR_METADATA_LLM_REQUEST_TIMEOUT_PER_ITEM":   "-1s",
		"GOPDS_AUTHOR_METADATA_LLM_MAX_ATTEMPTS":               "0",
		"GOPDS_AUTHOR_METADATA_LLM_ITEM_TIMEOUT_LIMIT":         "0",
		"GOPDS_AUTHOR_METADATA_LLM_TIMEOUT_ESCALATION":         "300s,200s",
		"GOPDS_AUTHOR_METADATA_LLM_FINGERPRINT_EVERY":          "0",
		"GOPDS_AUTHOR_METADATA_LLM_BUDGET_RUN_OVERRUN":         "0.9",
		"GOPDS_AUTHOR_METADATA_LLM_MAX_OUTPUT_PER_ITEM_TOKENS": "0",
		"GOPDS_AUTHOR_METADATA_LLM_MAX_OUTPUT_PER_CALL_TOKENS": "-1",
		"GOPDS_AUTHOR_METADATA_LLM_OUTPUT_LIMIT_FIELD":         "max_output_tokens",
	} {
		t.Run(env, func(t *testing.T) {
			isolateLLM(t)
			setEnv(t, requiredEnv)
			setEnv(t, map[string]string{env: value})
			_, err := Load()
			if err == nil {
				t.Fatalf("%s=%s accepted", env, value)
			}
			if !strings.Contains(err.Error(), "author_metadata.llm.") {
				t.Errorf("error %q does not name the key", err)
			}
		})
	}
}

func TestLoadAuthorLLMRefusesOutputLimitsThatCannotFit(t *testing.T) {
	for _, tc := range []struct{ name, env, value string }{
		{"huge per call", "GOPDS_AUTHOR_METADATA_LLM_MAX_OUTPUT_PER_CALL_TOKENS", "9223372036854775807"},
		{"huge per item", "GOPDS_AUTHOR_METADATA_LLM_MAX_OUTPUT_PER_ITEM_TOKENS", "9223372036854775807"},
		// Fits alone; the ceiling of a full batch does not.
		{"full batch over the cap", "GOPDS_AUTHOR_METADATA_LLM_MAX_OUTPUT_PER_ITEM_TOKENS", "60000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateLLM(t)
			setEnv(t, requiredEnv)
			setEnv(t, map[string]string{tc.env: tc.value})
			_, err := Load()
			if err == nil {
				t.Fatalf("%s=%s accepted", tc.env, tc.value)
			}
			if !strings.Contains(err.Error(), "author_metadata.llm.max_output") {
				t.Errorf("error %q does not name the setting", err)
			}
		})
	}
	c := DefaultAuthorLLMConfig()
	c.MaxOutput.PerCallTokens = AuthorLLMMaxOutputCeiling - AuthorLLMMaxItemsPerRequest*c.MaxOutput.PerItemTokens
	if err := c.validate(); err != nil {
		t.Errorf("the largest ceiling that fits was refused: %v", err)
	}
}

// The price is optional and for display only: empty unit by default (the card
// shows tokens alone), settable per participant, never negative.
func TestLoadAuthorLLMPrice(t *testing.T) {
	isolateLLM(t)
	setEnv(t, requiredEnv)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.AuthorMetadata.LLM.Price != (AuthorLLMPrice{}) {
		t.Errorf("default price = %+v, want none", cfg.AuthorMetadata.LLM.Price)
	}

	isolateLLM(t)
	setEnv(t, requiredEnv)
	setEnv(t, map[string]string{
		"GOPDS_AUTHOR_METADATA_LLM_PRICE_UNIT":    "USD",
		"GOPDS_AUTHOR_METADATA_LLM_PRICE_JUDGE_A": "2.5",
	})
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := cfg.AuthorMetadata.LLM.Price; got.Unit != "USD" || got.JudgeA != 2.5 || got.ProductionA != 0 {
		t.Errorf("price = %+v", got)
	}

	isolateLLM(t)
	setEnv(t, requiredEnv)
	setEnv(t, map[string]string{"GOPDS_AUTHOR_METADATA_LLM_PRICE_PRODUCTION_B": "-1"})
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "author_metadata.llm.price") {
		t.Errorf("a negative price: %v", err)
	}
}
