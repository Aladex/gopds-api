// Package tgtest is the fake Telegram every bot test talks to.
//
// FakeBotAPI is an HTTP server that answers Bot API methods the way Telegram
// does and records every call, so a test can drive a real update through the
// real webhook route and assert on what the bot actually sent: the method,
// its fields, its keyboard, its file. Rules script the unhappy answers — 429,
// 403, "message is not modified", a hang, a dropped connection — so delivery
// and error handling are tested end to end, not only through their parts.
//
// The package knows nothing about this service's bots; it only speaks the
// Bot API. Wiring a bot to it is one option: tgapi.Endpoint{ServerURL:
// fake.URL()}.
package tgtest

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// firstMessageID is where the fake's message ids start; any value works, a
// non-trivial one keeps ids from being confused with counts in assertions.
const firstMessageID = 100

// Call is one request a bot made to the fake.
type Call struct {
	Token  string
	Method string
	// Params holds every non-file field as sent. The SDK encodes structured
	// fields (reply_markup, link_preview_options, …) as JSON strings; read
	// them with Decode.
	Params map[string]string
	// Files holds the uploaded parts by field name.
	Files map[string]UploadedFile
	// MessageID is the message_id the fake answered with when the call
	// created or edited a message, else 0. It is set only once the call is
	// answered.
	MessageID int64
	// Answered says the fake finished writing its reply; Status is the HTTP
	// status it wrote. A call whose reply hangs or drops the connection is
	// received but never answered — for the bot, its outcome is unknown.
	Answered bool
	Status   int
	At       time.Time
}

// UploadedFile is one multipart file part.
type UploadedFile struct {
	Filename string
	Size     int
	SHA256   string
	Data     []byte
}

// Decode unmarshals the JSON field name into v.
func (c *Call) Decode(name string, v any) error {
	raw, ok := c.Params[name]
	if !ok {
		return fmt.Errorf("call %s has no field %q", c.Method, name)
	}
	return json.Unmarshal([]byte(raw), v)
}

// Int64 returns the integer field name, or 0.
func (c *Call) Int64(name string) int64 {
	v, _ := strconv.ParseInt(c.Params[name], 10, 64)
	return v
}

// Reply writes the fake's answer to one call.
type Reply func(w http.ResponseWriter, r *http.Request, c *Call)

// Rule overrides the default answer for matching calls. Rules are tried in
// the order they were added; the first that matches and has uses left wins.
type Rule struct {
	// Method matches the Bot API method name, case-insensitively; empty
	// matches every method.
	Method string
	// Match narrows the rule further; nil matches every call of Method.
	Match func(Call) bool
	Reply Reply
	// Times is how many calls the rule answers; 0 means every call.
	Times int

	used int
}

// FakeBotAPI is a scripted, recording Bot API server.
type FakeBotAPI struct {
	srv *httptest.Server

	mu   sync.Mutex
	cond *sync.Cond
	// calls is the journal: every call is entered when its body has been
	// read, before any reply runs, so a reply that hangs or aborts cannot hide
	// a request that reached the server.
	calls  []*Call
	rules  []*Rule
	nextID int64
	// pinned is each chat's most recent pinned message, as getChat reports
	// it.
	pinned map[int64]int64
}

// NewFakeBotAPI starts a fake on a local port; it stops when the test ends.
func NewFakeBotAPI(t testing.TB) *FakeBotAPI {
	t.Helper()
	f := &FakeBotAPI{nextID: firstMessageID}
	f.cond = sync.NewCond(&f.mu)
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// NewFakeBotAPIFromServer wraps a server the caller configured itself (for
// example HTTP/2 over TLS). The caller sets srv.Config.Handler to the
// returned fake's Handler before starting it.
func NewFakeBotAPIFromServer(t testing.TB, srv *httptest.Server) *FakeBotAPI {
	t.Helper()
	f := &FakeBotAPI{nextID: firstMessageID, srv: srv}
	f.cond = sync.NewCond(&f.mu)
	return f
}

// Handler is the fake's HTTP handler.
func (f *FakeBotAPI) Handler() http.Handler { return http.HandlerFunc(f.serve) }

// URL is the base URL to give a bot as its Bot API server.
func (f *FakeBotAPI) URL() string { return f.srv.URL }

// Server is the underlying test server.
func (f *FakeBotAPI) Server() *httptest.Server { return f.srv }

// On adds a rule.
func (f *FakeBotAPI) On(r Rule) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rule := r
	f.rules = append(f.rules, &rule)
}

