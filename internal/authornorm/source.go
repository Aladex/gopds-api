// Package authornorm holds the pure domain contract of the author-metadata
// source layer: canonical source values, reproducible fingerprints and the
// versioned decision vocabulary. It imports no database, HTTP or parser code.
package authornorm

import (
	"errors"
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

// ErrNonCanonicalSource marks stored source columns that no extraction could
// have produced: a field that is not in canonical form, or a display name that
// does not follow from the fields.
var ErrNonCanonicalSource = errors.New("authornorm: stored source value is not canonical")

// RestoreSourceValue rebuilds the canonical value of a stored credit from
// what the credit row keeps: the first value of each name-bearing child, the
// display name and whether a child repeated. The XML order is not stored, so
// the display name carries it; without a repeated child it must be the
// non-empty fields joined in some order, exactly as NewSourceValue builds it.
// With one, some sequence of children with a repeat must produce exactly
// these columns (realizableWithRepeats).
// Anything else did not come from an extraction and is refused, so a restored
// value always reproduces the stored fingerprint bytes.
func RestoreSourceValue(first, middle, last, nickname *string, displayName string, duplicate bool) (SourceValue, error) {
	v := SourceValue{
		first:              cloneField(first),
		middle:             cloneField(middle),
		last:               cloneField(last),
		nickname:           cloneField(nickname),
		displayName:        displayName,
		duplicateComponent: duplicate,
	}
	values := make([]string, 0, componentKinds)
	for _, field := range []*string{v.first, v.middle, v.last, v.nickname} {
		if field == nil {
			continue
		}
		if canonicalize(*field) != *field {
			return SourceValue{}, ErrNonCanonicalSource
		}
		if *field != "" {
			values = append(values, *field)
		}
	}
	if displayName == "" {
		return SourceValue{}, ErrNoNameComponents
	}
	if canonicalize(displayName) != displayName {
		return SourceValue{}, ErrNonCanonicalSource
	}
	if duplicate {
		if !realizableWithRepeats(displayName, []*string{v.first, v.middle, v.last, v.nickname}) {
			return SourceValue{}, ErrNonCanonicalSource
		}
		return v, nil
	}
	if !joinsInSomeOrder(displayName, values) {
		return SourceValue{}, ErrNonCanonicalSource
	}
	return v, nil
}

// componentKinds is how many name-bearing fields a source value has.
const componentKinds = 4

// realizableWithRepeats reports whether some XML sequence of name-bearing
// children with a repeated kind yields exactly these stored columns: fields
// holds the first value of each kind (nil when the kind never occurs), display
// the non-empty values joined in order by single U+0020.
//
// The display is cut into values at its separators. Each value is either the
// first occurrence of a kind whose stored field it equals — each kind once,
// so one occurrence never stands for two children — or a repeat of a kind
// whose first occurrence came before it. A present empty field adds nothing
// to the display, so its first occurrence is taken to open the sequence, the
// earliest it can be. Every non-empty field must be placed, and a repeat needs
// a first occurrence before it, so without any stored field nothing is
// realizable. A repeat that adds no text (an empty repeated child) is always
// possible once some kind occurs.
func realizableWithRepeats(display string, fields []*string) bool {
	var start, target uint8
	for k, field := range fields {
		switch {
		case field == nil:
		case *field == "":
			start |= 1 << k
		default:
			target |= 1 << k
		}
	}
	type state struct {
		pos    int
		placed uint8
	}
	seen := map[state]bool{}
	var walk func(s state) bool
	walk = func(s state) bool {
		if seen[s] {
			return false
		}
		seen[s] = true
		for end := s.pos; end <= len(display); end++ {
			if end < len(display) && display[end] != ' ' {
				continue
			}
			value := display[s.pos:end]
			if value == "" || canonicalize(value) != value {
				continue
			}
			for _, placed := range nextPlacements(value, fields, s.placed) {
				if end == len(display) {
					if placed&target == target {
						return true
					}
				} else if walk(state{pos: end + 1, placed: placed}) {
					return true
				}
			}
		}
		return false
	}
	return walk(state{placed: start})
}

// nextPlacements lists what one display value can be: the first occurrence
// of a kind whose field it equals, or a repeat of a kind already placed,
// which leaves the placement unchanged. (A kind already placed taken again is
// the same as a repeat of it.)
func nextPlacements(value string, fields []*string, placed uint8) []uint8 {
	var out []uint8
	for k, field := range fields {
		if field != nil && *field == value {
			out = append(out, placed|1<<k)
		}
	}
	if placed != 0 {
		out = append(out, placed)
	}
	return out
}

// joinsInSomeOrder reports whether display is the values joined by single
// U+0020 in some order. There are at most four values.
func joinsInSomeOrder(display string, values []string) bool {
	if len(values) == 0 {
		return false
	}
	if len(values) == 1 {
		return display == values[0]
	}
	for i, head := range values {
		if !strings.HasPrefix(display, head+" ") {
			continue
		}
		rest := make([]string, 0, len(values)-1)
		rest = append(rest, values[:i]...)
		rest = append(rest, values[i+1:]...)
		if joinsInSomeOrder(display[len(head)+1:], rest) {
			return true
		}
	}
	return false
}
