package parser

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- md5 is the catalog's book-content identity, not a security primitive
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"gopds-api/internal/authornorm"
)

var (
	// ErrMetadataLimit marks the first byte read past the injected metadata
	// byte budget. Exactly metadata_max_bytes of metadata is allowed; the
	// limit bounds only the metadata phase, never the MD5 drain.
	ErrMetadataLimit = errors.New("parser: metadata byte limit exceeded")
	// ErrInvalidExtractorInput marks a violated input precondition: book id
	// not positive, empty extractor version, non-positive limit, nil reader
	// or a known MD5 that is not 32 lowercase hex characters.
	ErrInvalidExtractorInput = errors.New("parser: invalid extractor input")
)

// Element local names of the FB2 metadata grammar. All matching is by local
// name only (contract 3.2): default, prefixed and absent namespaces extract
// identically.
const (
	elFictionBook  = "FictionBook"
	elDescription  = "description"
	elTitleInfo    = "title-info"
	elSrcTitleInfo = "src-title-info"
	elDocumentInfo = "document-info"
	elPublishInfo  = "publish-info"
	elAuthor       = "author"
	elTranslator   = "translator"
	elSequence     = "sequence"
	elAnnotation   = "annotation"
	elBody         = "body"
	elBookTitle    = "book-title"
	elLang         = "lang"
	elSrcLang      = "src-lang"
	elID           = "id"
	elVersion      = "version"
	elISBN         = "isbn"
	elPublisher    = "publisher"
	elCity         = "city"
	elYear         = "year"
	elFirstName    = "first-name"
	elMiddleName   = "middle-name"
	elLastName     = "last-name"
	elNickname     = "nickname"
	elNumber       = "number"
)

const (
	// md5HexLength is the length of a hex-encoded MD5 sum.
	md5HexLength = 32
	// sectionChildDepth is the path depth of a direct child of a description
	// section: root / description / section / child.
	sectionChildDepth = 4
	// UTF-8 multi-byte sequence lengths by lead-byte class.
	seqLenTwo   = 2
	seqLenThree = 3
	seqLenFour  = 4
)

// MetadataExtractor extracts the lossless source metadata of one unpacked FB2
// entry without touching the legacy FB2Parser contract. The byte budget and
// the version string are injected per construction.
type MetadataExtractor struct {
	MetadataMaxBytes int64
	ExtractorVersion string
}

// ExtractBookInput is the closed input of one extraction (contract 3.2): the
// unpacked FB2 stream, provenance, and either a known MD5 (32 lowercase hex)
// or an empty KnownMD5 asking for streaming computation.
type ExtractBookInput struct {
	Reader      io.Reader
	BookID      int64
	ArchivePath string
	EntryName   string
	KnownMD5    string
}

// metadataSink receives every byte pulled from the entry stream. Every byte
// increments raw (the byte budget counts all metadata-phase bytes, annotation
// included); while capture is on the byte is also appended to the metadata
// window; while hash is set every byte (metadata and drained body alike) goes
// into the MD5. The window never holds body bytes. Annotation bytes stay in
// the window, bounded by the byte budget; the extraction pass discards the
// decoded annotation tokens without accumulating their text (contract 3.2).
type metadataSink struct {
	window  bytes.Buffer
	raw     int64
	limit   int64
	limited bool
	capture bool
	hash    hash.Hash
}

func (s *metadataSink) Write(p []byte) (int, error) {
	if s.limited && s.raw+int64(len(p)) > s.limit {
		return 0, ErrMetadataLimit
	}
	s.raw += int64(len(p))
	if s.capture {
		s.window.Write(p)
	}
	if s.hash != nil {
		s.hash.Write(p)
	}
	return len(p), nil
}

// closeWindow ends the metadata phase: nothing more is retained and the byte
// budget no longer applies — the MD5 drain may read the whole entry.
func (s *metadataSink) closeWindow() {
	s.capture = false
	s.limited = false
}

// byteReader adapts an io.Reader to io.ByteReader. encoding/xml uses a reader
// implementing io.ByteReader directly (no bufio layer), which keeps source
// consumption token-exact: with a known MD5 not a single byte past
// </description> is pulled from the entry stream.
type byteReader struct {
	r   io.Reader
	buf [1]byte
}

