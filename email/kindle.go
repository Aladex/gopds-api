package email

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"gopds-api/logging"

	"github.com/spf13/viper"
)

// Send to Kindle: a book, as an EPUB attachment, mailed to the reader's own
// Send-to-Kindle address.
//
// Amazon takes a personal document only from a sender the Kindle's owner has
// put on their "Approved Personal Document E-mail List", and it throttles per
// sender. So every reader sends from an address of their own on this
// installation's domain — {their sender id}@{domain} — which never changes and
// which they approve once. The relay (MailerSend) has the whole domain
// verified, so any local part on it may stand on the envelope and in From.
//
// A message the relay accepted is all this side ever learns. A sender that is
// not approved is dropped by Amazon without a word back to us, so nothing here
// may call a book delivered: only sent.

// MaxKindleBookBytes is the largest EPUB this sends. MailerSend refuses an
// attachment over 25 MB once decoded, and Amazon a message over 50 MB; the
// margin under the first leaves room for the base64 and the headers around it.
const MaxKindleBookBytes = 24 << 20

// headerRoom is generous room for everything in a message that is not the book.
const headerRoom = 4 << 10

// ErrBookTooLarge is a book over MaxKindleBookBytes.
var ErrBookTooLarge = errors.New("the book is too large to mail to a Kindle")

// ErrNotKindleAddress is an address Amazon would not deliver to a Kindle.
var ErrNotKindleAddress = errors.New("not a Send-to-Kindle address")

// kindleDomains are the two domains a Send-to-Kindle address lives on: the
// usual one, and the one Amazon gave Wi-Fi-only delivery.
var kindleDomains = []string{"kindle.com", "free.kindle.com"}

// maxAddressLength is RFC 5321's limit on a whole path, and the column's.
const maxAddressLength = 254

// NormalizeKindleAddress checks a Send-to-Kindle address and returns it in the
// form it is stored in: trimmed and lower-case.
//
// Only a bare address is accepted — no display name, no list, nothing a header
// could be bent with. mail.ParseAddress alone would take "Name <x@kindle.com>"
// and hand back the address inside it, which is not what the reader typed and
// not what the profile will show them.
func NormalizeKindleAddress(raw string) (string, error) {
	address := strings.ToLower(strings.TrimFunc(raw, unicode.IsSpace))
	if address == "" || len(address) > maxAddressLength {
		return "", ErrNotKindleAddress
	}
	if !plainAddressChars(address) {
		return "", ErrNotKindleAddress
	}

	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Name != "" || parsed.Address != address {
		return "", ErrNotKindleAddress
	}

	at := strings.LastIndex(address, "@")
	if at <= 0 {
		return "", ErrNotKindleAddress
	}
	domain := address[at+1:]
	for _, allowed := range kindleDomains {
		if domain == allowed {
			return address, nil
		}
	}
	return "", ErrNotKindleAddress
}

// plainAddressChars reports whether s is printable ASCII with none of the
// characters that would make it more than one bare address.
func plainAddressChars(s string) bool {
	for _, r := range s {
		if r <= ' ' || r > '~' || strings.ContainsRune(`<>,"`, r) {
			return false
		}
	}
	return true
}

// KindleSenderDomain is the domain every reader's sender address is on:
// kindle.sender_domain when it is set, and otherwise the domain email.from
// sends from. Empty when neither names one, which leaves Kindle sending off.
func KindleSenderDomain() string {
	if domain := strings.ToLower(strings.TrimSpace(viper.GetString("kindle.sender_domain"))); domain != "" {
		return domain
	}
	from := strings.TrimSpace(viper.GetString("email.from"))
	if parsed, err := mail.ParseAddress(from); err == nil {
		from = parsed.Address
	}
	if at := strings.LastIndex(from, "@"); at >= 0 && at+1 < len(from) {
		return strings.ToLower(from[at+1:])
	}
	return ""
}

// KindleSenderAddress is the address a reader with this sender id sends from,
// or "" when there is no domain to put it on.
func KindleSenderAddress(senderID string) string {
	domain := KindleSenderDomain()
	if senderID == "" || domain == "" {
		return ""
	}
	return senderID + "@" + domain
}

// LogKindleSenderDomain says once, at startup, which domain the sender
// addresses are on, so that a production log answers the question without
// anyone having to read the configuration.
func LogKindleSenderDomain() {
	if domain := KindleSenderDomain(); domain != "" {
		logging.Infof("Send to Kindle: sender addresses are on %s", domain)
		return
	}
	logging.Warn("Send to Kindle: no sender domain (neither kindle.sender_domain nor email.from names one); sending is off")
}

// Attachment is a file a message carries.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// KindleDelivery is one book on its way to one Kindle.
type KindleDelivery struct {
	// From is the reader's own sender address, on the envelope and in the
	// header alike.
	From string
	// To is their Send-to-Kindle address.
	To string
	// Title is the book's title, which becomes the subject.
	Title string
	Book  Attachment
}

// SendToKindle builds the message and hands it to the relay. A nil error means
// the relay accepted it, which is all anyone here can know.
func SendToKindle(d *KindleDelivery) error {
	msg, err := buildKindleMessage(d, time.Now())
	if err != nil {
		return err
	}
	return send(d.From, d.To, msg)
}

