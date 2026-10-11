package llmres

import (
	"bytes"
	"encoding/json"
	"slices"
)

// The canonical answer of design doc section 2: the tuple (form, roles[],
// case_fix[]) with the lists sorted by index, serialized deterministically.
// The order is not in it: it follows from the roles. A cannot_tell answer is
// its form alone — a refusal whose roles mean nothing, so two refusals agree
// whatever roles they guessed. Two models agree iff the canonical forms are
// byte-equal; there is no partial agreement.

// canonicalWire is the canonical serialization shape: fixed field order,
// sorted lists, empty lists instead of null.
type canonicalWire struct {
	Form    Form             `json:"form"`
	Roles   []RoleAssignment `json:"roles"`
	CaseFix []CaseFix        `json:"case_fix"`
}

// CanonicalBytes returns the deterministic serialization of the reply's
// canonical form.
func (r *Reply) CanonicalBytes() []byte {
	roles := slices.Clone(r.Roles)
	slices.SortStableFunc(roles, func(a, b RoleAssignment) int { return a.I - b.I })
	fixes := slices.Clone(r.CaseFix)
	slices.SortStableFunc(fixes, func(a, b CaseFix) int { return a.I - b.I })
	if roles == nil || r.Form == FormCannotTell {
		roles = []RoleAssignment{}
	}
	if fixes == nil || r.Form == FormCannotTell {
		fixes = []CaseFix{}
	}
	out, err := json.Marshal(canonicalWire{Form: r.Form, Roles: roles, CaseFix: fixes})
	if err != nil {
		// The canonical wire is always marshalable; a failure is a bug.
		panic(err)
	}
	return out
}

// Agree reports the agreement of design doc section 2: byte-equality of the
// canonical forms.
func Agree(a, b *Reply) bool {
	return bytes.Equal(a.CanonicalBytes(), b.CanonicalBytes())
}