// Read caps every call at one byte: transform.Reader (UTF-16 path) reads its
// source in 4 KiB chunks, and the metadata window must not grow past
// </description>. The MD5 drain reads the tee directly, not through this
// reader, so the cap costs nothing there.
func (b *byteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return b.r.Read(p)
}

func (b *byteReader) ReadByte() (byte, error) {
	n, err := b.r.Read(b.buf[:])
	if n == 1 {
		return b.buf[0], nil
	}
	if err == nil {
		err = io.ErrNoProgress
	}
	return 0, err
}

// Extract runs one metadata-only extraction and returns the lossless source
// metadata. Charset decisions reuse the DecodeToUTF8 contract applied to the
// metadata window; there is no statistical guessing. With a known MD5 the
// stream is not read past </description>; without one, the remainder is
// drained into the hash only — never parsed, never accumulated.
func (e MetadataExtractor) Extract(in ExtractBookInput) (authornorm.SourceMetadata, error) {
	md := authornorm.SourceMetadata{
		BookID:           in.BookID,
		ArchivePath:      in.ArchivePath,
		EntryName:        in.EntryName,
		ExtractorVersion: e.ExtractorVersion,
	}
	if err := e.validateInput(in); err != nil {
		return md, err
	}

	sink := &metadataSink{limit: e.MetadataMaxBytes, limited: true, capture: true}
	if in.KnownMD5 == "" {
		sink.hash = md5.New() // #nosec G401 -- book-content identity, not a security primitive
	}
	tee := io.TeeReader(in.Reader, sink)

	peek, head, err := peekBOM(tee)
	if err != nil {
		return md, err
	}

	scan, err := openScanSource(peek, tee, head)
	if err != nil {
		return md, err
	}
	if scanErr := scanMetadataWindow(scan, sink); scanErr != nil {
		return md, scanErr
	}
	sink.closeWindow()

	decoded, err := DecodeToUTF8(sink.window.Bytes())
	if err != nil {
		return md, err
	}

	if err := finishMD5(&md, in.KnownMD5, sink, peek, tee); err != nil {
		return md, err
	}
	if err := parseDescriptionWindow(decoded, &md); err != nil {
		return md, err
	}
	if err := md.Validate(); err != nil {
		return md, err
	}
	return md, nil
}

// peekBOM reads the full BOM/prolog signature (through the tee, so every
// peeked byte is accounted in the window and the hash) and refuses the
// encodings that DecodeToUTF8 refuses by prefix, before the token scan
// starts. Ten bytes cover the UTF-16-without-BOM prolog signature ("<?xml"
// spread over units). readHead guarantees the signature is complete
// regardless of how the source reader fragments its output. A clean empty
// entry is a document failure; a genuine source error — including a one-shot
// io.ErrUnexpectedEOF, which io.ReadFull could not tell apart from a clean
// short head — keeps its identity because it is a systemic condition
// (contract 3.3), not damaged content.
func peekBOM(tee io.Reader) (*bytes.Reader, []byte, error) {
	var peekBuf [10]byte
	nPeek, err := readHead(tee, peekBuf[:])
	switch err {
	case nil:
	case io.EOF:
		if nPeek == 0 {
			return nil, nil, fmt.Errorf("%w: entry has no content", ErrDamagedContent)
		}
		// Short entry: the head is simply shorter than the signature window.
	default:
		return nil, nil, fmt.Errorf("parser: reading entry head: %w", err)
	}
	head := peekBuf[:nPeek]
	switch {
	case bytes.HasPrefix(head, bomUTF32LE), bytes.HasPrefix(head, bomUTF32BE):
		return nil, nil, fmt.Errorf("%w: utf-32 byte order mark", ErrUnsupportedCharset)
	case bytes.HasPrefix(head, xmlPrologUTF16LE) || bytes.HasPrefix(head, xmlPrologUTF16BE):
		return nil, nil, fmt.Errorf("%w: utf-16 without a byte order mark", ErrUnsupportedCharset)
	}
	return bytes.NewReader(head), head, nil
}

// readHead fills buf from r. Unlike io.ReadFull it never synthesizes
// io.ErrUnexpectedEOF: a clean short head reports plain io.EOF, so a source
// reader's own one-shot io.ErrUnexpectedEOF is returned with its identity
// instead of being mistaken for a short clean entry.
func readHead(r io.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
		if m == 0 {
			return n, io.ErrNoProgress
		}
	}
	return n, nil
}

