package llmreq

import (
	"errors"
	"slices"

	"gopds-api/config"
)

// The hard bound of one request, in tokens: what the claim reserves before
// anything is sent, so the reserve covers whatever the call can cost.
//
//   - The prompt side is bounded by bytes. The tokenizers behind the endpoint
//     encode bytes (byte-level BPE): a token covers at least one byte, so the
//     prompt tokens of a request are at most the bytes of its messages and
//     schema plus a fixed allowance for the chat template, role markers and
//     the rendering of the tool or schema definition. The worker measures the
//     request it built and does not send one over the bound it reserved.
//   - The output side is bounded by the request itself: it carries the
//     ceiling (max_completion_tokens or max_tokens), reasoning included.
//
// An endpoint that reports more than the reserve broke one of the two
// assumptions (it ignored the ceiling, or its tokenizer is not byte-level);
// the settle then pauses the participant, so the overrun is at most the calls
// already in flight.

// Allowances of the prompt bound.
const (
	// PromptTemplateTokens covers what the endpoint adds around the messages.
	PromptTemplateTokens = 1024
	// envelopeBytes covers the batch envelope and the per-message wrapping.
	envelopeBytes = 2048
	// ItemMaxBytes bounds one item without an excerpt: at most 200 code
	// points of name in at most 16 tokens, each escaped to at most 6 bytes,
	// with the token, script and flag wrapping.
	ItemMaxBytes = 4096
	// ExcerptMaxBytes bounds one item's excerpt (3 600 runes of text and the
	// field and credit wrapping, at Cyrillic's two bytes per rune with room
	// for escapes). A request that still exceeds its bound is not sent.
	ExcerptMaxBytes = 16384
)

// RequestBoundBytes is the most bytes of messages and schema a request with
// items items, withContext of them carrying an excerpt, may send.
func RequestBoundBytes(items, withContext int) int {
	return len(SystemPrompt) + len(batchSchema) + envelopeBytes + items*ItemMaxBytes + withContext*ExcerptMaxBytes
}

// RequestBytes is what RequestBoundBytes bounds: the bytes of the system and
// user messages and of the schema.
func RequestBytes(user []byte) int {
	return len(SystemPrompt) + len(user) + len(batchSchema)
}

// The request fields that carry the output ceiling. Which one an endpoint
// honors is a property of the endpoint and its models, so it is part of the
// configuration.
const (
	OutputFieldMaxCompletionTokens = "max_completion_tokens"
	OutputFieldMaxTokens           = "max_tokens"
)

// OutputLimits is the output ceiling of a request: PerCall plus PerItem for
// every item, reasoning included, sent in Field. A truncated reply is an
// invalid one, so the limits change answers and belong to the identity.
type OutputLimits struct {
	Field   string `json:"field"`
	PerCall int    `json:"per_call_tokens"`
	PerItem int    `json:"per_item_tokens"`
}

// Ceiling is the output ceiling of a request with items items.
func (o OutputLimits) Ceiling(items int) int { return o.PerCall + items*o.PerItem }

// Reserve is the hard bound of a request, in tokens: its prompt bound and its
// output ceiling.
func (o OutputLimits) Reserve(items, withContext int) int64 {
	return int64(RequestBoundBytes(items, withContext)) + PromptTemplateTokens + int64(o.Ceiling(items))
}

func (o OutputLimits) validate() error {
	if !slices.Contains([]string{OutputFieldMaxCompletionTokens, OutputFieldMaxTokens}, o.Field) {
		return errors.New("llmreq: unknown output limit field")
	}
	if o.PerCall < 0 || o.PerItem < 1 {
		return errors.New("llmreq: output limits must be positive")
	}
	if !config.AuthorLLMOutputCeilingFits(o.PerCall, o.PerItem) {
		return errors.New("llmreq: the output ceiling of a full batch exceeds its maximum")
	}
	return nil
}