// Calls returns the received calls of method (all calls when method is
// empty), oldest first — answered or not.
func (f *FakeBotAPI) Calls(method string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.matching(method, false)
}

// AnsweredCalls returns the calls of method the fake has finished answering.
func (f *FakeBotAPI) AnsweredCalls(method string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.matching(method, true)
}

func (f *FakeBotAPI) matching(method string, answeredOnly bool) []Call {
	var out []Call
	for _, c := range f.calls {
		if (method == "" || strings.EqualFold(c.Method, method)) && (!answeredOnly || c.Answered) {
			out = append(out, *c)
		}
	}
	return out
}

// WaitCalls waits until at least n calls of method were received and
// returns all received ones; it fails the test after timeout. A received
// call may still be waiting for its answer: use WaitAnswered when the test
// needs what the answer carries, such as the message id.
func (f *FakeBotAPI) WaitCalls(t testing.TB, method string, n int, timeout time.Duration) []Call {
	t.Helper()
	return f.wait(t, method, n, timeout, false)
}

// WaitAnswered waits until at least n calls of method were answered and
// returns the answered ones.
func (f *FakeBotAPI) WaitAnswered(t testing.TB, method string, n int, timeout time.Duration) []Call {
	t.Helper()
	return f.wait(t, method, n, timeout, true)
}

func (f *FakeBotAPI) wait(t testing.TB, method string, n int, timeout time.Duration, answeredOnly bool) []Call {
	t.Helper()
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() {
		f.mu.Lock()
		f.cond.Broadcast()
		f.mu.Unlock()
	})
	defer timer.Stop()

	f.mu.Lock()
	defer f.mu.Unlock()
	for {
		got := f.matching(method, answeredOnly)
		if len(got) >= n {
			return got
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("waited %s for %d %s call(s) (answered only: %v), got %d: %s",
				timeout, n, method, answeredOnly, len(got), f.summaryLocked())
			return nil
		}
		f.cond.Wait()
	}
}

// Quiet asserts that exactly want calls of method were received after d —
// answered or not. It is how a test says "and nothing else was sent": a
// request still hanging on its reply counts, because it has reached Telegram.
func (f *FakeBotAPI) Quiet(t testing.TB, method string, want int, d time.Duration) {
	t.Helper()
	time.Sleep(d)
	if got := len(f.Calls(method)); got != want {
		t.Fatalf("want exactly %d %s call(s), got %d: %s", want, method, got, f.summary())
	}
}

func (f *FakeBotAPI) summary() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summaryLocked()
}

func (f *FakeBotAPI) summaryLocked() string {
	names := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		if c.Answered {
			names = append(names, c.Method)
		} else {
			names = append(names, c.Method+"(unanswered)")
		}
	}
	return "[" + strings.Join(names, " ") + "]"
}

func (f *FakeBotAPI) serve(w http.ResponseWriter, r *http.Request) {
	call, err := parseCall(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request: "+err.Error())
		return
	}

	// Into the journal first: the body has reached the server, whatever the
	// reply does next.
	entry := &call
	f.mu.Lock()
	f.calls = append(f.calls, entry)
	reply := f.ruleFor(entry)
	f.cond.Broadcast()
	f.mu.Unlock()
	if reply == nil {
		reply = f.defaultReply
	}

	// The reply works on its own copy; the journal entry learns the outcome
	// only when the reply returns. A reply that panics (an aborted HTTP/2
	// stream) leaves the entry received and unanswered.
	local := call
	sw := &statusWriter{ResponseWriter: w}
	reply(sw, r, &local)

	// A reply that wrote nothing — it hung until the client gave up, or
	// dropped the connection — did not answer.
	f.mu.Lock()
	entry.MessageID = local.MessageID
	entry.Status = sw.status()
	entry.Answered = entry.Status != 0
	f.cond.Broadcast()
	f.mu.Unlock()
}

