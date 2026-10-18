package email

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// A book on its way to a Kindle is a different message from the two this
// package sent before: it carries a file, and it goes out under the reader's
// own address rather than the installation's. Everything below parses the
// message the way a receiving server would and checks what arrives.

func kindleSample(t *testing.T) KindleDelivery {
	t.Helper()

	// Random bytes rather than a string: a book is binary, and an encoder that
	// only survives text would pass on "hello" and mangle an EPUB.
	book := make([]byte, 64<<10+17)
	if _, err := rand.Read(book); err != nil {
		t.Fatalf("generating a book: %v", err)
	}
	return KindleDelivery{
		From:  "5f0c7a52-3c1e-4bb4-9a77-0b3c1f6a2d10@booksdump.com",
		To:    "reader_42@kindle.com",
		Title: "Полдень, XXII век",
		Book: Attachment{
			Filename:    "polden_xxii_vek.epub",
			ContentType: "application/epub+zip",
			Data:        book,
		},
	}
}

func buildKindle(t *testing.T, d *KindleDelivery) []byte {
	t.Helper()

	msg, err := buildKindleMessage(d, NOW)
	if err != nil {
		t.Fatalf("building the message: %v", err)
	}
	return msg
}

// kindleParts parses a multipart/mixed message into its parts, decoded.
type kindlePart struct {
	header mail.Header
	body   []byte
}

func kindleParts(t *testing.T, msg []byte) (mail.Header, []kindlePart) {
	t.Helper()

	parsed, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("the message does not parse as an email: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parsing Content-Type: %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("Content-Type is %q, want multipart/mixed", mediaType)
	}

	var out []kindlePart
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading a part: %v", err)
		}
		raw, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("reading a part's body: %v", err)
		}
		if part.Header.Get("Content-Transfer-Encoding") == "base64" {
			raw = decodeBase64Lines(t, raw)
		}
		out = append(out, kindlePart{header: mail.Header(part.Header), body: raw})
	}
	return parsed.Header, out
}

func decodeBase64Lines(t *testing.T, raw []byte) []byte {
	t.Helper()

	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("a base64 line runs to %d characters, past RFC 2045's 76", len(line))
		}
	}
	decoded, err := io.ReadAll(newBase64Decoder(raw))
	if err != nil {
		t.Fatalf("decoding base64: %v", err)
	}
	return decoded
}

// newBase64Decoder reads a base64 body; the standard decoder skips the CR LF
// line breaks on its own.
func newBase64Decoder(raw []byte) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, bytes.NewReader(raw))
}

// The book that arrives is the book that was sent, byte for byte.
func TestKindleAttachmentRoundTrips(t *testing.T) {
	d := kindleSample(t)
	_, parts := kindleParts(t, buildKindle(t, &d))

	if len(parts) != 2 {
		t.Fatalf("the message has %d parts, want a body and the book", len(parts))
	}
	book := parts[1]
	if !bytes.Equal(book.body, d.Book.Data) {
		t.Fatalf("the attachment came back as %d bytes that differ from the %d sent",
			len(book.body), len(d.Book.Data))
	}
	if got := book.header.Get("Content-Type"); !strings.HasPrefix(got, "application/epub+zip") {
		t.Errorf("the attachment's Content-Type is %q", got)
	}
}

// The file is an attachment with a name, given twice: RFC 2231's encoded form
// for a client that reads it, and plain ASCII for one that does not.
func TestKindleAttachmentIsNamedBothWays(t *testing.T) {
	d := kindleSample(t)
	d.Book.Filename = "Полдень.epub"
	_, parts := kindleParts(t, buildKindle(t, &d))

	disposition := parts[1].header.Get("Content-Disposition")
	for _, r := range disposition {
		if r > 127 {
			t.Fatalf("Content-Disposition carries raw non-ASCII: %q", disposition)
		}
	}
	if !strings.Contains(disposition, "filename*=UTF-8''") {
		t.Errorf("no RFC 2231 filename: %q", disposition)
	}
	if !strings.Contains(disposition, `filename="`) {
		t.Errorf("no ASCII fallback filename: %q", disposition)
	}

	kind, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		t.Fatalf("parsing Content-Disposition %q: %v", disposition, err)
	}
	if kind != "attachment" {
		t.Errorf("disposition is %q, want attachment", kind)
	}
	// Go's parser prefers the extended form, as RFC 2231 says it should.
	if params["filename"] != d.Book.Filename {
		t.Errorf("the name decodes as %q, want %q", params["filename"], d.Book.Filename)
	}
}

// An ASCII name, which is what DownloadName produces, survives unchanged in
// both forms.
func TestKindleASCIINameIsKept(t *testing.T) {
	d := kindleSample(t)
	_, parts := kindleParts(t, buildKindle(t, &d))

	disposition := parts[1].header.Get("Content-Disposition")
	if !strings.Contains(disposition, `filename="polden_xxii_vek.epub"`) {
		t.Errorf("ASCII fallback lost the name: %q", disposition)
	}
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		t.Fatal(err)
	}
	if params["filename"] != d.Book.Filename {
		t.Errorf("name decodes as %q", params["filename"])
	}
}

