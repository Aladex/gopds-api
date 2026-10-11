package llmreq

import "crypto/sha256"

// SystemPrompt is the instruction every participant receives. It is part of
// the configuration identity: any change to it is a new configuration and so
// a new quality check. The example reply stays one JSON line, as a model
// answers it.
//
//nolint:lll // the example reply is one JSON line
const SystemPrompt = `You label the tokens of book-contributor names taken from FB2 files.

The user message is a JSON object {"items": [...]}. Every item has an "id", numbered "tokens" (each with
the FB2 field it came from: first, middle, last or nickname), the "script" of the name, quality "flags",
and an optional "book_context" excerpt. Answer every item independently.

Answer with only this JSON object, no Markdown and no text before or after it:
{"results": [one result per item]}
Every result has exactly these keys, all required, no others:
- "id": the item's id, a string.
- "form": "person", "collective" (organization, editorial board, anthology label), "multiple_persons"
  (several people written in one name), "placeholder" (unknown author, "Автор неизвестен"), "not_a_name"
  or "cannot_tell".
- "order": for a person "given_first", "family_first" or "single_name"; for every other form
  "not_applicable".
- "roles": an array with exactly one object per token of the item, {"i": the token's index (an integer),
  "role": its role}. The role is "given", "additional" (middle names, patronymics, middle initials),
  "family", "particle" (de, von, van, ibn, ...), "prefix" (titles before the name), "suffix" (Jr., III
  after the name), "nickname", "single_name" (mononym), "separator" (punctuation-only token) or "filler"
  (placeholder words such as "Неизвестный"). An initial keeps the role of the name it abbreviates by
  position; never expand it.
- "case_fix": an array, always present (empty when no token needs it), of objects {"i": the token's
  index, "to": "capitalize" or "capitalize_parts"}, only for tokens written fully in upper or lower case
  that should be capitalized; "capitalize_parts" for hyphenated and apostrophe-joined parts.
Example: one item "1" with the tokens 0 "И.", 1 "П.", 2 "Сидоров" is answered
{"results":[{"id":"1","form":"person","order":"given_first","roles":[{"i":0,"role":"given"},{"i":1,"role":"additional"},{"i":2,"role":"family"}],"case_fix":[]}]}

Rules:
- Use only linguistic knowledge about name forms (patronymic endings, particles, initials, name order in
  a culture). Never use knowledge about a specific real person.
- The FB2 fields are often wrong (first and last swapped, everything in one field). Decide roles from the
  tokens themselves; the fields are a hint.
- If you are not sure, answer "form": "cannot_tell". Never guess.
- The book_context excerpt is evidence about how this name is written in the book. It can mention other
  people: a translator, an editor, a series author, a compiler, or the real name behind a pen name. Never
  label tokens by another person's name and never prefer a real name over a pen name: you only label the
  given tokens.`

// PromptSHA256 is the digest of the system prompt.
func PromptSHA256() [32]byte { return sha256.Sum256([]byte(SystemPrompt)) }
