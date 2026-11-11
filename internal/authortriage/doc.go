// Package authortriage is the rule set that decides open author review items
// in admin-triggered batches (design/review-triage). It is pure: the
// database layer loads the facts of each item — the stored model answers, the
// entity build's read of the fingerprint, the catalog's known given names —
// and Decide returns what to do with it, or why it is left or excluded.
//
// The rules are versioned (RuleVersion). A batch is planned, stored and
// applied under one version, and a change to anything here that can change a
// decision is a new version: the audit (audit.go) is of one version's
// decisions.
//
// The cascade runs in step order and the first step that decides an item
// wins:
//
//  1. four_model: the production pair and both judges gave one answer;
//  2. judges_only: both judges did, the pair did not;
//  3. rules: the build's case, look-alike and nickname reads;
//  4. same_name: the one structure of the same-name entity the build joined
//     the name to;
//  5. placeholder_org: a placeholder phrase is unknown, an organization's
//     name a collective;
//  6. as_named_collective: a collective candidate the build reads as named;
//  7. keep_handle: a one-word handle no one knows as a given name;
//  8. particle_only: a proposal whose only doubt is a name particle.
//
// A step decides only through Accept, Edit or Classify (person, collective or
// unknown): never malformed, never leave unresolved, never retry.
package authortriage
