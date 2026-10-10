package config

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// The author layer's LLM settings (author_metadata.llm). The endpoint and the
// key are not here: they are the shared llm section's, so an installation
// names its provider once. What is here is specific to the author layer —
// which four models take part, their request parameters, how many names one
// request carries, how many requests run at once, and the token budget.
//
// The participants are fixed fields rather than a list or a map: bindEnvKeys
// reaches only struct leaves, and every one of these is meant to be settable
// from the environment (GOPDS_AUTHOR_METADATA_LLM_MODELS_JUDGE_B=glm-5.3).

// AuthorLLMModels names the model of each participant.
type AuthorLLMModels struct {
	ProductionA string `mapstructure:"production_a" yaml:"production_a"`
	ProductionB string `mapstructure:"production_b" yaml:"production_b"`
	JudgeA      string `mapstructure:"judge_a" yaml:"judge_a"`
	JudgeB      string `mapstructure:"judge_b" yaml:"judge_b"`
}

// AuthorLLMParams are one participant's optional request parameters; unset
// means the request carries none and the endpoint's default applies.
type AuthorLLMParams struct {
	Temperature     *float64 `mapstructure:"temperature" yaml:"temperature"`
	ReasoningEffort string   `mapstructure:"reasoning_effort" yaml:"reasoning_effort"`
}

// AuthorLLMParamsSet holds the parameters of each participant.
type AuthorLLMParamsSet struct {
	ProductionA AuthorLLMParams `mapstructure:"production_a" yaml:"production_a"`
	ProductionB AuthorLLMParams `mapstructure:"production_b" yaml:"production_b"`
	JudgeA      AuthorLLMParams `mapstructure:"judge_a" yaml:"judge_a"`
	JudgeB      AuthorLLMParams `mapstructure:"judge_b" yaml:"judge_b"`
}

// AuthorLLMMaxOutput is the output ceiling every request carries:
// PerCallTokens plus PerItemTokens per item, reasoning included. With the
// prompt's byte bound it makes the hard bound of a call the claim reserves.
type AuthorLLMMaxOutput struct {
	PerCallTokens int `mapstructure:"per_call_tokens" yaml:"per_call_tokens"`
	PerItemTokens int `mapstructure:"per_item_tokens" yaml:"per_item_tokens"`
}

// AuthorLLMBudget is the runaway guard, in tokens: it exists so a looping
// worker or a retry bug cannot burn a quota or a balance, not to save money.
type AuthorLLMBudget struct {
	// OneOffTokens bounds every run together (eval, resolve, swap).
	OneOffTokens int64 `mapstructure:"one_off_tokens" yaml:"one_off_tokens"`
	// RunOverrun pauses a run once it spent this multiple of its own
	// estimate.
	RunOverrun float64 `mapstructure:"run_overrun" yaml:"run_overrun"`
}

// AuthorLLMConfig is the author_metadata.llm section.
type AuthorLLMConfig struct {
	Models AuthorLLMModels    `mapstructure:"models" yaml:"models"`
	Params AuthorLLMParamsSet `mapstructure:"params" yaml:"params"`
	// ItemsPerRequest is how many names one request carries. The quality
	// check compares 1 against larger batches before a resolve run uses one.
	ItemsPerRequest int `mapstructure:"items_per_request" yaml:"items_per_request"`
	// RequestTimeout bounds a one-item call; every further item of a batch
	// adds RequestTimeoutPerItem (CallTimeout).
	RequestTimeout        time.Duration `mapstructure:"request_timeout" yaml:"request_timeout"`
	RequestTimeoutPerItem time.Duration `mapstructure:"request_timeout_per_item" yaml:"request_timeout_per_item"`
	// ConcurrencyStart and ConcurrencyMax bound each participant's AIMD
	// window of calls in flight; ConcurrencyTotal bounds all participants
	// together, across replicas.
	ConcurrencyStart int `mapstructure:"concurrency_start" yaml:"concurrency_start"`
	ConcurrencyMax   int `mapstructure:"concurrency_max" yaml:"concurrency_max"`
	ConcurrencyTotal int `mapstructure:"concurrency_total" yaml:"concurrency_total"`
	// MaxAttempts is how many calls one job gets before it fails.
	MaxAttempts int `mapstructure:"max_attempts" yaml:"max_attempts"`
	// FingerprintEvery is how many answered jobs of a participant pass
	// between two tokenizer fingerprint checks.
	FingerprintEvery int `mapstructure:"fingerprint_every" yaml:"fingerprint_every"`
	// MaxOutput is the output ceiling every request carries, in
	// OutputLimitField: max_completion_tokens (OpenAI's current name) or
	// max_tokens, whichever the endpoint and its models honor.
	MaxOutput        AuthorLLMMaxOutput `mapstructure:"max_output" yaml:"max_output"`
	OutputLimitField string             `mapstructure:"output_limit_field" yaml:"output_limit_field"`
	Budget           AuthorLLMBudget    `mapstructure:"budget" yaml:"budget"`
}

