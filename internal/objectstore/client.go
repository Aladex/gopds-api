// Package objectstore is a small client for the S3 API.
//
// It knows four calls: HEAD, ranged GET, ListObjectsV2 and PUT. That is all the
// application needs from object storage, and it is the reason there is no SDK
// here. The two usual choices bring their own retry loops, which have to be
// fought to get the bounded failure a request path needs, and a long tail of
// modules this project would then have to keep patched: minio-go alone pulls
// in 19. Signing one GET is about a hundred lines of the standard library,
// checked against the signature AWS publishes for it.
//
// Being this small is also a guarantee. There is no DELETE in this package, and
// PUT writes only under the one prefix a client is given, or nowhere without
// one. So nothing that goes through it can remove a remote object or change one
// outside that prefix, whatever a bug elsewhere asks for.
package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors a call reports. Every failure of the storage itself, from a refused
// connection to a bad key, is ErrUnavailable, so that callers can tell "the
// file does not exist" from "the store cannot say".
var (
	// ErrNotFound is an object the store does not have.
	ErrNotFound = errors.New("object not found")
	// ErrUnavailable is a store that did not answer usefully.
	ErrUnavailable = errors.New("object storage unavailable")
	// ErrChanged is an object that is no longer the version asked for.
	ErrChanged = errors.New("object changed")
	// ErrWriteRefused is a write this client will not make: no request left it.
	ErrWriteRefused = errors.New("write outside the allowed prefix")
)

const (
	defaultRequestTimeout = 5 * time.Second
	defaultRetries        = 1

	dialTimeout    = 5 * time.Second
	keepAlive      = 30 * time.Second
	idleConnection = 90 * time.Second
	maxIdleConns   = 32
	retryBackoff   = 200 * time.Millisecond

	// A store that failed this many calls in a row is left alone for
	// breakerCooldown. In an outage a page of covers would otherwise queue
	// one full timeout per picture.
	breakerThreshold = 5
	breakerCooldown  = 15 * time.Second

	// defaultMaxActive bounds the calls in progress. It is what keeps a stalled
	// store from taking a connection and a goroutine per visitor.
	defaultMaxActive = 16

	// An error body is read for its code only.
	maxErrorBody = 4 << 10
	// A page of ListObjectsV2 holds a thousand keys at most.
	maxListBody = 8 << 20

	// listTypeParam and listTypeV2 select ListObjectsV2.
	listTypeParam = "list-type"
	prefixParam   = "prefix"
	listTypeV2    = "2"

	// emptyPayloadHash is the SHA-256 of nothing: none of these calls has a body.
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

const redacted = "[redacted]"

// Secret is a credential. It prints as a mask however it is formatted, so a
// logged configuration cannot leak it.
type Secret string

// String masks the secret.
func (Secret) String() string { return redacted }

// GoString masks the secret under %#v.
func (Secret) GoString() string { return redacted }

// MarshalText masks the secret in JSON, YAML and the like.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Config is how to reach a bucket.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     Secret
	SecretAccessKey Secret
	// PathStyle addresses the bucket as endpoint/bucket rather than as
	// bucket.endpoint. B2 takes both; MinIO usually only the first.
	PathStyle bool
	// RequestTimeout bounds one attempt up to the response headers, and any
	// silence inside a response body. Zero means the default.
	RequestTimeout time.Duration
	// MaxActive bounds the calls in progress at once. Zero means the default.
	MaxActive int
	// WritePrefix is the only place Put may write. Empty means nowhere.
	WritePrefix string
	// Retries is how many times a failed call is tried again. Negative means
	// none, zero the default.
	Retries int
}

// Info describes one object.
type Info struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
}

// Client talks to one bucket. It is safe for concurrent use.
type Client struct {
	cfg      Config
	endpoint *url.URL
	http     *http.Client
	timeout  time.Duration
	retries  int
	breaker  breaker
	active   chan struct{}
	now      func() time.Time
}

