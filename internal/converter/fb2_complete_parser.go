package converter

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"

	"gopds-api/internal/fb2sanitize"
	"gopds-api/internal/parser"
)

// ParseFB2Complete performs a single-pass parsing of FB2 content, extracting both
// metadata (BookFile) and body structure (FB2Document) in one XML traversal.
//
// This function is optimized to avoid the performance cost of parsing the same
// FB2 file twice. It applies sanitization once and uses a single XML decoder
// to collect both metadata and body content simultaneously.
//
// This is the unlimited entry: it parses without a work budget, exactly as it
// always did, because the EPUB download path must not change behavior. The
// preview pipeline calls ParseFB2CompleteLimited instead.
//
// Parameters:
//   - ctx: observed every ctxCheckInterval tokens; a canceled ctx stops the
//     parse the same way ParseFB2Body does
//   - xmlContent: Raw FB2 XML content as bytes
//   - readCover: Whether to extract and decode cover image from binary elements
//
// Returns:
//   - *FB2Document: Parsed body structure with sections, paragraphs, and formatting
//   - *parser.BookFile: Parsed metadata including title, authors, language, cover, etc.
//   - error: Any error encountered during parsing
//
// Performance: This function is approximately 30-40% faster than calling
// parser.Parse() and ParseFB2Body() separately.
func ParseFB2Complete(ctx context.Context, xmlContent []byte, readCover bool) (*FB2Document, *parser.BookFile, error) {
	doc, bookFile, _, err := parseFB2CompleteCore(ctx, xmlContent, readCover, FB2ParseLimits{})
	return doc, bookFile, err
}

// ParseFB2CompleteLimited is the preview pipeline's entry: the same single-pass
// parse as ParseFB2Complete, but the token loop runs under a work budget. The
// first element that exceeds a budget stops the parse with a typed refusal
// (ErrFB2NodeLimit / ErrFB2BinaryLimit) — the rest of the document is never
// tokenized, which is the whole point: the gates protect the resources the
// parse itself spends. The returned stats report how much work was done, so
// callers and tests can tell "stopped early" from "walked everything".
func ParseFB2CompleteLimited(
	ctx context.Context, xmlContent []byte, readCover bool, limits FB2ParseLimits,
) (*FB2Document, *parser.BookFile, FB2ParseStats, error) {
	return parseFB2CompleteCore(ctx, xmlContent, readCover, limits)
}

func parseFB2CompleteCore(
	ctx context.Context, xmlContent []byte, readCover bool, limits FB2ParseLimits,
) (*FB2Document, *parser.BookFile, FB2ParseStats, error) {
	// Apply all sanitization steps once
	decoded, err := parser.DecodeToUTF8(xmlContent)
	if err != nil {
		return nil, nil, FB2ParseStats{}, err
	}
	decoded = fb2sanitize.Apply(decoded)
	decoded = repairBrokenXML(decoded)

	// Initialize both parsers
	metadataParser := parser.NewFB2Parser(readCover)
	bodyState := &fb2BodyState{}
	doc := &FB2Document{}
	quota := parseQuota{limits: limits}

	// Single XML decoder pass
	decoder := newFB2Decoder(decoded)

	rootSeen := false
	tokensSinceCheck := 0
	for {
		// Same cadence as ParseFB2Body: the main loop is where the work is,
		// and a ctx that is accepted but not consulted is worse than no ctx
		// at all (the caller thinks cancellation works). The fallback only
		// runs on a decoder error, so on a well-formed book it would never
		// fire — without this check a cancel would be observed only after
		// the whole file is parsed.
		if cerr := checkCtx(ctx, &tokensSinceCheck); cerr != nil {
			return nil, nil, quota.stats, fmt.Errorf("fb2: parse canceled: %w", cerr)
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return completeDecodeFallback(ctx, decoded, readCover, limits, rootSeen, err, quota.stats)
		}

		if qerr := quota.countToken(token); qerr != nil {
			return nil, nil, quota.stats, qerr
		}
		if rerr := applyFB2CompleteToken(token, &rootSeen, metadataParser, bodyState, doc); rerr != nil {
			return nil, nil, quota.stats, rerr
		}
		if bodyState.err != nil {
			return nil, nil, quota.stats, bodyState.err
		}
	}

	if bodyState.err != nil {
		return nil, nil, quota.stats, bodyState.err
	}
	// A document without a single element is garbage, not an empty book.
	if !rootSeen {
		return nil, nil, quota.stats, fmt.Errorf("%w: the document has no root element", ErrNotFictionBook)
	}

	// Extract metadata
	bookFile, err := metadataParser.BuildBookFile(decoded)
	if err != nil {
		return nil, nil, quota.stats, err
	}

	return doc, bookFile, quota.stats, nil
}

