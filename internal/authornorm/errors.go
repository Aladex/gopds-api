package authornorm

import "errors"

var (
	// ErrNoNameComponents marks a contributor element whose name-bearing
	// children are all empty after canonicalization; no credit is created
	// from it (contract 3.2).
	ErrNoNameComponents = errors.New("authornorm: contributor has no non-empty name-bearing component")
	// ErrUnknownComponentKind marks a source component whose kind is outside
	// the closed set first/middle/last/nickname.
	ErrUnknownComponentKind = errors.New("authornorm: unknown component kind")
	// ErrEmptyVersion marks an extractor or normalizer version that is not a
	// non-empty exact string (contract 3.2).
	ErrEmptyVersion = errors.New("authornorm: version must be a non-empty exact string")
)
