// Package llmres is the pure core of the LLM author-parsing layer (design doc
// section 2): the tokenizer of source display names, the strict JSON schema
// of the model reply, the deterministic validators V0-V7, canonicalization
// and cross-model agreement, assembly of an authornorm.Result from source
// tokens and roles, the tier computation, the homoglyph repair rule, and the
// excerpt reader authornorm-context-v1. It imports no network or database
// code; every output is a deterministic function of its inputs.
package llmres
