// Package authornorm holds the pure domain contract of the author-metadata
// source layer: canonical source values, reproducible fingerprints and the
// versioned decision vocabulary. It imports no database, HTTP or parser code.
package authornorm

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// ComponentKind identifies which name-bearing child of a contributor element a
// source component came from.
type ComponentKind int

const (
	ComponentFirst ComponentKind = iota
	ComponentMiddle
	ComponentLast
	ComponentNickname
)

func (k ComponentKind) valid() bool {
	return k >= ComponentFirst && k <= ComponentNickname
}

// SourceComponent is one present name-bearing XML child of a contributor
// element, in original document order. An absent element produces no
// component; a present-but-empty element produces a component whose value is
// empty after canonicalization, and the two cases stay distinguishable.
type SourceComponent struct {
	Kind  ComponentKind
	Value string
}

// SourceValue is the canonical, immutable source payload of one contributor
// credit. The only admitted transformations over the raw element text are the
// structural XML-whitespace trim and NFC; case, punctuation, diacritics and
// the original child order are preserved.
type SourceValue struct {
	first    *string
	middle   *string
	last     *string
	nickname *string

	displayName        string
	duplicateComponent bool
}

// canonicalize applies the only transformations admitted before a source
// string is stored or fingerprinted (contract 3.2): trim structural XML
// whitespace — U+0009, U+000A, U+000D, U+0020 — from the edges, then NFC.
// NBSP and every other Unicode space stay untouched.
func canonicalize(raw string) string {
	return norm.NFC.String(strings.Trim(raw, "\t\n\r "))
}

// NewSourceValue canonicalizes the ordered name-bearing children of one
// contributor element into an immutable source value. A contributor without a
// single non-empty name-bearing child yields ErrNoNameComponents and creates
// no credit downstream.
func NewSourceValue(components []SourceComponent) (SourceValue, error) {
	var v SourceValue
	seen := make(map[ComponentKind]bool, len(components))
	display := make([]string, 0, len(components))

	for _, c := range components {
		if !c.Kind.valid() {
			return SourceValue{}, ErrUnknownComponentKind
		}
		value := canonicalize(c.Value)
		if seen[c.Kind] {
			v.duplicateComponent = true
		} else {
			seen[c.Kind] = true
			field := new(string)
			*field = value
			switch c.Kind {
			case ComponentFirst:
				v.first = field
			case ComponentMiddle:
				v.middle = field
			case ComponentLast:
				v.last = field
			case ComponentNickname:
				v.nickname = field
			}
		}
		if value != "" {
			display = append(display, value)
		}
	}

	v.displayName = strings.Join(display, " ")
	if v.displayName == "" {
		return SourceValue{}, ErrNoNameComponents
	}
	return v, nil
}

// cloneField keeps the value immutable: accessors hand out a copy of the
// stored string, never the internal pointer, so a caller cannot rewrite a
// constructed value (or a copy of it) and bypass canonicalization into the
// fingerprint byte contract. nil — element absent — stays nil, distinct from
// a present-but-empty element.
func cloneField(field *string) *string {
	if field == nil {
		return nil
	}
	clone := *field
	return &clone
}

// First returns a copy of the canonical first-name component, or nil when the
// element had no first-name child at all.
func (v SourceValue) First() *string { return cloneField(v.first) }

// Middle returns a copy of the canonical middle-name component, or nil when
// absent.
func (v SourceValue) Middle() *string { return cloneField(v.middle) }

// Last returns a copy of the canonical last-name component, or nil when
// absent.
func (v SourceValue) Last() *string { return cloneField(v.last) }

// Nickname returns a copy of the canonical nickname component, or nil when
// absent.
func (v SourceValue) Nickname() *string { return cloneField(v.nickname) }

// DisplayName joins the non-empty canonical components in their original XML
// order with a single U+0020, without reordering name parts by guessed culture.
func (v SourceValue) DisplayName() string { return v.displayName }

// HasDuplicateComponent reports whether any name-bearing child appeared more
// than once in the contributor element; such credits are never autoaccepted.
func (v SourceValue) HasDuplicateComponent() bool { return v.duplicateComponent }