// AuthorLLMMaxItemsPerRequest caps the batch size.
const AuthorLLMMaxItemsPerRequest = 20

// AuthorLLMMaxOutputCeiling caps the output ceiling of the largest batch
// (per_call_tokens + 20 × per_item_tokens). A call's ceiling is stored as a
// PostgreSQL INTEGER and its reserve is computed in int64; this cap keeps both
// far from overflow, and is well above any model's output limit.
const AuthorLLMMaxOutputCeiling = 1_000_000

// The default participants: the production pair and the judges the phase-0
// rehearsal settled on (gpt-6.1-sol is not served by the in-cluster gateway).
const (
	AuthorLLMDefaultProductionA = "gpt-6-luna"
	AuthorLLMDefaultProductionB = "deepseek-v4-pro"
	AuthorLLMDefaultJudgeA      = "gpt-6-sol"
	AuthorLLMDefaultJudgeB      = "claude-opus-5"
)

// Defaults of the section. Latency through the gateway is 13-21 s for one
// item and 52-211 s for ten, so a call's timeout grows with its batch
// (CallTimeout: 120 s + 30 s per further item, 390 s for ten), and the
// worker's leases follow the timeout. The concurrency numbers are design K's
// (start 4, AIMD up to 16, 32 calls in flight per endpoint).
const (
	authorLLMItemsPerRequest       = 1
	authorLLMRequestTimeout        = 120 * time.Second
	authorLLMRequestTimeoutPerItem = 30 * time.Second
	authorLLMConcurrencyStart      = 4
	authorLLMConcurrencyMax        = 16
	authorLLMConcurrencyTotal      = 32
	authorLLMMaxAttempts           = 5
	authorLLMFingerprintEvery      = 500
	authorLLMOutputPerCall         = 1000
	authorLLMOutputPerItem         = 3000
	authorLLMOneOffTokens          = 900_000_000
	authorLLMRunOverrun            = 1.5
)

// The request fields an output ceiling can travel in (llmreq has the same
// two).
const (
	AuthorLLMOutputMaxCompletionTokens = "max_completion_tokens"
	AuthorLLMOutputMaxTokens           = "max_tokens"
)

// AuthorLLMOutputCeilingFits reports whether the ceiling of the largest
// batch, perCall + 20 × perItem, is at most AuthorLLMMaxOutputCeiling. It
// compares step by step, so no operand can overflow.
func AuthorLLMOutputCeilingFits(perCall, perItem int) bool {
	if perCall < 0 || perItem < 0 || perCall > AuthorLLMMaxOutputCeiling ||
		perItem > AuthorLLMMaxOutputCeiling/AuthorLLMMaxItemsPerRequest {
		return false
	}
	return perCall+AuthorLLMMaxItemsPerRequest*perItem <= AuthorLLMMaxOutputCeiling
}

