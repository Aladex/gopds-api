package llmreq

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gopds-api/internal/authornorm/llmres"
)

// ItemInput is the stored input of one job: the source name fields in
// document order, the script and the quality flags the local normalizer
// observed. Tokens are not stored: they are derived from the fields by
// llmres, so the validators never trust a stored token list.
type ItemInput struct {
	Fields []llmres.FieldValue `json:"fields"`
	Script string              `json:"script"`
	Flags  []string            `json:"flags"`
}

// Item is one input of a batch request. Key is the item's id in the request
// and the reply; Context is the optional excerpt.
type Item struct {
	Key     string
	Input   ItemInput
	Context *llmres.BookContext
}

// wireToken is a token as the request carries it.
type wireToken struct {
	I     int    `json:"i"`
	Text  string `json:"text"`
	Field string `json:"field"`
}

// wireItem is one item as the request carries it.
type wireItem struct {
	ID          string              `json:"id"`
	Tokens      []wireToken         `json:"tokens"`
	Script      string              `json:"script"`
	Flags       []string            `json:"flags"`
	BookContext *llmres.BookContext `json:"book_context"`
}

// Tokens returns the request tokens of an input, refusing an input over the
// request limits (llmres.ErrInputTooLong).
func (in ItemInput) Tokens() ([]llmres.Token, error) {
	parsed, err := llmres.NewInput(in.Fields)
	if err != nil {
		return nil, err
	}
	if err := llmres.CheckLimits(parsed.Tokens); err != nil {
		return nil, err
	}
	return parsed.Tokens, nil
}

// BuildUserMessage serializes a batch: {"items": [...]} in the given order.
// Keys must be non-empty and distinct; an input over the request limits is
// refused, so nothing too long is ever sent.
func BuildUserMessage(items []Item) ([]byte, error) {
	if len(items) == 0 {
		return nil, errors.New("llmreq: empty batch")
	}
	seen := make(map[string]bool, len(items))
	wire := make([]wireItem, 0, len(items))
	for _, it := range items {
		if it.Key == "" || seen[it.Key] {
			return nil, fmt.Errorf("llmreq: item key %q is empty or repeated", it.Key)
		}
		seen[it.Key] = true
		tokens, err := it.Input.Tokens()
		if err != nil {
			return nil, err
		}
		w := wireItem{ID: it.Key, Script: it.Input.Script, Flags: it.Input.Flags, BookContext: it.Context}
		if w.Flags == nil {
			w.Flags = []string{}
		}
		for _, t := range tokens {
			w.Tokens = append(w.Tokens, wireToken{I: t.I, Text: t.Text, Field: string(t.Field)})
		}
		wire = append(wire, w)
	}
	return json.Marshal(struct {
		Items []wireItem `json:"items"`
	}{wire})
}

// ItemErrorClass is why one item of a batch reply has no usable answer.
type ItemErrorClass string

const (
	// ItemMissing: the reply has no result with the item's id.
	ItemMissing ItemErrorClass = "missing"
	// ItemDuplicate: the reply has more than one result with the item's id.
	ItemDuplicate ItemErrorClass = "duplicate"
	// ItemInvalid: the item's result fails the strict reply parsing (V1).
	ItemInvalid ItemErrorClass = "invalid"
)

// ItemError is the per-item failure of a batch reply.
type ItemError struct {
	Class ItemErrorClass
	// Cause is the V1 violation of an invalid item.
	Cause error
}

func (e *ItemError) Error() string { return "llmreq: batch item " + string(e.Class) }

func (e *ItemError) Unwrap() error { return e.Cause }

// ItemReply is one item of a split batch reply: a strictly parsed reply, or
// the reason there is none.
type ItemReply struct {
	Reply *llmres.Reply
	Err   error
}

// ErrMalformedBatch marks a reply that is not the batch envelope at all: no
// item can be attributed, so every item of the batch is retried.
var ErrMalformedBatch = errors.New("llmreq: reply is not the batch envelope")

