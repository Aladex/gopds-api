package llmreq

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"gopds-api/internal/authorentity"
)

// The merge question (E4c, plan §4.3): two cards — the names and sample
// books of two author records — and a closed answer, one person or not. It
// is its own configuration (NewMergeIdentity): its prompt, schema and
// validator are hashed into a version that never meets the normalization's.

// MergeValidatorVersion versions the merge reply validator; a change to what
// it accepts must bump it.
const MergeValidatorVersion = "e4-merge-validator-v1"

// The closed answers of the merge question.
const (
	MergeSame       = "same"
	MergeDifferent  = "different"
	MergeCannotTell = "cannot_tell"
)

// MergeAnswers lists the closed answers.
func MergeAnswers() []string { return []string{MergeSame, MergeDifferent, MergeCannotTell} }

// MergeSystemPrompt is the instruction every participant of the merge
// configuration receives; it is part of the identity.
const MergeSystemPrompt = `You decide whether two author records of an e-book catalog are one real person.

The user message is a JSON object {"cards": {...}, "items": [...]}. "cards" maps a card id to a card; every item has an
"id" and two card ids, "a" and "b". A card describes one author record: "names" (how the record's credits are
displayed), "variants" (pen names or nicknames written beside them), "size" (how many books the record has, as a class:
1, 2-3, 4-10, 11-30, 31-100, 100+) and up to 10 "books", the oldest in the catalog, each with its title, year,
language, genre codes, series, an annotation cut to 300 characters and the other authors of the book ("coauthors"). A
title marked "samizdat" is self-published. One card can be shared by several items. Answer every item independently.

Answer with only this JSON object, no Markdown and no text before or after it:
{"results": [one result per item]}
Every result has exactly these keys, all required, no others:
- "id": the item's id, a string.
- "answer": "same", "different" or "cannot_tell".
Example: {"results":[{"id":"1","answer":"different"}]}

Rules:
- "same" means one person whose name is written differently: the parts in another order, split into other fields,
  another spelling or transliteration, initials instead of a full name, another script.
- A pen name and the real name of one person are "different": the catalog keeps them apart.
- Two people with the same or a similar name are "different". A similar name alone is never enough for "same": the
  books must fit one author (their themes, genres, series, co-authors, languages and years).
- If the cards do not let you decide, answer "cannot_tell". Never guess.`

// MergePromptSHA256 is the digest of the merge system prompt.
func MergePromptSHA256() [32]byte { return sha256.Sum256([]byte(MergeSystemPrompt)) }

// mergeSchema is the strict JSON schema of a merge batch reply.
var mergeSchema = mustMergeSchema()

func mustMergeSchema() []byte {
	const typeKey, propsKey, closedKey, requiredKey = "type", "properties", "additionalProperties", "required"
	const objectType, stringType, arrayType = "object", "string", "array"
	const resultsKey, itemsKey, idKey, answerKey = "results", "items", "id", "answer"
	answers := make([]any, 0, len(MergeAnswers()))
	for _, a := range MergeAnswers() {
		answers = append(answers, a)
	}
	item := map[string]any{
		typeKey: objectType, closedKey: false, requiredKey: []any{idKey, answerKey},
		propsKey: map[string]any{
			idKey:     map[string]any{typeKey: stringType},
			answerKey: map[string]any{typeKey: stringType, "enum": answers},
		},
	}
	schema := map[string]any{
		typeKey: objectType, closedKey: false, requiredKey: []any{resultsKey},
		propsKey: map[string]any{resultsKey: map[string]any{typeKey: arrayType, itemsKey: item}},
	}
	out, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return out
}

// MergeSchema is the strict JSON schema of a merge batch reply; the bytes
// are deterministic.
func MergeSchema() []byte { return bytes.Clone(mergeSchema) }

// MergeSchemaSHA256 is the digest of the merge schema.
func MergeSchemaSHA256() [32]byte { return sha256.Sum256(mergeSchema) }

// MergeItem is one question of a merge batch: Key is its id in the request
// and the reply, Input the exact two cards (authorentity.MergeInputJSON).
type MergeItem struct {
	Key   string
	Input authorentity.MergeInput
}

type mergeWireItem struct {
	ID string `json:"id"`
	A  string `json:"a"`
	B  string `json:"b"`
}