// DefaultAuthorLLMConfig is the section's defaults.
func DefaultAuthorLLMConfig() AuthorLLMConfig {
	return AuthorLLMConfig{
		Models: AuthorLLMModels{
			ProductionA: AuthorLLMDefaultProductionA,
			ProductionB: AuthorLLMDefaultProductionB,
			JudgeA:      AuthorLLMDefaultJudgeA,
			JudgeB:      AuthorLLMDefaultJudgeB,
		},
		ItemsPerRequest:       authorLLMItemsPerRequest,
		RequestTimeout:        authorLLMRequestTimeout,
		RequestTimeoutPerItem: authorLLMRequestTimeoutPerItem,
		ConcurrencyStart:      authorLLMConcurrencyStart,
		ConcurrencyMax:        authorLLMConcurrencyMax,
		ConcurrencyTotal:      authorLLMConcurrencyTotal,
		MaxAttempts:           authorLLMMaxAttempts,
		FingerprintEvery:      authorLLMFingerprintEvery,
		MaxOutput:             AuthorLLMMaxOutput{PerCallTokens: authorLLMOutputPerCall, PerItemTokens: authorLLMOutputPerItem},
		OutputLimitField:      AuthorLLMOutputMaxCompletionTokens,
		Budget:                AuthorLLMBudget{OneOffTokens: authorLLMOneOffTokens, RunOverrun: authorLLMRunOverrun},
	}
}

// setAuthorLLMDefaults registers the section's defaults with viper.
func setAuthorLLMDefaults() {
	d := DefaultAuthorLLMConfig()
	const p = "author_metadata.llm."
	viper.SetDefault(p+"models.production_a", d.Models.ProductionA)
	viper.SetDefault(p+"models.production_b", d.Models.ProductionB)
	viper.SetDefault(p+"models.judge_a", d.Models.JudgeA)
	viper.SetDefault(p+"models.judge_b", d.Models.JudgeB)
	viper.SetDefault(p+"items_per_request", d.ItemsPerRequest)
	viper.SetDefault(p+"request_timeout", d.RequestTimeout)
	viper.SetDefault(p+"request_timeout_per_item", d.RequestTimeoutPerItem)
	viper.SetDefault(p+"concurrency_start", d.ConcurrencyStart)
	viper.SetDefault(p+"concurrency_max", d.ConcurrencyMax)
	viper.SetDefault(p+"concurrency_total", d.ConcurrencyTotal)
	viper.SetDefault(p+"max_attempts", d.MaxAttempts)
	viper.SetDefault(p+"fingerprint_every", d.FingerprintEvery)
	viper.SetDefault(p+"max_output.per_call_tokens", d.MaxOutput.PerCallTokens)
	viper.SetDefault(p+"max_output.per_item_tokens", d.MaxOutput.PerItemTokens)
	viper.SetDefault(p+"output_limit_field", d.OutputLimitField)
	viper.SetDefault(p+"budget.one_off_tokens", d.Budget.OneOffTokens)
	viper.SetDefault(p+"budget.run_overrun", d.Budget.RunOverrun)
}

// CallTimeout is the timeout of one call carrying items names.
func (c *AuthorLLMConfig) CallTimeout(items int) time.Duration {
	if items < 1 {
		items = 1
	}
	return c.RequestTimeout + time.Duration(items-1)*c.RequestTimeoutPerItem
}

