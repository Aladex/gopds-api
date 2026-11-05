// Package s3test is a fake S3 endpoint for tests: path-style HEAD, ranged GET,
// PUT and ListObjectsV2 over httptest, with request and byte counters and
// switches for the ways a real store fails.
//
// It verifies every request's signature with a Signature V4 of its own, written
// from the protocol rather than shared with objectstore, so a signing mistake
// fails the test that made the request.
package s3test

import (
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- S3's ETag of a simple object is its MD5.
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopds-api/internal/objectstore"
)

// forbiddenBody is what a read-only key gets for a PUT.
const forbiddenBody = "<Error><Code>AccessDenied</Code></Error>"

// Credentials every Server expects.
const (
	Bucket    = "test-bucket"
	Region    = "test-region"
	AccessKey = "TESTACCESSKEY"
	SecretKey = "test/secret+key"
)

const listPageSize = 2

type object struct {
	data     []byte
	etag     string
	modified time.Time
	kind     string
}

// Read is one served request.
type Read struct {
	Method string
	Key    string
	Off    int64
	Bytes  int64
}

// Server is the fake store.
type Server struct {
	*httptest.Server
	t testing.TB

	mu          sync.Mutex
	objects     map[string]*object
	reads       []Read
	down        bool
	hang        bool
	ignoreRange bool
	dropAfter   int64
	failNext    int
	refuseWrite bool
	failWrites  bool
	hangWrites  bool
	silentError bool
	stallBodies bool
	drip        time.Duration
	active      int
	peak        int
}

// New starts a server, closed with the test.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{t: t, objects: map[string]*object{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// Client is a client of this server with a short timeout.
func (s *Server) Client(timeout time.Duration, retries int) *objectstore.Client {
	s.t.Helper()
	client, err := objectstore.New(&objectstore.Config{
		Endpoint:        s.URL,
		Region:          Region,
		Bucket:          Bucket,
		AccessKeyID:     AccessKey,
		SecretAccessKey: SecretKey,
		PathStyle:       true,
		RequestTimeout:  timeout,
		Retries:         retries,
	})
	if err != nil {
		s.t.Fatalf("client: %v", err)
	}
	return client
}

// WriterClient is a client of this server allowed to write under prefix.
func (s *Server) WriterClient(timeout time.Duration, prefix string) *objectstore.Client {
	s.t.Helper()
	client, err := objectstore.New(&objectstore.Config{
		Endpoint:        s.URL,
		Region:          Region,
		Bucket:          Bucket,
		AccessKeyID:     AccessKey,
		SecretAccessKey: SecretKey,
		PathStyle:       true,
		RequestTimeout:  timeout,
		Retries:         -1,
		WritePrefix:     prefix,
	})
	if err != nil {
		s.t.Fatalf("client: %v", err)
	}
	return client
}

// Put stores or replaces an object.
func (s *Server) Put(key string, data []byte) {
	s.PutAt(key, data, time.Now())
}

// PutAt is Put with the object dated modified.
func (s *Server) PutAt(key string, data []byte, modified time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// #nosec G401 -- S3's ETag of a simple object is its MD5.
	sum := md5.Sum(data)
	s.objects[key] = &object{data: data, etag: hex.EncodeToString(sum[:]), modified: modified.Truncate(time.Second)}
}

// Object returns what the server holds under key, and its Content-Type.
func (s *Server) Object(key string) (data []byte, kind string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[key]
	if !ok {
		return nil, "", false
	}
	return obj.data, obj.kind, true
}

// Writes names the keys written with PUT so far, in order.
func (s *Server) Writes() []string {
	var keys []string
	for _, r := range s.Reads() {
		if r.Method == http.MethodPut {
			keys = append(keys, r.Key)
		}
	}
	return keys
}

// Reads returns the requests served so far.
func (s *Server) Reads() []Read {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Read(nil), s.reads...)
}

// Requests counts the requests served so far.
func (s *Server) Requests() int { return len(s.Reads()) }

// Bytes counts the body bytes served so far.
func (s *Server) Bytes() int64 {
	var total int64
	for _, r := range s.Reads() {
		total += r.Bytes
	}
	return total
}

// Reset forgets the counters.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = nil
}

// SetDown makes every request a 503.
func (s *Server) SetDown(down bool) { s.set(func() { s.down = down }) }

// SetHang makes every request wait for the client to give up.
func (s *Server) SetHang(hang bool) { s.set(func() { s.hang = hang }) }

// SetIgnoreRange answers ranged requests with the whole object.
func (s *Server) SetIgnoreRange(ignore bool) { s.set(func() { s.ignoreRange = ignore }) }