// BuildMergeUserMessage serializes a merge batch: every distinct card once,
// keyed c1, c2, ... in the order the items first name it, and the items in
// the given order naming their cards. A bucket batch whose books share the
// full name's card sends that card once. Keys must be non-empty and
// distinct, and every item's card version this build's.
func BuildMergeUserMessage(items []MergeItem) ([]byte, error) {
	if len(items) == 0 {
		return nil, errors.New("llmreq: empty batch")
	}
	seen := make(map[string]bool, len(items))
	cards := map[string]json.RawMessage{}
	ids := map[string]string{}
	wire := make([]mergeWireItem, 0, len(items))
	cardID := func(c *authorentity.MergeCard) (string, error) {
		raw, err := json.Marshal(c)
		if err != nil {
			return "", err
		}
		if id, ok := ids[string(raw)]; ok {
			return id, nil
		}
		id := "c" + strconv.Itoa(len(ids)+1)
		ids[string(raw)], cards[id] = id, raw
		return id, nil
	}
	for i := range items {
		it := &items[i]
		if it.Key == "" || seen[it.Key] {
			return nil, fmt.Errorf("llmreq: item key %q is empty or repeated", it.Key)
		}
		seen[it.Key] = true
		if it.Input.CardVersion != authorentity.MergeCardVersion {
			return nil, errors.New("llmreq: a merge item of another card version")
		}
		a, err := cardID(&it.Input.A)
		if err != nil {
			return nil, err
		}
		b, err := cardID(&it.Input.B)
		if err != nil {
			return nil, err
		}
		wire = append(wire, mergeWireItem{ID: it.Key, A: a, B: b})
	}
	return json.Marshal(struct {
		Cards map[string]json.RawMessage `json:"cards"`
		Items []mergeWireItem            `json:"items"`
	}{cards, wire})
}

// MergeItemReply is one item of a split merge reply: the answer, or why
// there is none.
type MergeItemReply struct {
	Answer string
	Err    error
}

// MergeCanonical is the canonical bytes of an answer, the reply stored.
func MergeCanonical(answer string) []byte {
	out, err := json.Marshal(struct {
		Answer string `json:"answer"`
	}{answer})
	if err != nil {
		panic(err)
	}
	return out
}

// SplitMergeReply splits a merge batch reply into per-item answers, one per
// key, as strictly as SplitReply: exactly {"results": [...]} (one Markdown
// fence tolerated); each result exactly {"id", "answer"} with a closed
// answer. A missing, repeated or invalid result invalidates only its item; a
// result with an unknown or absent id is ignored.
func SplitMergeReply(content []byte, keys []string) (map[string]MergeItemReply, error) {
	var envelope struct {
		Results *[]json.RawMessage `json:"results"`
	}
	dec := json.NewDecoder(bytes.NewReader(stripFence(content)))
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
	found := make(map[string][]json.RawMessage, len(keys))
	for _, raw := range *envelope.Results {
		var probe struct {
			ID *string `json:"id"`
		}
		if json.Unmarshal(raw, &probe) != nil || probe.ID == nil || !wanted[*probe.ID] {
			continue
		}
		found[*probe.ID] = append(found[*probe.ID], raw)
	}
	out := make(map[string]MergeItemReply, len(keys))
	for _, k := range keys {
		switch raws := found[k]; len(raws) {
		case 0:
			out[k] = MergeItemReply{Err: &ItemError{Class: ItemMissing}}
		case 1:
			answer, err := parseMergeResult(raws[0])
			if err != nil {
				out[k] = MergeItemReply{Err: &ItemError{Class: ItemInvalid, Cause: err}}
				continue
			}
			out[k] = MergeItemReply{Answer: answer}
		default:
			out[k] = MergeItemReply{Err: &ItemError{Class: ItemDuplicate}}
		}
	}
	return out, nil
}

// errMergeAnswer is a result that is not exactly {"id", "answer"} with a
// closed answer.
var errMergeAnswer = errors.New("llmreq: a merge result is not one closed answer")

func parseMergeResult(raw json.RawMessage) (string, error) {
	var r struct {
		ID     *string `json:"id"`
		Answer *string `json:"answer"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&r) != nil || r.ID == nil || r.Answer == nil {
		return "", errMergeAnswer
	}
	for _, a := range MergeAnswers() {
		if *r.Answer == a {
			return a, nil
		}
	}
	return "", errMergeAnswer
}

// MergeItemMaxBytes bounds one merge item: two cards of at most 10 books
// with their annotations cut to 300 characters, Cyrillic at two bytes a
// rune, with the item wiring. A request over its bound is not sent.
const MergeItemMaxBytes = 32768

// MergeRequestBoundBytes is the most bytes of messages and schema a merge
// request of items items may send.
func MergeRequestBoundBytes(items, _ int) int {
	return len(MergeSystemPrompt) + len(mergeSchema) + envelopeBytes + items*MergeItemMaxBytes
}

// MergeRequestBytes is what MergeRequestBoundBytes bounds.
func MergeRequestBytes(user []byte) int {
	return len(MergeSystemPrompt) + len(user) + len(mergeSchema)
}

// MergeFingerprintItem is the fixed one-item request of the merge
// configuration's tokenizer fingerprint check: the same messages every time.
func MergeFingerprintItem() MergeItem {
	book := authorentity.MergeCardBook{Title: "Ночной дозор", Year: "1998", Lang: "ru", Genres: []string{"sf_fantasy"}}
	return MergeItem{Key: "1", Input: authorentity.MergeInput{
		CardVersion: authorentity.MergeCardVersion,
		A:           authorentity.MergeCard{Names: []string{"Сергей Лукьяненко"}, Size: "1", Books: []authorentity.MergeCardBook{book}},
		B:           authorentity.MergeCard{Names: []string{"Лукьяненко Сергей"}, Size: "1", Books: []authorentity.MergeCardBook{book}},
	}}
}