// openScanSource builds the byte-exact token-scan input for the raw window
// scan. Both candidates implement io.ByteReader, so encoding/xml consumes
// them directly without a bufio layer and source reads stay token-exact. The
// BOM stays in the raw window (DecodeToUTF8 wants it) but is skipped in the
// scan stream; UTF-16 is unit-decoded so the scan sees text.
func openScanSource(peek *bytes.Reader, tee io.Reader, head []byte) (io.Reader, error) {
	src := &byteReader{r: io.MultiReader(peek, tee)}
	var scan io.Reader = src
	bomLen, endianness, isUTF16 := sniffUTF16BOM(head)
	switch {
	case isUTF16:
		if _, err := peek.Seek(int64(bomLen), io.SeekStart); err != nil {
			return nil, err
		}
		scan = newTransformByteReader(unicode.UTF16(endianness, unicode.IgnoreBOM).NewDecoder(), src)
	case bytes.HasPrefix(head, bomUTF8):
		if _, err := peek.Seek(int64(len(bomUTF8)), io.SeekStart); err != nil {
			return nil, err
		}
	}
	return newScanSanitizer(scan), nil
}

// finishMD5 sets the result's book MD5: the echo of the known value, or the
// streamed hash of the whole entry. The drain reads the tee directly (full
// speed, bypassing the byte-exact scan reader); the window is already closed,
// so the payload cannot grow and the body is never parsed.
func finishMD5(md *authornorm.SourceMetadata, knownMD5 string, sink *metadataSink, peek *bytes.Reader, tee io.Reader) error {
	if sink.hash == nil {
		md.BookMD5 = knownMD5
		return nil
	}
	if _, err := io.Copy(io.Discard, io.MultiReader(peek, tee)); err != nil {
		return fmt.Errorf("parser: draining entry for md5: %w", err)
	}
	md.BookMD5 = hex.EncodeToString(sink.hash.Sum(nil))
	return nil
}

func (e MetadataExtractor) validateInput(in ExtractBookInput) error {
	if in.BookID <= 0 {
		return fmt.Errorf("%w: book id must be positive, got %d", ErrInvalidExtractorInput, in.BookID)
	}
	if e.ExtractorVersion == "" {
		return fmt.Errorf("%w: extractor version must be a non-empty exact string", ErrInvalidExtractorInput)
	}
	if e.MetadataMaxBytes <= 0 {
		return fmt.Errorf("%w: metadata max bytes must be positive, got %d", ErrInvalidExtractorInput, e.MetadataMaxBytes)
	}
	if in.Reader == nil {
		return fmt.Errorf("%w: reader is nil", ErrInvalidExtractorInput)
	}
	if in.KnownMD5 != "" && !isLowerHexMD5(in.KnownMD5) {
		return fmt.Errorf("%w: known md5 must be 32 lowercase hex characters", ErrInvalidExtractorInput)
	}
	return nil
}