// New builds a client. It does not touch the network.
func New(cfg *Config) (*Client, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("objectstore: bad endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, errors.New("objectstore: endpoint must be a full URL")
	}
	if cfg.Bucket == "" || cfg.Region == "" {
		return nil, errors.New("objectstore: bucket and region are required")
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	retries := cfg.Retries
	switch {
	case retries < 0:
		retries = 0
	case retries == 0:
		retries = defaultRetries
	}
	maxActive := cfg.MaxActive
	if maxActive <= 0 {
		maxActive = defaultMaxActive
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConnsPerHost:   maxIdleConns,
		IdleConnTimeout:       idleConnection,
		// A Range is a range of the stored bytes; nothing may re-encode it.
		DisableCompression: true,
		ForceAttemptHTTP2:  true,
	}
	return &Client{
		cfg:      *cfg,
		endpoint: endpoint,
		http:     &http.Client{Transport: transport},
		timeout:  timeout,
		retries:  retries,
		active:   make(chan struct{}, maxActive),
		now:      time.Now,
	}, nil
}

// Timeout is the bound of one attempt.
func (c *Client) Timeout() time.Duration { return c.timeout }

// Retries is how many times a failed call is tried again.
func (c *Client) Retries() int { return c.retries }

// Head describes one object.
func (c *Client) Head(ctx context.Context, key string) (Info, error) {
	resp, finish, err := c.do(ctx, http.MethodHead, key, nil, "")
	if err != nil {
		return Info{}, err
	}
	defer finish()
	defer func() { _ = resp.Body.Close() }()
	return infoFrom(key, resp.Header, resp.ContentLength), nil
}

// Get opens an object's bytes. A positive length asks for that many bytes from
// off and is refused unless the store answers with exactly that range, so a
// store that ignores Range cannot pour a whole archive into a call for one
// book. The Body then hands over no more than that range and fails if it ends
// early. A non-empty etag is the version the caller knows: any other version is
// ErrChanged, before a byte of it is read. A store that sends no ETag cannot be
// held to one.
//
// The Body must be closed. Silence longer than the request timeout fails its
// next Read.
func (c *Client) Get(ctx context.Context, key string, off, length int64, etag string) (*Body, error) {
	rangeHeader := ""
	if length > 0 {
		rangeHeader = "bytes=" + strconv.FormatInt(off, 10) + "-" + strconv.FormatInt(off+length-1, 10)
	}
	resp, finish, err := c.do(ctx, http.MethodGet, key, nil, rangeHeader)
	if err != nil {
		return nil, err
	}
	info := infoFrom(key, resp.Header, resp.ContentLength)
	expected := int64(unknownLength)
	if rangeHeader != "" {
		info.Size, expected, err = checkRange(resp, off, length)
		if err != nil {
			_ = resp.Body.Close()
			finish()
			c.breaker.failure(c.now())
			return nil, err
		}
	}
	if etag != "" && info.ETag != "" && info.ETag != etag {
		_ = resp.Body.Close()
		finish()
		return nil, fmt.Errorf("%w: %s", ErrChanged, key)
	}
	return c.newBody(ctx, resp.Body, finish, info, expected), nil
}

// checkRange takes a ranged answer apart: the object's full size, and how many
// bytes the body must hold. The store may clip the range at the object's end
// and nothing else.
func checkRange(resp *http.Response, off, length int64) (total, expected int64, err error) {
	if resp.StatusCode != http.StatusPartialContent {
		return 0, 0, fmt.Errorf("%w: range refused, status %d", ErrUnavailable, resp.StatusCode)
	}
	header := resp.Header.Get("Content-Range")
	start, end, total, ok := parseContentRange(header)
	last := off + length - 1
	if !ok || start != off || end < start || end >= total || (end != last && (end > last || end != total-1)) {
		return 0, 0, fmt.Errorf("%w: unexpected Content-Range %q", ErrUnavailable, header)
	}
	expected = end - start + 1
	if resp.ContentLength >= 0 && resp.ContentLength != expected {
		return 0, 0, fmt.Errorf("%w: Content-Length %d for Content-Range %q", ErrUnavailable, resp.ContentLength, header)
	}
	return total, expected, nil
}

// parseContentRange reads "bytes start-end/total".
func parseContentRange(value string) (start, end, total int64, ok bool) {
	rest, found := strings.CutPrefix(value, "bytes ")
	if !found {
		return 0, 0, 0, false
	}
	span, size, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, 0, false
	}
	first, last, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, 0, false
	}
	var err error
	if start, err = strconv.ParseInt(first, 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if end, err = strconv.ParseInt(last, 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if total, err = strconv.ParseInt(size, 10, 64); err != nil {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

// Put stores data under key, replacing what is there. It makes one attempt
// within the request timeout and stays out of the breaker's count: a write that
// fails, as it will for good on a read-only key, says nothing about whether
// books can be read. Nothing depends on it, so the caller only logs a failure.
func (c *Client) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if !c.writable(key) {
		return fmt.Errorf("%w: %s", ErrWriteRefused, key)
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.objectURL(key, nil), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("objectstore: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	sum := sha256.Sum256(data)
	c.signPayload(req, c.now(), hex.EncodeToString(sum[:]))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return statusError(key, resp)
}

// writable reports a key strictly under the write prefix. A dot segment is
// refused because a gateway may normalize it, and the key would then land
// somewhere the prefix check never saw.
func (c *Client) writable(key string) bool {
	if c.cfg.WritePrefix == "" {
		return false
	}
	rest, found := strings.CutPrefix(key, c.cfg.WritePrefix)
	if !found || rest == "" {
		return false
	}
	for segment := range strings.SplitSeq(rest, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// listResult is the part of a ListObjectsV2 page used here.
type listResult struct {
	Contents []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		ETag         string    `xml:"ETag"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

// List returns every object under prefix.
func (c *Client) List(ctx context.Context, prefix string) ([]Info, error) {
	var out []Info
	token := ""
	for {
		query := url.Values{listTypeParam: {listTypeV2}, prefixParam: {prefix}}
		if token != "" {
			query.Set("continuation-token", token)
		}
		page, err := c.listPage(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			out = append(out, Info{Key: o.Key, Size: o.Size, ETag: normalizeETag(o.ETag), LastModified: o.LastModified})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			return out, nil
		}
		token = page.NextContinuationToken
	}
}

func (c *Client) listPage(ctx context.Context, query url.Values) (*listResult, error) {
	resp, finish, err := c.do(ctx, http.MethodGet, "", query, "") //nolint:bodyclose // closed through Body below
	if err != nil {
		return nil, err
	}
	body := c.newBody(ctx, resp.Body, finish, Info{}, unknownLength)
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(body, maxListBody))
	if err != nil {
		return nil, err
	}
	page := &listResult{}
	if err := xml.Unmarshal(raw, page); err != nil {
		body.reject()
		return nil, fmt.Errorf("%w: bad listing: %v", ErrUnavailable, err)
	}
	return page, nil
}

// do runs one call with its retries. On success the caller owns the response
// and the finish function, which ends the request and gives back its slot.
//
// Only failures are counted here. Headers prove little: a gateway in trouble
// sends them and then stalls, and a HEAD or a 404 has no body to stall on. What
// clears the count is a read that completed, which only the Body can see.
func (c *Client) do(
	ctx context.Context, method, key string, query url.Values, rangeHeader string,
) (*http.Response, context.CancelFunc, error) {
	if !c.breaker.allow(c.now()) {
		return nil, nil, fmt.Errorf("%w: resting after repeated failures", ErrUnavailable)
	}
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, retryBackoff<<(attempt-1)); err != nil {
				return nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
		}
		release, err := c.acquire(ctx)
		if err != nil {
			return nil, nil, err
		}
		resp, cancel, err := c.attempt(ctx, method, key, query, rangeHeader)
		if err == nil {
			return resp, func() { cancel(); release() }, nil
		}
		release()
		if !retryable(err) {
			return nil, nil, err
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
		}
	}
	c.breaker.failure(c.now())
	return nil, nil, lastErr
}

// acquire takes a slot for one call. A store this busy is treated as an
// outage after one timeout, not as a queue: during a stall every waiter would
// otherwise pay for the calls in front of it. It says nothing of the store's
// health, so it does not count.
func (c *Client) acquire(ctx context.Context) (func(), error) {
	wait := time.NewTimer(c.timeout)
	defer wait.Stop()
	select {
	case c.active <- struct{}{}:
	case <-wait.C:
		return nil, fmt.Errorf("%w: %d calls in progress", ErrUnavailable, cap(c.active))
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
	var once sync.Once
	return func() { once.Do(func() { <-c.active }) }, nil
}

// attempt makes one request and judges its status.
func (c *Client) attempt(
	ctx context.Context, method, key string, query url.Values, rangeHeader string,
) (*http.Response, context.CancelFunc, error) {
	reqCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(reqCtx, method, c.objectURL(key, query), http.NoBody)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("objectstore: build request: %w", err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	c.sign(req, c.now())

	// The watchdog covers the wait for headers, including the connection
	// itself; after that a good body has its own. A refusal does not: limiting
	// its bytes says nothing of how long they take, so the same watchdog runs
	// on until it has been read.
	watchdog := time.AfterFunc(c.timeout, cancel)
	resp, err := c.http.Do(req)
	if err != nil {
		watchdog.Stop()
		timedOut := reqCtx.Err() != nil && ctx.Err() == nil
		cancel()
		if timedOut {
			return nil, nil, &transientError{fmt.Errorf("%w: no answer within %s", ErrUnavailable, c.timeout)}
		}
		return nil, nil, &transientError{fmt.Errorf("%w: %w", ErrUnavailable, err)}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		watchdog.Stop()
		return resp, cancel, nil
	}
	defer cancel()
	defer watchdog.Stop()
	defer func() { _ = resp.Body.Close() }()
	return nil, nil, statusError(key, resp)
}

// transientError marks a failure worth trying again.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

func retryable(err error) bool {
	var t *transientError
	return errors.As(err, &t)
}

// statusError turns a refusal into the error it stands for.
func statusError(key string, resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	case http.StatusPreconditionFailed:
		return fmt.Errorf("%w: %s", ErrChanged, key)
	}
	var detail struct {
		Code string `xml:"Code"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	_ = xml.Unmarshal(raw, &detail)
	err := fmt.Errorf("%w: status %d %s on %s", ErrUnavailable, resp.StatusCode, detail.Code, key)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
		return &transientError{err}
	}
	return err
}

// objectURL is the address of a key, or of the bucket for an empty key. It is
// built as text because the path has to go out exactly as it was signed.
func (c *Client) objectURL(key string, query url.Values) string {
	host := c.endpoint.Host
	path := strings.TrimRight(c.endpoint.EscapedPath(), "/")
	if c.cfg.PathStyle {
		path += "/" + uriEncode(c.cfg.Bucket, false)
	} else {
		host = c.cfg.Bucket + "." + host
	}
	path += "/" + uriEncode(key, false)
	target := c.endpoint.Scheme + "://" + host + path
	if len(query) > 0 {
		target += "?" + canonicalQuery(query)
	}
	return target
}

func infoFrom(key string, header http.Header, length int64) Info {
	info := Info{Key: key, Size: length, ETag: normalizeETag(header.Get("ETag"))}
	if modified, err := http.ParseTime(header.Get("Last-Modified")); err == nil {
		info.LastModified = modified
	}
	return info
}

// normalizeETag drops what a store may wrap a tag in: quotes, a weak marker.
func normalizeETag(tag string) string {
	return strings.Trim(strings.TrimPrefix(tag, "W/"), `"`)
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// breaker stops calling a store that keeps failing.
type breaker struct {
	mu       sync.Mutex
	failures int
	until    time.Time
}

func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !now.Before(b.until)
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
}

func (b *breaker) failure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= breakerThreshold {
		b.until = now.Add(breakerCooldown)
		// One failure after the rest closes it again; one completed read
		// resets it.
		b.failures = breakerThreshold - 1
	}
}

// Body is an open object: its bytes and what the store said about them. Its
// outcome is what the breaker learns: a read that failed counts against the
// store, one that delivered clears the count, and a caller that left says
// nothing either way.
type Body struct {
	Info
	client   *Client
	ctx      context.Context
	body     io.ReadCloser
	finish   context.CancelFunc
	watchdog *time.Timer
	expected int64 // the bytes this body must hold; unknownLength if not known
	read     int64
	settled  bool
}

// unknownLength is the expected size of a body without a range.
const unknownLength = -1

func (c *Client) newBody(ctx context.Context, body io.ReadCloser, finish context.CancelFunc, info Info, expected int64) *Body {
	b := &Body{Info: info, client: c, ctx: ctx, body: body, finish: finish, expected: expected}
	b.watchdog = time.AfterFunc(c.timeout, finish)
	b.watchdog.Stop()
	return b
}

// Read reads the body, failing if the store goes silent for the timeout, sends
// more than the range it promised, or ends before it. One byte past the range
// is all it takes to see an oversized answer, and all that is ever consumed.
func (b *Body) Read(p []byte) (int, error) {
	if b.expected != unknownLength {
		if room := b.expected - b.read + 1; int64(len(p)) > room {
			p = p[:room]
		}
	}
	b.watchdog.Reset(b.client.timeout)
	n, err := b.body.Read(p)
	b.watchdog.Stop()
	b.read += int64(n)
	if b.expected != unknownLength {
		if over := b.read - b.expected; over > 0 {
			b.read -= over
			return n - int(over), b.fail(fmt.Errorf("%w: more than the range of %s", ErrUnavailable, b.Key))
		}
		if errors.Is(err, io.EOF) && b.read < b.expected {
			return n, b.fail(fmt.Errorf("%w: short range from %s", ErrUnavailable, b.Key))
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, b.fail(fmt.Errorf("%w: reading %s: %w", ErrUnavailable, b.Key, err))
	}
	return n, err
}

// fail counts a broken read against the store, unless it broke because the
// caller left.
func (b *Body) fail(err error) error {
	if !b.settled && b.ctx.Err() == nil {
		b.settled = true
		b.client.breaker.failure(b.client.now())
	}
	return err
}

// reject is a caller saying that the bytes it got are no good.
func (b *Body) reject() {
	_ = b.fail(nil)
}

// Close ends the request. A body read without a fault proves the store works,
// even if the caller stopped short of its end: a read ahead usually does.
func (b *Body) Close() error {
	b.watchdog.Stop()
	err := b.body.Close()
	b.finish()
	if !b.settled && b.read > 0 && b.ctx.Err() == nil {
		b.settled = true
		b.client.breaker.success()
	}
	return err
}