// FailNext makes the next n requests a 503.
func (s *Server) FailNext(n int) { s.set(func() { s.failNext = n }) }

// DropAfter cuts every response body after n bytes; zero turns that off.
func (s *Server) DropAfter(n int64) { s.set(func() { s.dropAfter = n }) }

// SetRefuseWrites answers every PUT as a read-only key would be answered.
func (s *Server) SetRefuseWrites(refuse bool) { s.set(func() { s.refuseWrite = refuse }) }

// SetFailWrites answers every PUT with a 503, and leaves every other call alone.
func (s *Server) SetFailWrites(fail bool) { s.set(func() { s.failWrites = fail }) }

// SetHangWrites makes every PUT wait for the client to give up.
func (s *Server) SetHangWrites(hang bool) { s.set(func() { s.hangWrites = hang }) }

// SetSilentErrors answers every GET with flushed 503 headers that promise a
// body, and then says nothing until the client gives up.
func (s *Server) SetSilentErrors(silent bool) { s.set(func() { s.silentError = silent }) }

// SetStallBodies answers every GET with good headers and then says nothing
// until the client gives up.
func (s *Server) SetStallBodies(stall bool) { s.set(func() { s.stallBodies = stall }) }

// SetDrip sends every GET body a byte at a time at this interval, for as long
// as the client stays: each read succeeds and the whole never ends. Zero turns
// that off.
func (s *Server) SetDrip(interval time.Duration) { s.set(func() { s.drip = interval }) }

// Active counts the requests in progress right now.
func (s *Server) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Peak is the most requests that were ever in progress at once.
func (s *Server) Peak() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

func (s *Server) enter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active++
	s.peak = max(s.peak, s.active)
}

func (s *Server) leave() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
}

func (s *Server) set(change func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	if !verified(r, payload) {
		s.t.Errorf("bad signature on %s %s", r.Method, r.URL.String())
		http.Error(w, "<Error><Code>SignatureDoesNotMatch</Code></Error>", http.StatusForbidden)
		return
	}
	s.enter()
	defer s.leave()
	key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/"+Bucket), "/")
	if r.Method == http.MethodPut {
		s.write(w, r, key, payload)
		return
	}
	s.mu.Lock()
	down := s.down || s.failNext > 0
	if s.failNext > 0 {
		s.failNext--
	}
	hang, ignoreRange, dropAfter := s.hang, s.ignoreRange, s.dropAfter
	silentError, stallBodies, drip := s.silentError, s.stallBodies, s.drip
	obj := s.objects[key]
	s.mu.Unlock()

	isGet := r.Method == http.MethodGet
	switch {
	case hang:
		<-r.Context().Done()
	case silentError && isGet:
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	case down:
		s.record(Read{Method: r.Method, Key: key})
		w.WriteHeader(http.StatusServiceUnavailable)
	case key == "":
		s.list(w, r)
	case obj == nil:
		s.record(Read{Method: r.Method, Key: key})
		w.WriteHeader(http.StatusNotFound)
	case (stallBodies || drip > 0) && isGet:
		s.withhold(w, r, obj, drip)
	default:
		s.serve(w, r, key, obj, ignoreRange, dropAfter)
	}
}

// withhold sends good headers and then none of the body, or only a trickle.
func (s *Server) withhold(w http.ResponseWriter, r *http.Request, obj *object, drip time.Duration) {
	size := int64(len(obj.data))
	off, end, _ := parseRange(r.Header.Get("Range"), size)
	w.Header().Set("ETag", `"`+obj.etag+`"`)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, end, size))
	w.Header().Set("Content-Length", strconv.FormatInt(end-off+1, 10))
	w.WriteHeader(http.StatusPartialContent)
	w.(http.Flusher).Flush()
	if drip == 0 {
		<-r.Context().Done()
		return
	}
	ticker := time.NewTicker(drip)
	defer ticker.Stop()
	for i := off; i < end; i++ {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = w.Write(obj.data[i : i+1])
			w.(http.Flusher).Flush()
		}
	}
	<-r.Context().Done()
}

// write takes a PUT. A write is recorded whether or not it was accepted: the
// tests care that it was tried.
func (s *Server) write(w http.ResponseWriter, r *http.Request, key string, payload []byte) {
	s.record(Read{Method: r.Method, Key: key, Bytes: int64(len(payload))})
	s.mu.Lock()
	refuse, fail, hang := s.refuseWrite, s.failWrites, s.hangWrites
	s.mu.Unlock()
	switch {
	case hang:
		<-r.Context().Done()
	case refuse:
		http.Error(w, forbiddenBody, http.StatusForbidden)
	case fail:
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		s.Put(key, payload)
		s.set(func() { s.objects[key].kind = r.Header.Get("Content-Type") })
	}
}

