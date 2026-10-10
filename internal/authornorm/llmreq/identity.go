package llmreq

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"gopds-api/config"
	"gopds-api/internal/authornorm/llmres"
)

// Versions of the pieces of llmres the answers depend on (not credentials). llmres carries the
// excerpt reader's version itself; the validators and the tokenizer are
// versioned here, and a change to either must bump its constant.
const (
	ValidatorVersion = "authornorm-llmres-validators-v1"
	// #nosec G101 -- a version label of the tokenizer, not a credential
	TokenizerVersion = "authornorm-llmres-tokens-v1"
)

// VersionPrefix starts every configuration version.
const VersionPrefix = "llm1-"

// Slot is a participant's fixed role.
type Slot string

const (
	SlotProductionA Slot = "production_a"
	SlotProductionB Slot = "production_b"
	SlotJudgeA      Slot = "judge_a"
	SlotJudgeB      Slot = "judge_b"
)

// Slots lists the four participants in their fixed order.
func Slots() []Slot { return []Slot{SlotProductionA, SlotProductionB, SlotJudgeA, SlotJudgeB} }

// Valid reports whether s is one of the four slots.
func (s Slot) Valid() bool { return slices.Contains(Slots(), s) }

// ReasoningEfforts is the closed set of reasoning effort values, the
// configuration's own; empty means the request carries none.
func ReasoningEfforts() []string { return config.AuthorLLMReasoningEfforts() }

// Participant is one model of a configuration with its request parameters.
// Temperature nil means the request carries none.
type Participant struct {
	Slot            Slot     `json:"slot"`
	Model           string   `json:"model"`
	Temperature     *float64 `json:"temperature,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
}

// Identity is everything an answer depends on: where the requests go, which
// models with which parameters, and the prompt, schema, validators, tokenizer
// and excerpt reader. It never holds a key. Its hash is the configuration
// version; concurrency, limits and timeouts do not change answers and are not
// part of it.
type Identity struct {
	BaseURL          string        `json:"base_url"`
	Participants     []Participant `json:"participants"`
	Output           OutputLimits  `json:"output"`
	PromptSHA256     string        `json:"prompt_sha256"`
	SchemaSHA256     string        `json:"schema_sha256"`
	ValidatorVersion string        `json:"validator_version"`
	TokenizerVersion string        `json:"tokenizer_version"`
	ContextVersion   string        `json:"context_version"`
}

// NormalizeBaseURL reduces an endpoint to what identifies it: http or https,
// lower-case scheme and host, the path without a trailing slash. User info,
// query and fragment are dropped — they can carry a credential and never
// name a different endpoint.
func NormalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("llmreq: base URL does not parse")
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" {
		return "", errors.New("llmreq: base URL must be an absolute http or https URL")
	}
	clean := url.URL{Scheme: scheme, Host: strings.ToLower(u.Host), Path: strings.TrimRight(u.Path, "/")}
	return clean.String(), nil
}

// NewIdentity builds the identity of a configuration from its endpoint and
// its four participants (in any order), with this build's prompt, schema and
// versions. It refuses anything but exactly the four slots, each with a
// non-empty trimmed model, all four models distinct, and parameters inside
// their closed ranges.
func NewIdentity(baseURL string, participants []Participant, output OutputLimits) (Identity, error) {
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return Identity{}, err
	}
	if outErr := output.validate(); outErr != nil {
		return Identity{}, outErr
	}
	if len(participants) != len(Slots()) {
		return Identity{}, errors.New("llmreq: a configuration has exactly four participants")
	}
	bySlot := make(map[Slot]Participant, len(participants))
	models := make(map[string]bool, len(participants))
	for _, p := range participants {
		if !p.Slot.Valid() {
			return Identity{}, fmt.Errorf("llmreq: unknown slot %q", p.Slot)
		}
		if _, dup := bySlot[p.Slot]; dup {
			return Identity{}, fmt.Errorf("llmreq: slot %s given twice", p.Slot)
		}
		if p.Model == "" || strings.TrimSpace(p.Model) != p.Model {
			return Identity{}, fmt.Errorf("llmreq: slot %s needs a model id without surrounding space", p.Slot)
		}
		if models[p.Model] {
			return Identity{}, fmt.Errorf("llmreq: slot %s repeats another participant's model", p.Slot)
		}
		if !slices.Contains(ReasoningEfforts(), p.ReasoningEffort) {
			return Identity{}, fmt.Errorf("llmreq: slot %s has an unknown reasoning effort", p.Slot)
		}
		if p.Temperature != nil && (*p.Temperature < 0 || *p.Temperature > 2) {
			return Identity{}, fmt.Errorf("llmreq: slot %s temperature is outside 0..2", p.Slot)
		}
		models[p.Model] = true
		bySlot[p.Slot] = p
	}
	ordered := make([]Participant, 0, len(participants))
	for _, s := range Slots() {
		ordered = append(ordered, bySlot[s])
	}
	prompt, schema := PromptSHA256(), SchemaSHA256()
	return Identity{
		BaseURL:          base,
		Participants:     ordered,
		Output:           output,
		PromptSHA256:     hex.EncodeToString(prompt[:]),
		SchemaSHA256:     hex.EncodeToString(schema[:]),
		ValidatorVersion: ValidatorVersion,
		TokenizerVersion: TokenizerVersion,
		ContextVersion:   llmres.ContextVersion,
	}, nil
}

// Participant returns the participant of a slot.
func (id *Identity) Participant(s Slot) (Participant, bool) {
	for _, p := range id.Participants {
		if p.Slot == s {
			return p, true
		}
	}
	return Participant{}, false
}

// CanonicalJSON is the identity's canonical serialization, the input of its
// version.
func (id *Identity) CanonicalJSON() []byte {
	out, err := json.Marshal(id)
	if err != nil {
		panic(err) // the identity shape is always marshalable
	}
	return out
}

// Version is the configuration version: the prefix and the hex SHA-256 of
// the canonical serialization.
func (id *Identity) Version() string {
	sum := sha256.Sum256(id.CanonicalJSON())
	return VersionPrefix + hex.EncodeToString(sum[:])
}