// statusWriter remembers the status a reply wrote, and still lets a reply
// take over the connection.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) status() int { return w.code }

// Hijack hands the connection to a reply that drops it (HTTP/1.1 only).
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hj.Hijack()
}

func (f *FakeBotAPI) ruleFor(c *Call) Reply {
	for _, rule := range f.rules {
		if rule.Method != "" && !strings.EqualFold(rule.Method, c.Method) {
			continue
		}
		if rule.Times > 0 && rule.used >= rule.Times {
			continue
		}
		if rule.Match != nil && !rule.Match(*c) {
			continue
		}
		rule.used++
		return rule.Reply
	}
	return nil
}

// newMessageID hands out message ids, like a private chat does.
func (f *FakeBotAPI) newMessageID() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return f.nextID
}

// defaultReply answers like a healthy Telegram.
func (f *FakeBotAPI) defaultReply(w http.ResponseWriter, _ *http.Request, c *Call) {
	switch strings.ToLower(c.Method) {
	case "getme":
		writeResult(w, BotUser(c.Token))
	case "sendmessage", "senddocument", "sendphoto", "copymessage", "sendrichmessage":
		c.MessageID = f.newMessageID()
		writeResult(w, messageFor(c))
	case "editmessagetext", "editmessagecaption", "editmessagemedia", "editmessagereplymarkup":
		c.MessageID = c.Int64("message_id")
		writeResult(w, messageFor(c))
	case "pinchatmessage":
		f.pin(c.Int64("chat_id"), c.Int64("message_id"))
		writeResult(w, true)
	case "getchat":
		writeResult(w, f.chatFor(c.Int64("chat_id")))
	case "getwebhookinfo":
		writeResult(w, map[string]any{"url": "", "has_custom_certificate": false, "pending_update_count": 0})
	case "answercallbackquery", "setwebhook", "deletewebhook", "setmycommands", "deletemycommands",
		"sendchataction", "deletemessage", "setmydescription", "setmyshortdescription", "setmyprofilephoto",
		"removemyprofilephoto", "answerinlinequery", "sendmessagedraft", "setchatmenubutton", "unpinchatmessage":
		writeResult(w, true)
	default:
		writeError(w, http.StatusNotFound, "Not Found: method not found")
	}
}

// BotUser is what getMe answers for token: the bot id is the token's prefix,
// as in real tokens.
func BotUser(token string) map[string]any {
	prefix, _, _ := strings.Cut(token, ":")
	id, _ := strconv.ParseInt(prefix, 10, 64)
	return map[string]any{
		"id":         id, //nolint:goconst // a Bot API field name, clearer inline than as a constant
		"is_bot":     true,
		"first_name": "Test Bot",
		"username":   fmt.Sprintf("test%d_bot", id),
	}
}

func messageFor(c *Call) map[string]any {
	chatID, _ := strconv.ParseInt(c.Params["chat_id"], 10, 64)
	msg := map[string]any{
		"message_id": c.MessageID,
		"date":       time.Now().Unix(),
		"chat":       map[string]any{"id": chatID, "type": "private"},
		"from":       BotUser(c.Token),
	}
	if text, ok := c.Params["text"]; ok {
		msg["text"] = text
	}
	if caption, ok := c.Params["caption"]; ok {
		msg["caption"] = caption
	}
	if markup, ok := c.Params["reply_markup"]; ok {
		msg["reply_markup"] = json.RawMessage(markup)
	}
	if rich, ok := c.Params["rich_message"]; ok {
		msg["rich_message"] = richMessageFor(rich, c.Files)
	}
	if doc, ok := c.Files["document"]; ok {
		msg["document"] = map[string]any{
			"file_id":        "doc-" + doc.SHA256[:16],
			"file_unique_id": doc.SHA256[:16],
			"file_name":      doc.Filename,
			"file_size":      doc.Size,
		}
	}
	return msg
}