// validate refuses limits that would make a claim unbounded, a lease
// endless or the budget meaningless, naming the key. The participants are
// not checked here: a bad participant switches the layer off (Status)
// instead of stopping the server.
func (c *AuthorLLMConfig) validate() error {
	const p = "author_metadata.llm."
	limits := []configLimit{
		{p + "concurrency_start", int64(c.ConcurrencyStart)},
		{p + "concurrency_total", int64(c.ConcurrencyTotal)},
		{p + "request_timeout", int64(c.RequestTimeout)},
		{p + "max_attempts", int64(c.MaxAttempts)},
		{p + "fingerprint_every", int64(c.FingerprintEvery)},
		{p + "max_output.per_item_tokens", int64(c.MaxOutput.PerItemTokens)},
		{p + "budget.one_off_tokens", c.Budget.OneOffTokens},
	}
	for _, l := range limits {
		if l.value <= 0 {
			return limitError(l.key, l.value)
		}
	}
	switch {
	case c.ItemsPerRequest < 1 || c.ItemsPerRequest > AuthorLLMMaxItemsPerRequest:
		return rangeError(p+"items_per_request", "1..20")
	case c.ConcurrencyMax < c.ConcurrencyStart:
		return rangeError(p+"concurrency_max", "at least concurrency_start")
	case c.RequestTimeoutPerItem < 0:
		return rangeError(p+"request_timeout_per_item", "zero or positive")
	case c.Budget.RunOverrun < 1:
		return rangeError(p+"budget.run_overrun", "at least 1")
	case c.MaxOutput.PerCallTokens < 0:
		return rangeError(p+"max_output.per_call_tokens", "zero or positive")
	case !AuthorLLMOutputCeilingFits(c.MaxOutput.PerCallTokens, c.MaxOutput.PerItemTokens):
		return rangeError(p+"max_output", fmt.Sprintf(
			"such that per_call_tokens + %d × per_item_tokens is at most %d", AuthorLLMMaxItemsPerRequest,
			AuthorLLMMaxOutputCeiling))
	case c.OutputLimitField != AuthorLLMOutputMaxCompletionTokens && c.OutputLimitField != AuthorLLMOutputMaxTokens:
		return rangeError(p+"output_limit_field", AuthorLLMOutputMaxCompletionTokens+" or "+AuthorLLMOutputMaxTokens)
	}
	return nil
}

// Reasons the author layer's LLM is off, as the admin card shows them.
const (
	// AuthorLLMDisabledNotConfigured: the shared llm section allows no
	// requests (no base URL, or the public OpenAI API without a key).
	AuthorLLMDisabledNotConfigured = "llm_not_configured"
	// AuthorLLMDisabledModels: the participants are not four distinct,
	// non-empty models with known parameters.
	AuthorLLMDisabledModels = "llm_participants_invalid"
)

// AuthorLLMStatus says whether the author layer may call its models, and
// why not.
type AuthorLLMStatus struct {
	Enabled bool
	Reason  string
}

// AuthorLLMReasoningEfforts is the closed set of reasoning effort values;
// llmreq.ReasoningEfforts returns this list.
func AuthorLLMReasoningEfforts() []string {
	return []string{"", "minimal", "low", "medium", AuthorLLMEffortHigh, "xhigh"}
}

// AuthorLLMEffortHigh is the reasoning effort a judge usually runs at.
const AuthorLLMEffortHigh = "high"

// Status decides whether the author layer is on: the shared section must
// allow requests, and the four participants must be four distinct trimmed
// model ids with known parameters. It never fails the server: a bad setting
// leaves the review queue working as it does without the LLM.
func (c *AuthorLLMConfig) Status(shared LLMConfig) AuthorLLMStatus {
	if !shared.Configured() {
		return AuthorLLMStatus{Reason: AuthorLLMDisabledNotConfigured}
	}
	models := []string{c.Models.ProductionA, c.Models.ProductionB, c.Models.JudgeA, c.Models.JudgeB}
	seen := map[string]bool{}
	for _, m := range models {
		if m == "" || strings.TrimSpace(m) != m || seen[m] {
			return AuthorLLMStatus{Reason: AuthorLLMDisabledModels}
		}
		seen[m] = true
	}
	for _, p := range []AuthorLLMParams{c.Params.ProductionA, c.Params.ProductionB, c.Params.JudgeA, c.Params.JudgeB} {
		if !slices.Contains(AuthorLLMReasoningEfforts(), p.ReasoningEffort) ||
			(p.Temperature != nil && (*p.Temperature < 0 || *p.Temperature > 2)) {
			return AuthorLLMStatus{Reason: AuthorLLMDisabledModels}
		}
	}
	return AuthorLLMStatus{Enabled: true}
}
