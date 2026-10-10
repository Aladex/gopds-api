// Package llmreq is the request side of the LLM author layer: the system
// prompt, the batch request every participant receives, the batch reply
// schema and its per-item split, the fixed fingerprint request, and the
// identity of a configuration (endpoint, participants, prompt and schema)
// whose hash is the configuration version.
//
// The package is pure. It builds on llmres — tokens, the single-item reply
// schema and its strict parser — and adds nothing to it: a batch reply is
// split into single-item replies that llmres parses exactly as before.
package llmreq