func isLowerHexMD5(s string) bool {
	if len(s) != md5HexLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sniffUTF16BOM reports the BOM length and endianness of BOM-marked UTF-16
// content. UTF-32 was refused before this runs, so FF FE here is UTF-16LE.
func sniffUTF16BOM(head []byte) (bomLen int, endianness unicode.Endianness, ok bool) {
	switch {
	case bytes.HasPrefix(head, bomUTF16LE):
		return len(bomUTF16LE), unicode.LittleEndian, true
	case bytes.HasPrefix(head, bomUTF16BE):
		return len(bomUTF16BE), unicode.BigEndian, true
	}
	return 0, unicode.LittleEndian, false
}

// transformByteReader serves one transform.Transformer to an encoding/xml
// decoder one byte at a time, so the decoder consumes the underlying raw
// stream token-exactly. The transformer instance is created once: partial
// multi-byte state lives in it across calls.
type transformByteReader struct {
	tr  *transform.Reader
	buf [1]byte
}

func newTransformByteReader(t transform.Transformer, r io.Reader) *transformByteReader {
	return &transformByteReader{tr: transform.NewReader(r, t)}
}

func (t *transformByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return t.tr.Read(p)
}

func (t *transformByteReader) ReadByte() (byte, error) {
	n, err := t.Read(t.buf[:])
	if n == 1 {
		return t.buf[0], nil
	}
	if err == nil {
		err = io.ErrNoProgress
	}
	return 0, err
}

// scanMetadataWindow consumes the token stream until the end of the root's
// description element. The reader is byte-exact, so on return the source
// stands immediately after "</description>". A non-FictionBook root, a
// missing root, a missing description, a body reached before the description,
// or a pre-root syntax error is ErrDamagedContent (the legacy invalid_fb2
// class); a token error after the recognized FictionBook root is returned
// as-is for the metadata_parse_failed class; source and limit failures pass
// through with their identity, being systemic conditions, not document
// damage. Annotation bytes are retained in the window like any other
// metadata; skipping happens on decoded tokens in the extraction pass.
// windowScanner is the token state of the raw window scan.
type windowScanner struct {
	dec             *xml.Decoder
	sink            *metadataSink
	rootSeen        bool
	descriptionSeen bool
	depth           int
}

// rootChildDepth is the scan depth of a direct child of the root element.
const rootChildDepth = 2

func scanMetadataWindow(scan io.Reader, sink *metadataSink) error {
	dec := xml.NewDecoder(scan)
	// The raw scan only locates the description boundary; the real charset
	// decision happens on the buffered window. Pass every declared label
	// through undecoded so a cp1251 declaration does not fail the scan.
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	s := &windowScanner{dec: dec, sink: sink}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return s.eof()
		}
		if err != nil {
			return s.tokenError(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := s.startElement(t); err != nil {
				return err
			}
		case xml.EndElement:
			if done, err := s.endElement(t); done || err != nil {
				return err
			}
		}
	}
}

// eof classifies a clean end of input: without a root or without a
// description the document is invalid; with both, the description close
// already ended the scan.
func (s *windowScanner) eof() error {
	if !s.rootSeen {
		return fmt.Errorf("%w: no root element", ErrDamagedContent)
	}
	if !s.descriptionSeen {
		return fmt.Errorf("%w: no description element", ErrDamagedContent)
	}
	return nil
}

// tokenError classifies a token failure: source and limit failures pass
// through with their identity, being systemic conditions, not document
// damage; a syntax error before the recognized root is invalid content,
// after it a metadata parse failure.
func (s *windowScanner) tokenError(err error) error {
	var syn *xml.SyntaxError
	if !errors.As(err, &syn) {
		return err
	}
	if !s.rootSeen {
		return fmt.Errorf("%w: %v", ErrDamagedContent, err)
	}
	return err
}

func (s *windowScanner) startElement(t xml.StartElement) error {
	s.depth++
	if s.depth == 1 {
		if t.Name.Local != elFictionBook {
			return fmt.Errorf("%w: root element is %q, not FictionBook", ErrDamagedContent, t.Name.Local)
		}
		s.rootSeen = true
	}
	if s.depth == rootChildDepth {
		switch t.Name.Local {
		case elDescription:
			s.descriptionSeen = true
		case elBody:
			if !s.descriptionSeen {
				return fmt.Errorf("%w: body element before description", ErrDamagedContent)
			}
		}
	}
	return nil
}

// endElement reports done=true when the description closed: nothing after it
// is metadata.
func (s *windowScanner) endElement(t xml.EndElement) (done bool, err error) {
	if s.depth == rootChildDepth && t.Name.Local == elDescription {
		return true, nil
	}
	s.depth--
	if s.depth == 0 {
		// The root closed; a description close would have returned above, so
		// the document has no description.
		return true, fmt.Errorf("%w: no description element", ErrDamagedContent)
	}
	return false, nil
}

// contributorBuilder accumulates one title-info/author or translator element:
// its id attribute and the ordered present name-bearing children. The builder
// is strictly scoped to its own XML node, so a missing middle/last name in one
// contributor can never shift the components of the next.
type contributorBuilder struct {
	role       authornorm.ContributorRole
	depth      int
	id         *string
	components []authornorm.SourceComponent
}

