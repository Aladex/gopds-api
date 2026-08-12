package converter

// fb2_parse_limits.go holds the work budget of one parse: how many element
// nodes and how many <binary> elements a single document may carry before the
// parse stops. The gates exist to bound the WORK of parsing, so they are
// enforced inside the token loop — a document over the cap is refused at the
// exceeding element, not after the whole tree is built.
//
// The unlimited entries keep their old signatures (ParseFB2Complete for the
// EPUB download path, ParseFB2Body for the salvage fallback) and delegate
// with zero limits; only the preview pipeline calls the limited entry.

import (
	"encoding/xml"
	"errors"
	"fmt"
)

// Typed refusals of the parse-time work gates, matched with errors.Is.
var (
	// ErrFB2NodeLimit: the document carries more element nodes than the parse
	// budget allows. A node is one start-element token — the unit of work the
	// decoder loop spends on, and a strict over-approximation of the document
	// tree (empty or dropped elements still cost a token). Counting the tree
	// instead would require building it, which is exactly what the gate must
	// prevent.
	ErrFB2NodeLimit = errors.New("fb2: element node count exceeds the parse limit")

	// ErrFB2BinaryLimit: the document carries more <binary> elements than the
	// parse budget allows. Counted at the start element, before a byte of
	// base64 is buffered or decoded.
	ErrFB2BinaryLimit = errors.New("fb2: binary count exceeds the parse limit")
)

// FB2ParseLimits bounds the work one parse may do. A zero field disables the
// corresponding gate: the unlimited entries pass a zero struct, which keeps
// the EPUB download path behaving exactly as it did before the gates existed.
type FB2ParseLimits struct {
	// MaxNodes caps element nodes (start-element tokens) in the whole
	// document, metadata and binaries included.
	MaxNodes int
	// MaxBinaries caps <binary> start elements.
	MaxBinaries int
}

// FB2ParseStats reports how much work a parse actually did. It exists for
// evidence, not for control flow: a refusal by work and a refusal by result
// produce the same error, and only the counters tell them apart.
type FB2ParseStats struct {
	Tokens   int // decoder tokens processed, of any kind
	Nodes    int // start-element tokens processed
	Binaries int // <binary> start elements processed
}

// parseQuota threads the limits and the counters through a token loop.
type parseQuota struct {
	limits FB2ParseLimits
	stats  FB2ParseStats
}

// countToken records one processed token and stops the parse at the first
// element that exceeds a budget. It runs BEFORE the token is dispatched to
// the parsers, so the exceeding element is never turned into work — no
// section pushed, no binary buffered.
func (q *parseQuota) countToken(token xml.Token) error {
	q.stats.Tokens++
	elem, ok := token.(xml.StartElement)
	if !ok {
		return nil
	}
	q.stats.Nodes++
	if q.limits.MaxNodes > 0 && q.stats.Nodes > q.limits.MaxNodes {
		return fmt.Errorf("%w: stopped at %d, cap is %d", ErrFB2NodeLimit, q.stats.Nodes, q.limits.MaxNodes)
	}
	if normalizeName(elem.Name.Local) == binaryElement {
		q.stats.Binaries++
		if q.limits.MaxBinaries > 0 && q.stats.Binaries > q.limits.MaxBinaries {
			return fmt.Errorf("%w: stopped at %d, cap is %d", ErrFB2BinaryLimit, q.stats.Binaries, q.limits.MaxBinaries)
		}
	}
	return nil
}
