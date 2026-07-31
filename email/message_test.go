package email

import (
	"encoding/base64"
	"html"
	"mime"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// Composing a message is separate from sending one so that all of this can be
// checked without a mail server. Every case below is something that was wrong
// and went out over the wire unnoticed.

// NOW is the moment these messages are built. Derived rather than written down,
// so the assertions cannot start failing when a date on the calendar passes.
var NOW = time.Now()

// sample is a real password reset: the wording comes from the same place the
// application takes it from, so these check the message that actually goes out
// rather than one written for the test.
func sample() SendType {
	data := SendType{
		Email:       "reader@example.test",
		From:        "no-reply@booksdump.com",
		ProductName: "Booksdump",
		URL:         "https://booksdump.com/change-password/abc123",
	}
	wording := resolve(kindReset)
	wording.apply(&data)
	return data
}

func build(t *testing.T, data *SendType) string {
	t.Helper()

	msg, err := buildMessage(data, notificationTemplate, NOW)
	if err != nil {
		t.Fatalf("building the message: %v", err)
	}
	return string(msg)
}

// headers returns the part before the blank line, parsed.
func headers(t *testing.T, msg string) mail.Header {
	t.Helper()

	parsed, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		t.Fatalf("the message does not parse as an email: %v", err)
	}
	return parsed.Header
}

// A header must be ASCII. A Russian subject went out as raw UTF-8 bytes, which
// is not a header at all, and arrived as mojibake wherever it was not guessed.
func TestSubjectIsEncoded(t *testing.T) {
	data := sample()
	msg := build(t, &data)

	raw := headers(t, msg).Get("Subject")
	for _, r := range raw {
		if r > 127 {
			t.Fatalf("the subject went out unencoded: %q", raw)
		}
	}

	decoded, err := new(mime.WordDecoder).DecodeHeader(raw)
	if err != nil {
		t.Fatalf("decoding the subject: %v", err)
	}
	if decoded != data.Subject {
		t.Errorf("the subject came back as %q, not %q", decoded, data.Subject)
	}
}

// An ASCII subject needs no encoding and should not be dressed in any.
func TestPlainSubjectIsLeftAlone(t *testing.T) {
	data := sample()
	data.Subject = "Password reset"

	if got := headers(t, build(t, &data)).Get("Subject"); got != data.Subject {
		t.Errorf("subject is %q, want %q", got, data.Subject)
	}
}

// Date is required by RFC 5322 and was missing, as was Message-ID. Both absent
// is a combination spam filters are entitled to be unkind about.
func TestCarriesTheHeadersEveryMessageMustHave(t *testing.T) {
	h := headers(t, build(t, ptr(sample())))

	when, err := h.Date()
	if err != nil {
		t.Fatalf("reading the Date header: %v", err)
	}
	if when.Unix() != NOW.Unix() {
		t.Errorf("Date says %v, want %v", when, NOW)
	}

	id := h.Get("Message-ID")
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, ">") {
		t.Errorf("Message-ID is not in angle brackets: %q", id)
	}
	if !strings.HasSuffix(id, "@booksdump.com>") {
		t.Errorf("Message-ID does not name the sending domain: %q", id)
	}
}

// Two messages must not share a name.
func TestEveryMessageGetsItsOwnID(t *testing.T) {
	first := headers(t, build(t, ptr(sample()))).Get("Message-ID")
	second := headers(t, build(t, ptr(sample()))).Get("Message-ID")

	if first == second {
		t.Errorf("two messages share the id %q", first)
	}
}

// The message used to be text/html and nothing else, so a reader whose client
// shows no HTML got markup or nothing.
func TestOffersATextAlternative(t *testing.T) {
	data := sample()
	msg := build(t, &data)

	contentType := headers(t, msg).Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/alternative") {
		t.Fatalf("Content-Type is %q", contentType)
	}

	textPart, htmlPart := parts(t, msg)
	if !strings.Contains(textPart, data.Message) {
		t.Errorf("the text part does not carry the message:\n%s", textPart)
	}
	if !strings.Contains(textPart, data.URL) {
		t.Errorf("the text part does not carry the link:\n%s", textPart)
	}
	if strings.Contains(textPart, "<table") {
		t.Error("the text part carries markup")
	}
	if !strings.Contains(htmlPart, "<table") {
		t.Error("the html part carries no markup")
	}
}