// nameComponentKinds maps the closed set of name-bearing children to their
// component kinds, matched by local name only.
var nameComponentKinds = map[string]authornorm.ComponentKind{
	elFirstName:  authornorm.ComponentFirst,
	elMiddleName: authornorm.ComponentMiddle,
	elLastName:   authornorm.ComponentLast,
	elNickname:   authornorm.ComponentNickname,
}

// textCapture accumulates the concatenated descendant character data of one
// element. assign runs when the element closes; a present-but-empty element
// still runs assign with "", which is how present-empty stays distinct from
// absent.
type textCapture struct {
	depth  int
	buf    []byte
	assign func(string)
}

// windowParser is the path-scoped token state of the extraction pass over the
// decoded metadata window. All state is structural: the element path, one
// skipped subtree, one open contributor, one open text capture and the
// sequence stack. Elements match by local name only (contract 3.2).
type windowParser struct {
	md *authornorm.SourceMetadata

	path        []string
	skipDepth   int
	section     string
	contrib     *contributorBuilder
	capture     *textCapture
	seqStack    []int
	authorN     int
	translatorN int
	done        bool
}

// parseDescriptionWindow extracts the source metadata from the decoded
// window. The window ends right after </description>, so body and binary are
// never seen here. Token errors are returned as-is (the FictionBook root was
// already recognized during the raw scan).
func parseDescriptionWindow(decoded []byte, md *authornorm.SourceMetadata) error {
	dec := xml.NewDecoder(bytes.NewReader(decoded))
	p := &windowParser{md: md}
	for !p.done {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			p.startElement(t)
		case xml.EndElement:
			p.endElement(t)
		case xml.CharData:
			if p.capture != nil && p.skipDepth == 0 {
				p.capture.buf = append(p.capture.buf, t...)
			}
		}
	}
	if p.authorN == 0 {
		p.md.Outcome = authornorm.OutcomeNoAuthor
	} else {
		p.md.Outcome = authornorm.OutcomeExtracted
	}
	return nil
}

func (p *windowParser) startElement(el xml.StartElement) {
	p.path = append(p.path, el.Name.Local)
	depth := len(p.path)

	if p.skipDepth > 0 {
		return
	}
	local := el.Name.Local

	if depth == 3 && p.inDescription() {
		// Direct child of description: open a section or skip a subtree that
		// the source contract never reads (custom).
		switch local {
		case elTitleInfo, elSrcTitleInfo, elDocumentInfo, elPublishInfo:
			p.section = local
		default:
			p.skipDepth = depth
		}
		return
	}
	if !p.inDescription() || p.section == "" {
		return
	}
	if local == elAnnotation {
		// The annotation subtree is skipped without accumulating text.
		p.skipDepth = depth
		return
	}

	if p.contrib != nil {
		p.captureNameChild(el, depth)
		return
	}

	switch p.section {
	case elTitleInfo:
		p.startTitleInfoChild(el, depth)
	case elSrcTitleInfo:
		p.startSrcTitleInfoChild(el)
	case elDocumentInfo:
		p.startDocumentInfoChild(el, depth)
	case elPublishInfo:
		p.startPublishInfoChild(el, depth)
	}
}

// startSrcTitleInfoChild handles one element inside src-title-info. Credits
// come from title-info only, so contributor elements are ignored here — but
// the all-sequences output of contract 3.2 does not exempt the source-title
// subtree, so its sequences are recorded like everywhere else.
func (p *windowParser) startSrcTitleInfoChild(el xml.StartElement) {
	if el.Name.Local == elSequence {
		p.startSequence(el)
	}
}

// captureNameChild starts a text capture when el is a name-bearing child of
// the open contributor element. Components accumulate in original document
// order inside the contributor's own node.
func (p *windowParser) captureNameChild(el xml.StartElement, depth int) {
	kind, ok := nameComponentKinds[el.Name.Local]
	if !ok || depth != p.contrib.depth+1 || p.capture != nil {
		return
	}
	p.capture = &textCapture{depth: depth, assign: func(v string) {
		p.contrib.components = append(p.contrib.components, authornorm.SourceComponent{Kind: kind, Value: v})
	}}
}