func (s *Server) record(read Read) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, read)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request, key string, obj *object, ignoreRange bool, dropAfter int64) {
	header := w.Header()
	header.Set("ETag", `"`+obj.etag+`"`)
	header.Set("Last-Modified", obj.modified.UTC().Format(http.TimeFormat))
	size := int64(len(obj.data))
	if r.Method == http.MethodHead {
		header.Set("Content-Length", strconv.FormatInt(size, 10))
		s.record(Read{Method: r.Method, Key: key})
		return
	}
	off, end := int64(0), size-1
	status := http.StatusOK
	if spec := r.Header.Get("Range"); spec != "" && !ignoreRange {
		parsed := false
		off, end, parsed = parseRange(spec, size)
		if !parsed {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, end, size))
		status = http.StatusPartialContent
	}
	body := obj.data[off : end+1]
	header.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if dropAfter > 0 && int64(len(body)) > dropAfter {
		_, _ = w.Write(body[:dropAfter])
		// Flushed, or a small cut never leaves the buffer and reads as a refused
		// connection instead of a body that broke.
		w.(http.Flusher).Flush()
		s.record(Read{Method: r.Method, Key: key, Off: off, Bytes: dropAfter})
		panic(http.ErrAbortHandler)
	}
	n, _ := w.Write(body)
	s.record(Read{Method: r.Method, Key: key, Off: off, Bytes: int64(n)})
}

func parseRange(spec string, size int64) (off, end int64, ok bool) {
	rest, found := strings.CutPrefix(spec, "bytes=")
	if !found {
		return 0, 0, false
	}
	first, last, found := strings.Cut(rest, "-")
	if !found {
		return 0, 0, false
	}
	off, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end, err = strconv.ParseInt(last, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return off, min(end, size-1), off < size
}

type listContents struct {
	Key          string
	Size         int64
	ETag         string
	LastModified string
}

type listBucketResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Contents              []listContents
	IsTruncated           bool
	NextContinuationToken string `xml:",omitempty"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	s.mu.Lock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		if strings.HasPrefix(key, query.Get("prefix")) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	start, _ := strconv.Atoi(query.Get("continuation-token"))
	result := listBucketResult{}
	for i := start; i < len(keys) && i < start+listPageSize; i++ {
		obj := s.objects[keys[i]]
		result.Contents = append(result.Contents, listContents{
			Key: keys[i], Size: int64(len(obj.data)), ETag: `"` + obj.etag + `"`,
			LastModified: obj.modified.UTC().Format(time.RFC3339),
		})
	}
	if start+listPageSize < len(keys) {
		result.IsTruncated = true
		result.NextContinuationToken = strconv.Itoa(start + listPageSize)
	}
	s.mu.Unlock()
	s.record(Read{Method: r.Method})
	_ = xml.NewEncoder(w).Encode(result)
}

// verified recomputes a request's signature. Written apart from objectstore on
// purpose: see the package comment.
func verified(r *http.Request, body []byte) bool {
	stamp := r.Header.Get("X-Amz-Date")
	if len(stamp) < 8 || r.Header.Get("X-Amz-Content-Sha256") == "" {
		return false
	}
	day := stamp[:8]
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	if r.Header.Get("X-Amz-Content-Sha256") != payload {
		return false
	}
	canonicalRequest := r.Method + "\n" + r.URL.EscapedPath() + "\n" + sortedQuery(r.URL.Query()) + "\n" +
		"host:" + r.Host + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + stamp + "\n\n" +
		"host;x-amz-content-sha256;x-amz-date\n" + payload
	hashed := sha256.Sum256([]byte(canonicalRequest))
	scope := day + "/" + Region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(hashed[:])
	key := []byte("AWS4" + SecretKey)
	for _, part := range []string{day, Region, "s3", "aws4_request", stringToSign} {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	want := "AWS4-HMAC-SHA256 Credential=" + AccessKey + "/" + scope +
		", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + hex.EncodeToString(key)
	return hmac.Equal([]byte(r.Header.Get("Authorization")), []byte(want))
}

func sortedQuery(query url.Values) string {
	var pairs []string
	for key, values := range query {
		for _, value := range values {
			pairs = append(pairs, escape(key)+"="+escape(value))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// escape is RFC 3986 encoding with uppercase hex, as the signature wants it.
func escape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