func parseFB2CompleteFallback(
	ctx context.Context, content []byte, readCover bool, limits FB2ParseLimits,
) (*FB2Document, *parser.BookFile, FB2ParseStats, error) {
	bodyDoc, bodyStats, bodyErr := parseFB2BodyCore(ctx, content, limits)
	if bodyErr != nil {
		return nil, nil, bodyStats, bodyErr
	}
	metaParser := parser.NewFB2Parser(readCover)
	bookFile, metaErr := metaParser.Parse(bytes.NewReader(content))
	if metaErr != nil {
		return bodyDoc, &parser.BookFile{}, bodyStats, nil
	}
	return bodyDoc, bookFile, bodyStats, nil
}

// completeDecodeFallback decides the outcome of a broken token stream. No
// verified root means the sanitizers could not make it reachable, so this is
// the same refusal the metadata scanner gives. A verified root means this is
// a book; whatever broke after it goes through the salvage fallback, whose
// typed verdict ("too deep", a tripped work gate) still outranks the main
// decoder's raw syntax error. The fallback re-parses from the start, so it
// runs under the same budget.
func completeDecodeFallback(
	ctx context.Context, decoded []byte, readCover bool, limits FB2ParseLimits,
	rootSeen bool, decErr error, stats FB2ParseStats,
) (*FB2Document, *parser.BookFile, FB2ParseStats, error) {
	if !rootSeen {
		return nil, nil, stats, fmt.Errorf("%w: %w", ErrNotFictionBook, decErr)
	}
	docFallback, bookFallback, fbStats, fallbackErr := parseFB2CompleteFallback(ctx, decoded, readCover, limits)
	if fallbackErr != nil {
		if errors.Is(fallbackErr, ErrNotFictionBook) || errors.Is(fallbackErr, ErrDepthLimit) ||
			errors.Is(fallbackErr, ErrFB2NodeLimit) || errors.Is(fallbackErr, ErrFB2BinaryLimit) {
			return nil, nil, fbStats, fallbackErr
		}
		return nil, nil, fbStats, decErr
	}
	return docFallback, bookFallback, fbStats, nil
}

// applyFB2CompleteToken feeds one decoder token to both the metadata parser
// and the body parser. The root criterion (first element must be FictionBook)
// lives here too, so the main loop is a flat read-dispatch and stays under
// the lint complexity ceiling.
func applyFB2CompleteToken(
	token xml.Token,
	rootSeen *bool,
	metadataParser *parser.FB2Parser,
	bodyState *fb2BodyState,
	doc *FB2Document,
) error {
	switch t := token.(type) {
	case xml.StartElement:
		if !*rootSeen {
			*rootSeen = true
			// The same criterion the metadata scanner applies: the first
			// element the decoder reaches decides whether this is a book.
			if t.Name.Local != fictionBookRoot {
				return fmt.Errorf("%w: root element is %q, not FictionBook", ErrNotFictionBook, t.Name.Local)
			}
		}
		// Feed to both parsers
		metadataParser.HandleStartElement(t)
		bodyState.handleStart(doc, t)
	case xml.EndElement:
		metadataParser.HandleEndElement(t)
		bodyState.handleEnd(doc, t)
	case xml.CharData:
		metadataParser.HandleCharData(t)
		bodyState.handleChar(t)
	}
	return nil
}
