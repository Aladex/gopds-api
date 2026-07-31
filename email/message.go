package email

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"mime"
	"net/mail"
	"strings"
	"time"

	assets "gopds-api"
)

// Composing a message is kept apart from sending one, and happens first.
//
// Apart, because everything that can be got wrong about a message — an
// unencoded subject, a missing Date, a body no text-only client can read — is
// then checkable without a mail server. First, because the order used to be the
// other way round: the SMTP conversation was opened and carried as far as DATA
// before the template was so much as read, so a template that failed to parse
// left the connection stranded mid-message.

// messageIDDomain is the fallback for an address that carries no domain, which
// only a misconfigured instance would have.
const messageIDDomain = "localhost"

// buildMessage renders one email in full: headers, a plain-text part and an
// HTML part. The two parts say the same thing, which is the point — a client
// that shows the text one must not be showing less than the other.
func buildMessage(data *SendType, templateName string, now time.Time) ([]byte, error) {
	html, err := renderHTML(data, templateName)
	if err != nil {
		return nil, err
	}

	from := mail.Address{Name: "BOOKSDUMP", Address: data.From}
	to := mail.Address{Address: data.Email}

	// A boundary no body can contain: it is random, and the parts are text.
	boundary, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("generating a MIME boundary: %w", err)
	}

	var b bytes.Buffer
	// Written in a fixed order rather than from a map, whose iteration order
	// changes between runs and made two identical messages look different.
	writeHeader(&b, "From", from.String())
	writeHeader(&b, "To", to.String())
	// Date is required by RFC 5322 and was absent. Message-ID was too, and both
	// missing is a combination spam filters are entitled to be unkind about.
	writeHeader(&b, "Date", now.Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", messageID(from.Address, now))
	// Encoded rather than written as-is: a header must be ASCII, so a Russian
	// subject went out as raw UTF-8 bytes and arrived as mojibake.
	writeHeader(&b, "Subject", mime.QEncoding.Encode("UTF-8", data.Subject))
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")

	// Least capable part first, as multipart/alternative asks: a client shows
	// the last part it understands.
	writePart(&b, boundary, "text/plain; charset=UTF-8", plainText(data))
	writePart(&b, boundary, "text/html; charset=UTF-8", string(html))
	fmt.Fprintf(&b, "--%s--\r\n", boundary)

	return b.Bytes(), nil
}

func writeHeader(b *bytes.Buffer, name, value string) {
	fmt.Fprintf(b, "%s: %s\r\n", name, value)
}

func writePart(b *bytes.Buffer, boundary, contentType, body string) {
	fmt.Fprintf(b, "--%s\r\n", boundary)
	writeHeader(b, "Content-Type", contentType)
	// Quoted-printable would be the tidier encoding, but base64 avoids having
	// to reason about line lengths and about a line of body that begins with a
	// dot, which SMTP would otherwise eat.
	writeHeader(b, "Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")
	b.WriteString(base64Lines(body))
	b.WriteString("\r\n")
}

// renderHTML executes the template for this kind of email.
func renderHTML(data *SendType, templateName string) ([]byte, error) {
	asset, err := assets.Assets.ReadFile("email/templates/" + templateName)
	if err != nil {
		return nil, fmt.Errorf("reading email template %s: %w", templateName, err)
	}

	tpl, err := template.New(templateName).Parse(string(asset))
	if err != nil {
		return nil, fmt.Errorf("parsing email template %s: %w", templateName, err)
	}

	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		return nil, fmt.Errorf("rendering email template %s: %w", templateName, err)
	}
	return out.Bytes(), nil
}

// plainText is the same email for a reader whose client shows no HTML.
//
// Built from the fields rather than by stripping tags out of the rendered page:
// the parts that matter are already separate values, and a stripped page would
// carry the layout's stray words and none of its meaning.
func plainText(data *SendType) string {
	var b strings.Builder
	b.WriteString(data.Title)
	b.WriteString("\r\n\r\n")
	b.WriteString(data.Message)
	b.WriteString("\r\n\r\n")
	b.WriteString(data.URL)
	b.WriteString("\r\n\r\n")
	if data.Warning != "" {
		b.WriteString(data.Warning)
		b.WriteString("\r\n\r\n")
	}
	b.WriteString(data.Thanks)
	b.WriteString("\r\n")
	return b.String()
}

// messageID gives every message a name of its own, as RFC 5322 asks.
func messageID(from string, now time.Time) string {
	domain := messageIDDomain
	if at := strings.LastIndex(from, "@"); at >= 0 && at+1 < len(from) {
		domain = from[at+1:]
	}

	token, err := randomToken()
	if err != nil {
		// Randomness failing is not a reason to drop the message; the time
		// alone still names it apart from its neighbors.
		token = fmt.Sprintf("%d", now.UnixNano())
	}
	return fmt.Sprintf("<%d.%s@%s>", now.UnixNano(), token, domain)
}

func randomToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// base64Lines encodes a part and wraps it at 76 characters, which is the limit
// RFC 2045 sets and which some servers enforce by refusing the message.
func base64Lines(s string) string {
	const width = 76
	encoded := base64.StdEncoding.EncodeToString([]byte(s))

	var b strings.Builder
	for start := 0; start < len(encoded); start += width {
		end := start + width
		if end > len(encoded) {
			end = len(encoded)
		}
		b.WriteString(encoded[start:end])
		b.WriteString("\r\n")
	}
	return b.String()
}