// The fake's rich photo blocks: their type, and the size it claims.
const (
	richPhoto   = "photo"
	coverWidth  = 600
	coverHeight = 900
)

// richMessageFor is the rich_message Telegram returns for one sent: the
// same blocks, a photo block carrying a file_id — derived from the uploaded
// part's hash, or the file_id it was sent with.
func richMessageFor(raw string, files map[string]UploadedFile) map[string]any {
	var sent struct {
		Blocks []map[string]any `json:"blocks"`
	}
	_ = json.Unmarshal([]byte(raw), &sent)
	blocks := make([]map[string]any, 0, len(sent.Blocks))
	for _, block := range sent.Blocks {
		if block["type"] != richPhoto {
			blocks = append(blocks, block)
			continue
		}
		photo, _ := block[richPhoto].(map[string]any)
		media, _ := photo["media"].(string)
		fileID := media
		if name, ok := strings.CutPrefix(media, "attach://"); ok {
			if part, ok := files[name]; ok {
				fileID = "photo-" + part.SHA256[:16]
			}
		}
		blocks = append(blocks, map[string]any{"type": richPhoto, richPhoto: []map[string]any{
			{"file_id": fileID, "file_unique_id": "u" + fileID, "width": coverWidth, "height": coverHeight},
		}})
	}
	return map[string]any{"blocks": blocks}
}

// pin records chat's pinned message.
func (f *FakeBotAPI) pin(chat, message int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pinned == nil {
		f.pinned = map[int64]int64{}
	}
	f.pinned[chat] = message
}

// Unpin forgets chat's pinned message, as a reader unpinning it would.
func (f *FakeBotAPI) Unpin(chat int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pinned, chat)
}

// chatFor is what getChat answers.
func (f *FakeBotAPI) chatFor(chat int64) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{"id": chat, "type": "private"}
	if id, ok := f.pinned[chat]; ok {
		out["pinned_message"] = map[string]any{"message_id": id, "date": time.Now().Unix(), "chat": map[string]any{"id": chat, "type": "private"}}
	}
	return out
}

func parseCall(r *http.Request) (Call, error) {
	// Path: /bot{token}/{method}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if !strings.HasPrefix(path, "bot") {
		return Call{}, fmt.Errorf("unexpected path %q", r.URL.Path)
	}
	slash := strings.LastIndex(path, "/")
	if slash < 0 {
		return Call{}, fmt.Errorf("no method in %q", r.URL.Path)
	}
	c := Call{
		Token:  path[len("bot"):slash],
		Method: path[slash+1:],
		Params: map[string]string{},
		Files:  map[string]UploadedFile{},
		At:     time.Now(),
	}

	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mediaType {
	case "multipart/form-data":
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return c, err
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return c, err
			}
			if part.FileName() != "" {
				sum := sha256.Sum256(data)
				c.Files[part.FormName()] = UploadedFile{
					Filename: part.FileName(), Size: len(data), SHA256: hex.EncodeToString(sum[:]), Data: data,
				}
				continue
			}
			c.Params[part.FormName()] = string(data)
		}
	case "application/json":
		var fields map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			return c, err
		}
		for k, v := range fields {
			var s string
			if json.Unmarshal(v, &s) == nil {
				c.Params[k] = s
			} else {
				c.Params[k] = string(v)
			}
		}
	default:
		// No body: methods without fields (getMe, getWebhookInfo).
		_, _ = io.Copy(io.Discard, r.Body)
	}
	return c, nil
}

// apiAnswer is the Bot API's response envelope.
type apiAnswer struct {
	OK          bool           `json:"ok"`
	Result      any            `json:"result,omitempty"`
	ErrorCode   int            `json:"error_code,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

func writeAnswer(w http.ResponseWriter, status int, a apiAnswer) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(a)
}

func writeResult(w http.ResponseWriter, result any) {
	writeAnswer(w, http.StatusOK, apiAnswer{OK: true, Result: result})
}

func writeError(w http.ResponseWriter, code int, description string) {
	writeAnswer(w, code, apiAnswer{ErrorCode: code, Description: description})
}