// The text part comes first: a client shows the last alternative it understands,
// so the richest one has to be last or nobody sees it.
func TestTextComesBeforeHTML(t *testing.T) {
	msg := build(t, ptr(sample()))

	textAt := strings.Index(msg, "text/plain")
	htmlAt := strings.Index(msg, "text/html")
	if textAt < 0 || htmlAt < 0 {
		t.Fatal("one of the parts is missing")
	}
	if textAt > htmlAt {
		t.Error("the html part is offered before the text one")
	}
}

// Both spellings of the email go through the same template, so what tells them
// apart has to actually reach it.
func TestTheWarningAppearsOnlyWhereItIsGiven(t *testing.T) {
	warned := sample()
	_, htmlPart := parts(t, build(t, &warned))
	if !shows(htmlPart, warned.Warning) {
		t.Error("the reset warning is missing from the email that carries one")
	}

	plain := sample()
	plain.Warning = ""
	_, htmlPart = parts(t, build(t, &plain))
	if shows(htmlPart, warned.Warning) {
		t.Error("an email with no warning shows one anyway")
	}
	if strings.Contains(htmlPart, "fff3cd") {
		t.Error("the warning's box is drawn with nothing in it")
	}
}

func TestTheFooterAppearsOnlyWhereItIsGiven(t *testing.T) {
	data := sample()
	_, htmlPart := parts(t, build(t, &data))
	if !shows(htmlPart, data.Footer) {
		t.Error("the footer is missing")
	}

	none := sample()
	none.Footer = ""
	_, htmlPart = parts(t, build(t, &none))
	if shows(htmlPart, data.Footer) {
		t.Error("an email with no footer shows one anyway")
	}
}

// The link is the whole point of both emails.
func TestTheLinkIsBothTheButtonAndCopyable(t *testing.T) {
	data := sample()
	_, htmlPart := parts(t, build(t, &data))

	if !strings.Contains(htmlPart, `href="`+data.URL+`"`) {
		t.Error("the button does not point at the link")
	}
	// Once as the href, once as text to copy.
	if strings.Count(htmlPart, data.URL) < 2 {
		t.Error("the link is not offered in a form that can be copied")
	}
}

// The template is data-driven, and Go's html/template escapes what it inserts.
func TestContentIsEscaped(t *testing.T) {
	data := sample()
	data.Title = `<script>alert(1)</script>`

	_, htmlPart := parts(t, build(t, &data))
	if strings.Contains(htmlPart, "<script>alert(1)</script>") {
		t.Error("a title was inserted into the page as markup")
	}
}

func TestAnUnknownTemplateIsAnError(t *testing.T) {
	data := sample()
	if _, err := buildMessage(&data, "no-such-template.gohtml", NOW); err == nil {
		t.Error("accepted a template that does not exist")
	}
}

// ptr makes a value addressable, so a sample can be handed straight over.
func ptr(data SendType) *SendType { //nolint:gocritic // the copy is the point: an addressable one.
	return &data
}

// shows reports whether the rendered page says something, comparing against the
// text a reader sees rather than the markup: html/template escapes what it
// inserts, so a wording carrying an apostrophe arrives as didn&#39;t.
func shows(rendered, want string) bool {
	return strings.Contains(html.UnescapeString(rendered), want)
}

// parts splits a built message and returns the decoded text and html bodies.
func parts(t *testing.T, msg string) (textPart, htmlPart string) {
	t.Helper()

	parsed, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		t.Fatalf("the message does not parse as an email: %v", err)
	}
	_, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parsing the content type: %v", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatal("the message names no boundary")
	}

	start := strings.Index(msg, "--"+boundary)
	if start < 0 {
		t.Fatal("the body carries no part at all")
	}
	body := msg[start:]
	sections := strings.Split(body, "--"+boundary)
	for _, section := range sections {
		header, encoded, found := strings.Cut(section, "\r\n\r\n")
		if !found {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(
			strings.ReplaceAll(strings.TrimSpace(encoded), "\r\n", ""),
		)
		if err != nil {
			continue
		}
		switch {
		case strings.Contains(header, "text/plain"):
			textPart = string(decoded)
		case strings.Contains(header, "text/html"):
			htmlPart = string(decoded)
		}
	}
	if textPart == "" || htmlPart == "" {
		t.Fatalf("could not find both parts (text %d bytes, html %d bytes)",
			len(textPart), len(htmlPart))
	}
	return textPart, htmlPart
}
