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
	// ErrInvalidClaimLimit marks a claim limit of zero or below; the claim is
	// refused before it touches the database (contract 3.8).
	ErrInvalidClaimLimit = errors.New("authornorm: claim limit must be positive")
	// ErrLeaseLost marks an operation by a worker that no longer holds the
	// lease — another owner reclaimed the row, or the lease ran out. Its
	// result is discarded and the row stays claimable (contracts 3.3, 3.8).
	ErrLeaseLost = errors.New("authornorm: lease lost")
)