// startTitleInfoChild handles one element inside title-info: contributors,
// sequences and the scalar title/language fields.
func (p *windowParser) startTitleInfoChild(el xml.StartElement, depth int) {
	switch el.Name.Local {
	case elAuthor, elTranslator:
		if depth != sectionChildDepth {
			return
		}
		p.contrib = &contributorBuilder{role: authornorm.ContributorRole(el.Name.Local), depth: depth, id: idAttr(el)}
	case elSequence:
		p.startSequence(el)
	case elBookTitle:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.Title = strPtr(v) }}
	case elLang:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.Lang = strPtr(v) }}
	case elSrcLang:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.SrcLang = strPtr(v) }}
	}
}

// startDocumentInfoChild handles one element inside document-info. Only the
// document id and version are source metadata; document-info/author is the
// FB2 creator or converter, never a book contributor, so its subtree is
// skipped and never yields a credit.
func (p *windowParser) startDocumentInfoChild(el xml.StartElement, depth int) {
	if depth != sectionChildDepth {
		return
	}
	switch el.Name.Local {
	case elAuthor:
		p.skipDepth = depth
	case elID:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.DocumentID = strPtr(v) }}
	case elVersion:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.DocumentVersion = strPtr(v) }}
	}
}

// startPublishInfoChild handles one element inside publish-info: sequences
// and the raw publication fields, kept as lists without validation.
func (p *windowParser) startPublishInfoChild(el xml.StartElement, depth int) {
	switch el.Name.Local {
	case elSequence:
		p.startSequence(el)
	case elISBN:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.ISBNs = append(p.md.ISBNs, v) }}
	case elPublisher:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.Publisher = append(p.md.Publisher, v) }}
	case elCity:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.City = append(p.md.City, v) }}
	case elYear:
		p.capture = &textCapture{depth: depth, assign: func(v string) { p.md.Year = append(p.md.Year, v) }}
	}
}

func (p *windowParser) endElement(el xml.EndElement) {
	depth := len(p.path)

	if p.capture != nil && p.capture.depth == depth {
		p.capture.assign(string(p.capture.buf))
		p.capture = nil
	}
	if p.contrib != nil && p.contrib.depth == depth {
		p.finishContributor()
	}
	if el.Name.Local == elSequence && len(p.seqStack) > 0 && p.skipDepth == 0 {
		p.seqStack = p.seqStack[:len(p.seqStack)-1]
	}
	if p.skipDepth > 0 && p.skipDepth == depth {
		p.skipDepth = 0
	}
	if depth == 3 && p.section != "" {
		p.section = ""
	}
	if depth == 2 && p.inDescription() {
		// Nothing after </description> is metadata.
		p.done = true
	}
	p.path = p.path[:depth-1]
}

func (p *windowParser) inDescription() bool {
	return len(p.path) >= 2 && p.path[1] == elDescription
}

// startSequence records one sequence element in document order. Parent is the
// index of the enclosing sequence on the stack, or -1 at top level.
func (p *windowParser) startSequence(el xml.StartElement) {
	parent := -1
	if len(p.seqStack) > 0 {
		parent = p.seqStack[len(p.seqStack)-1]
	}
	seq := authornorm.Sequence{
		Index:  len(p.md.Sequences),
		Parent: parent,
		Name:   attrValue(el, "name"),
	}
	if n, ok := attr(el, elNumber); ok {
		seq.Number = &n
	}
	p.md.Sequences = append(p.md.Sequences, seq)
	p.seqStack = append(p.seqStack, seq.Index)
}

// finishContributor turns one closed contributor element into a credit. An
// element without a single non-empty name-bearing child creates no credit and
// consumes no position (contract 3.2).
func (p *windowParser) finishContributor() {
	b := p.contrib
	p.contrib = nil
	value, err := authornorm.NewSourceValue(b.components)
	if err != nil {
		// ErrNoNameComponents is the only reachable error: kinds come from
		// the closed nameComponentKinds set.
		return
	}
	position := p.authorN
	if b.role == authornorm.RoleTranslator {
		position = p.translatorN
		p.translatorN++
	} else {
		p.authorN++
	}
	p.md.Contributors = append(p.md.Contributors, authornorm.Contributor{
		Role:     b.role,
		Position: position,
		SourceID: b.id,
		Value:    value,
	})
}

