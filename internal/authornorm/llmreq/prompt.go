package llmreq

import "crypto/sha256"

// SystemPrompt is the instruction every participant receives. It is part of
// the configuration identity: any change to it is a new configuration and so
// a new quality check.
const SystemPrompt = `You label the tokens of book-contributor names taken from FB2 files.

The user message is a JSON object {"items": [...]}. Every item has an "id", numbered "tokens" (each with
the FB2 field it came from: first, middle, last or nickname), the "script" of the name, quality "flags",
and an optional "book_context" excerpt. Answer every item independently and return only the JSON object
required by the schema: {"results": [...]}, one result per item, each with the item's "id".

form: person | collective (organization, editorial board, anthology label) | multiple_persons (several
people written in one name) | placeholder (unknown author, "Автор неизвестен") | not_a_name | cannot_tell.
order (person only): given_first | family_first | single_name; for every other form: not_applicable.
roles: exactly one entry per token index of the item.
  given, additional (middle names, patronymics, middle initials), family, particle (de, von, van, ibn,
  ...), prefix (titles before the name), suffix (Jr., III after the name), nickname, single_name
  (mononym), separator (punctuation-only token), filler (placeholder words such as "Неизвестный").
  An initial keeps the role of the name it abbreviates by position; never expand it.
case_fix: only for tokens written fully in upper or lower case that should be capitalized:
  capitalize, or capitalize_parts for hyphenated and apostrophe-joined parts.

Rules:
- Use only linguistic knowledge about name forms (patronymic endings, particles, initials, name order in
  a culture). Never use knowledge about a specific real person.
- The FB2 fields are often wrong (first and last swapped, everything in one field). Decide roles from the
  tokens themselves; the fields are a hint.
- If you are not sure, answer form=cannot_tell. Never guess.
- The book_context excerpt is evidence about how this name is written in the book. It can mention other
  people: a translator, an editor, a series author, a compiler, or the real name behind a pen name. Never
  label tokens by another person's name and never prefer a real name over a pen name: you only label the
  given tokens.`

// PromptSHA256 is the digest of the system prompt.
func PromptSHA256() [32]byte { return sha256.Sum256([]byte(SystemPrompt)) }