// buildKindleMessage is the whole message: a line of text saying what it is,
// then the book.
func buildKindleMessage(d *KindleDelivery, now time.Time) ([]byte, error) {
	if len(d.Book.Data) > MaxKindleBookBytes {
		return nil, ErrBookTooLarge
	}
	if d.From == "" || d.To == "" {
		return nil, errors.New("a Kindle delivery needs both a sender and a recipient")
	}

	boundary, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("generating a MIME boundary: %w", err)
	}

	subject := d.Title
	if strings.TrimSpace(subject) == "" {
		subject = d.Book.Filename
	}
	from := mail.Address{Name: productName(), Address: d.From}
	to := mail.Address{Address: d.To}

	var b bytes.Buffer
	// Sized once for the base64 book, its line breaks and the headers, rather
	// than doubled a dozen times on the way to 30 MB.
	b.Grow(len(d.Book.Data)*4/3 + len(d.Book.Data)/38 + headerRoom)
	writeHeader(&b, "From", from.String())
	writeHeader(&b, "To", to.String())
	writeHeader(&b, "Date", now.Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", messageID(from.Address, now))
	writeFoldedHeader(&b, "Subject", mime.QEncoding.Encode("UTF-8", subject))
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString("\r\n")

	// Amazon reads nothing but the attachment; the line is for a reader who
	// finds the message somewhere else — a sent folder, a bounce.
	writePart(&b, boundary, "text/plain; charset=UTF-8", subject+"\r\n\r\n"+productName()+"\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	contentType := d.Book.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := capFilename(d.Book.Filename, maxFilenameRunes)
	writeFoldedHeader(&b, "Content-Type", contentType+`; name="`+asciiFilename(filename)+`"`)
	writeFoldedHeader(&b, "Content-Disposition", attachmentDisposition(filename))
	writeHeader(&b, "Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")
	b.WriteString(base64Lines(d.Book.Data))
	fmt.Fprintf(&b, "--%s--\r\n", boundary)

	return b.Bytes(), nil
}

// attachmentDisposition names the file twice. The plain filename= is ASCII,
// for a client that knows nothing newer; filename*= is RFC 2231's encoded
// form, which a client that reads it prefers, and which carries a Cyrillic
// name intact.
//
// The encoded form of a long name runs to hundreds of characters, so it is
// cut into RFC 2231 continuations (filename*0*=, filename*1*=, …), each short
// enough for a line of its own once the header is folded.
func attachmentDisposition(filename string) string {
	parts := []string{dispositionAttachment, `filename="` + asciiFilename(filename) + `"`}
	chunks := splitEncoded(rfc2231Encode(filename), continuationWidth)
	if len(chunks) == 1 {
		parts = append(parts, "filename*=UTF-8''"+chunks[0])
	} else {
		for i, chunk := range chunks {
			value := chunk
			if i == 0 {
				value = "UTF-8''" + chunk
			}
			parts = append(parts, fmt.Sprintf("filename*%d*=%s", i, value))
		}
	}
	return strings.Join(parts, "; ")
}

// dispositionAttachment says the part is a file to keep, not to show inline.
const dispositionAttachment = "attachment"

// Header line limits. RFC 5322 forbids a physical line over 998 characters and
// asks for 78; an unfolded header broke the first for a long Cyrillic title,
// and a server enforcing it refuses the whole message.
const (
	foldWidth         = 78
	continuationWidth = 60
	// maxFilenameRunes caps the attachment's name. A title may run to 512
	// characters; Kindle names the document from the EPUB's own metadata, so
	// nothing is lost by a shorter file name.
	maxFilenameRunes = 100
)

// writeFoldedHeader writes a header, folding it at spaces so that its lines
// stay within foldWidth where the value allows. A fold is a CRLF before an
// existing space, which unfolding takes out again: the value read back is the
// value written. Encoded words carry no spaces of their own and Go separates
// them with one, so a Q-encoded subject folds between words.
func writeFoldedHeader(b *bytes.Buffer, name, value string) {
	line := name + ":"
	for _, word := range strings.Split(value, " ") {
		if word != "" && len(line)+1+len(word) > foldWidth && strings.TrimSpace(line) != name+":" {
			b.WriteString(line)
			b.WriteString("\r\n")
			line = ""
		}
		line += " " + word
	}
	b.WriteString(line)
	b.WriteString("\r\n")
}

// capFilename shortens a name to at most limit runes, keeping its extension.
func capFilename(name string, limit int) string {
	runes := []rune(name)
	if len(runes) <= limit {
		return name
	}
	ext := ""
	if dot := strings.LastIndex(name, "."); dot > 0 && len([]rune(name[dot:])) < limit/2 {
		ext = name[dot:]
	}
	keep := limit - len([]rune(ext))
	return string([]rune(strings.TrimSuffix(name, ext))[:keep]) + ext
}

// splitEncoded cuts a percent-encoded string into pieces of at most width
// characters, never inside a %XX triplet.
func splitEncoded(s string, width int) []string {
	var chunks []string
	for len(s) > width {
		cut := width
		if i := strings.LastIndexByte(s[:cut], '%'); i > cut-3 {
			cut = i
		}
		chunks = append(chunks, s[:cut])
		s = s[cut:]
	}
	return append(chunks, s)
}

// asciiFilename is the name with everything a quoted header value cannot hold
// as-is replaced: non-ASCII, controls, the quote and the backslash.
func asciiFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < ' ' || r > '~' || r == '"' || r == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// rfc2231Encode percent-encodes every byte outside RFC 2231's attribute-char.
func rfc2231Encode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isAttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// isAttrChar is RFC 2231's attribute-char: a token character other than the
// asterisk, the apostrophe and the percent sign.
func isAttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}