func attr(el xml.StartElement, local string) (string, bool) {
	for _, a := range el.Attr {
		if a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

func attrValue(el xml.StartElement, local string) string {
	v, _ := attr(el, local)
	return v
}

func idAttr(el xml.StartElement) *string {
	if v, ok := attr(el, elID); ok {
		return &v
	}
	return nil
}

func strPtr(s string) *string { return &s }

// scanSanitizer makes the raw window scan tolerant of content encoding/xml
// would reject before the real charset decision can run: invalid UTF-8 in
// character data and disallowed control characters. The scan only needs
// markup structure (element names, quotes, angle brackets — all ASCII), so
// valid runes pass through untouched while every invalid byte becomes a
// single ASCII '?'. Byte accounting happens upstream in the sink, so the
// replacement changes nothing about the window or the limit; classification
// of the damage is DecodeToUTF8's job on the untouched raw window.
type scanSanitizer struct {
	src     io.ByteReader
	pending []byte // decoded output not yet served
	raw     []byte // partial multi-byte sequence under examination
	eof     bool
}

func newScanSanitizer(r io.Reader) *scanSanitizer {
	if rb, ok := r.(io.ByteReader); ok {
		return &scanSanitizer{src: rb}
	}
	return &scanSanitizer{src: &byteReader{r: r}}
}

func (s *scanSanitizer) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		b, err := s.ReadByte()
		if err != nil {
			return n, err
		}
		p[n] = b
		n++
	}
	return n, nil
}

func (s *scanSanitizer) ReadByte() (byte, error) {
	for len(s.pending) == 0 {
		if err := s.produce(); err != nil {
			return 0, err
		}
	}
	b := s.pending[0]
	s.pending = s.pending[1:]
	return b, nil
}

// produce decodes one unit of input into pending: one ASCII byte, one valid
// multi-byte UTF-8 rune, or one '?' replacement.
func (s *scanSanitizer) produce() error {
	if len(s.raw) == 0 {
		if err := s.fillRaw(); err != nil {
			return err
		}
	}
	if r0 := s.raw[0]; r0 < utf8.RuneSelf {
		s.raw = s.raw[1:]
		s.pending = append(s.pending, sanitizeASCII(r0))
		return nil
	}
	return s.produceMultiByte()
}

// fillRaw pulls one more source byte into the raw accumulator.
func (s *scanSanitizer) fillRaw() error {
	if s.eof {
		return io.EOF
	}
	b, err := s.src.ReadByte()
	if err == io.EOF {
		s.eof = true
		return io.EOF
	}
	if err != nil {
		return err
	}
	s.raw = append(s.raw, b)
	return nil
}

// produceMultiByte emits one valid multi-byte rune or one '?' per invalid
// byte. Only the lead byte is dropped on failure; the following bytes are
// re-examined, so markup after a broken sequence survives.
func (s *scanSanitizer) produceMultiByte() error {
	size := utf8SeqLen(s.raw[0])
	if size == 0 {
		s.dropLeadByte()
		return nil
	}
	for len(s.raw) < size && !s.eof {
		b, err := s.src.ReadByte()
		if err == io.EOF {
			s.eof = true
			break
		}
		if err != nil {
			return err
		}
		s.raw = append(s.raw, b)
	}
	if len(s.raw) < size {
		s.dropLeadByte()
		return nil
	}
	cand := s.raw[:size]
	if r, n := utf8.DecodeRune(cand); n == size && r != utf8.RuneError {
		s.raw = s.raw[size:]
		s.pending = append(s.pending, cand...)
		return nil
	}
	s.dropLeadByte()
	return nil
}

func (s *scanSanitizer) dropLeadByte() {
	s.raw = s.raw[1:]
	s.pending = append(s.pending, '?')
}

// sanitizeASCII passes printable ASCII and the XML whitespace characters
// through and replaces disallowed control characters with '?'.
func sanitizeASCII(b byte) byte {
	if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
		return '?'
	}
	return b
}

// utf8SeqLen returns the expected sequence length for a lead byte, or 0 for a
// byte that cannot start a UTF-8 sequence.
func utf8SeqLen(b byte) int {
	switch {
	case b >= 0xC2 && b < 0xE0:
		return seqLenTwo
	case b >= 0xE0 && b < 0xF0:
		return seqLenThree
	case b >= 0xF0 && b < 0xF5:
		return seqLenFour
	}
	return 0
}