// From is the reader's own sender address, the one they put on Amazon's
// approved list; the subject is the book's title, encoded.
func TestKindleHeaders(t *testing.T) {
	d := kindleSample(t)
	h, _ := kindleParts(t, buildKindle(t, &d))

	from, err := mail.ParseAddress(h.Get("From"))
	if err != nil {
		t.Fatalf("parsing From: %v", err)
	}
	if from.Address != d.From {
		t.Errorf("From is %q, want the reader's sender %q", from.Address, d.From)
	}
	to, err := mail.ParseAddress(h.Get("To"))
	if err != nil {
		t.Fatalf("parsing To: %v", err)
	}
	if to.Address != d.To {
		t.Errorf("To is %q, want %q", to.Address, d.To)
	}

	raw := h.Get("Subject")
	decoded, err := new(mime.WordDecoder).DecodeHeader(raw)
	if err != nil {
		t.Fatalf("decoding the subject: %v", err)
	}
	if decoded != d.Title {
		t.Errorf("subject is %q, want the title %q", decoded, d.Title)
	}
	if h.Get("Date") == "" || h.Get("Message-ID") == "" || h.Get("MIME-Version") != "1.0" {
		t.Errorf("a required header is missing: %v", h)
	}
	if !strings.HasSuffix(h.Get("Message-ID"), "@booksdump.com>") {
		t.Errorf("Message-ID does not name the sending domain: %q", h.Get("Message-ID"))
	}
}

// A book is refused before anything is built when MailerSend would refuse it.
func TestKindleRefusesABookTooLargeToMail(t *testing.T) {
	d := kindleSample(t)
	d.Book.Data = make([]byte, MaxKindleBookBytes+1)

	if _, err := buildKindleMessage(&d, NOW); err == nil {
		t.Fatal("a book over the limit was built into a message")
	}
}

func TestKindleAddressValidation(t *testing.T) {
	good := []struct{ in, want string }{
		{"reader@kindle.com", "reader@kindle.com"},
		{"  Reader_42@Kindle.COM ", "reader_42@kindle.com"},
		{"reader.name@free.kindle.com", "reader.name@free.kindle.com"},
		{"READER@FREE.KINDLE.COM\t", "reader@free.kindle.com"},
		{"first.last_123@kindle.com", "first.last_123@kindle.com"},
		{"x+tag@kindle.com", "x+tag@kindle.com"},
		{"\u00a0reader@kindle.com\u00a0", "reader@kindle.com"},
	}
	for _, tc := range good {
		got, err := NormalizeKindleAddress(tc.in)
		if err != nil {
			t.Errorf("%q was refused: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q normalized to %q, want %q", tc.in, got, tc.want)
		}
	}

	bad := []string{
		"",
		"   ",
		"reader@gmail.com",
		"reader@kindle.com.evil.test",
		"reader@notkindle.com",
		"reader@kindle.co",
		"@kindle.com",
		"reader@@kindle.com",
		"Reader <reader@kindle.com>",
		"reader@kindle.com, other@kindle.com",
		"reader kindle@kindle.com",
		"reader@kindle.com\r\nBcc: victim@example.test",
		strings.Repeat("a", 250) + "@kindle.com",
	}
	for _, in := range bad {
		if got, err := NormalizeKindleAddress(in); err == nil {
			t.Errorf("%q was accepted as %q", in, got)
		}
	}
}

func TestKindleSenderAddress(t *testing.T) {
	t.Cleanup(func() {
		viper.Set("kindle.sender_domain", "")
		viper.Set("email.from", "")
	})

	viper.Set("email.from", "no-reply@booksdump.com")
	viper.Set("kindle.sender_domain", "")
	if got := KindleSenderAddress("abc"); got != "abc@booksdump.com" {
		t.Errorf("with no domain configured the sender is %q", got)
	}

	viper.Set("kindle.sender_domain", " Send.Example.TEST ")
	if got := KindleSenderAddress("abc"); got != "abc@send.example.test" {
		t.Errorf("with a domain configured the sender is %q", got)
	}

	viper.Set("kindle.sender_domain", "")
	viper.Set("email.from", "")
	if got := KindleSenderAddress("abc"); got != "" {
		t.Errorf("with nothing to take a domain from the sender is %q, want none", got)
	}
	if got := KindleSenderAddress(""); got != "" {
		t.Errorf("a user with no sender id got %q", got)
	}
}

// The envelope sender — what MAIL FROM says, which is what Amazon checks
// against the approved list — is the reader's own address, not the
// installation's. Proved on the wire, against the fake server.
func TestSendToKindleUsesTheReadersAddressOnTheEnvelope(t *testing.T) {
	cert, pool := selfSigned(t)
	server := &fakeServer{offerSTARTTLS: true, cert: cert}
	address := server.listen(t)

	previous := mailConnection
	t.Cleanup(func() { mailConnection = previous })
	mailConnection = func() (*smtp.Client, error) {
		host, port, _ := net.SplitHostPort(address)
		return dial(address, host, port, &tls.Config{
			ServerName: "127.0.0.1",
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		})
	}

	d := kindleSample(t)
	if err := SendToKindle(&d); err != nil {
		t.Fatalf("sending: %v", err)
	}
	server.wait(t)

	if server.mailFrom != "MAIL FROM:<"+d.From+">" {
		t.Errorf("the envelope sender is %q, want the reader's %q", server.mailFrom, d.From)
	}
	if server.rcptTo != "RCPT TO:<"+d.To+">" {
		t.Errorf("the envelope recipient is %q, want %q", server.rcptTo, d.To)
	}
	_, parts := kindleParts(t, server.data)
	if len(parts) != 2 || !bytes.Equal(parts[1].body, d.Book.Data) {
		t.Error("the book that reached the server is not the book that was sent")
	}
}