// SplitReply splits a batch reply into per-item replies, one per key. The
// envelope is parsed strictly — exactly {"results": [...]}, nothing before or
// after, which also refuses a Markdown fence. Each result is attributed by its
// string id and parsed by llmres.ParseReply without the id; a missing,
// repeated or unparsable result invalidates only its own item, and a result
// with an unknown or absent id is ignored.
func SplitReply(content []byte, keys []string) (map[string]ItemReply, error) {
	var envelope struct {
		Results *[]json.RawMessage `json:"results"`
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil || envelope.Results == nil {
		return nil, ErrMalformedBatch
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, ErrMalformedBatch
	}

	wanted := make(map[string]bool, len(keys))
	for _, k := range keys {
		wanted[k] = true
	}
	raws := make(map[string][]json.RawMessage, len(keys))
	for _, raw := range *envelope.Results {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		var id string
		if json.Unmarshal(fields["id"], &id) != nil || !wanted[id] {
			continue
		}
		delete(fields, "id")
		rest, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		raws[id] = append(raws[id], rest)
	}

	out := make(map[string]ItemReply, len(keys))
	for _, k := range keys {
		switch found := raws[k]; len(found) {
		case 0:
			out[k] = ItemReply{Err: &ItemError{Class: ItemMissing}}
		case 1:
			rep, err := llmres.ParseReply(found[0])
			if err != nil {
				out[k] = ItemReply{Err: &ItemError{Class: ItemInvalid, Cause: err}}
				continue
			}
			out[k] = ItemReply{Reply: &rep}
		default:
			out[k] = ItemReply{Err: &ItemError{Class: ItemDuplicate}}
		}
	}
	return out, nil
}

// batchSchema is built once from llmres.ReplySchema: the single-item reply
// with an "id" property, wrapped as {"results": [item, ...]}, with the $defs
// hoisted to the root where the item's $refs resolve.
var batchSchema = mustBatchSchema()

func mustBatchSchema() []byte {
	var item map[string]any
	if err := json.Unmarshal([]byte(llmres.ReplySchema), &item); err != nil {
		panic(err)
	}
	defs := item["$defs"]
	delete(item, "$defs")
	delete(item, "$schema")
	const typeKey, idKey, resultsKey = "type", "id", "results"
	props := item["properties"].(map[string]any)
	props[idKey] = map[string]any{typeKey: "string"}
	item["required"] = append([]any{idKey}, item["required"].([]any)...)
	schema := map[string]any{
		typeKey:                "object",
		"additionalProperties": false,
		"required":             []any{resultsKey},
		"properties": map[string]any{
			resultsKey: map[string]any{typeKey: "array", "items": item},
		},
		"$defs": defs,
	}
	out, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return out
}

// BatchSchema is the strict JSON schema of a batch reply, sent as the
// response format or as the tool's parameters. The bytes are deterministic.
func BatchSchema() []byte { return bytes.Clone(batchSchema) }

// SchemaSHA256 is the digest of the batch schema.
func SchemaSHA256() [32]byte { return sha256.Sum256(batchSchema) }

// SchemaName names the schema in the response format and the tool.
const SchemaName = "submit_results"

// FingerprintItem is the fixed one-item request of the tokenizer fingerprint
// check: the same messages every time, so the endpoint's prompt token count
// for it is a property of the model's tokenizer (and of whatever the endpoint
// adds to the prompt), not of the data.
func FingerprintItem() Item {
	return Item{
		Key: "1",
		Input: ItemInput{
			Fields: []llmres.FieldValue{
				{Field: llmres.FieldFirst, Text: "Сергей Петрович"},
				{Field: llmres.FieldLast, Text: "Иванов-Смирнов"},
			},
			Script: fingerprintScript,
			Flags:  []string{},
		},
	}
}

// fingerprintScript is the script of the fingerprint item's name.
const fingerprintScript = "Cyrl"
