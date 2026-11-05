// Package objectstore is a small read-only client for the S3 API.
//
// It knows three calls: HEAD, ranged GET and ListObjectsV2. That is all the
// application needs from object storage, and it is the reason there is no SDK
// here. The two usual choices bring their own retry loops, which have to be
// fought to get the bounded failure a request path needs, and a long tail of
// modules this project would then have to keep patched: minio-go alone pulls
// in 19. Signing one GET is about a hundred lines of the standard library,
// checked against the signature AWS publishes for it.
//
// Being read-only is also a guarantee. There is no PUT or DELETE in this
// package, so nothing that goes through it can change or remove a remote
// object, whatever a bug elsewhere asks for.
package objectstore

import (
	"context"
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
		now:      time.Now,
	}, nil
}

// Timeout is the bound of one attempt.
func (c *Client) Timeout() time.Duration { return c.timeout }

// Retries is how many times a failed call is tried again.
func (c *Client) Retries() int { return c.retries }

// Head describes one object.
func (c *Client) Head(ctx context.Context, key string) (Info, error) {
	resp, cancel, err := c.do(ctx, http.MethodHead, key, nil, "")
	if err != nil {
		return Info{}, err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	return infoFrom(key, resp.Header, resp.ContentLength), nil
}

// Get opens an object's bytes. A positive length asks for that many bytes from
// off and is refused unless the store answers with exactly that range, so a
// store that ignores Range cannot pour a whole archive into a call for one
// book. A non-empty etag is the version the caller knows: any other version is
// ErrChanged, before a byte of it is read. A store that sends no ETag cannot
// be held to one.
//
// The Body must be closed. Silence longer than the request timeout fails its
// next Read.
func (c *Client) Get(ctx context.Context, key string, off, length int64, etag string) (*Body, error) {
	rangeHeader := ""
	if length > 0 {
		rangeHeader = "bytes=" + strconv.FormatInt(off, 10) + "-" + strconv.FormatInt(off+length-1, 10)
	}
	resp, cancel, err := c.do(ctx, http.MethodGet, key, nil, rangeHeader)
	if err != nil {
		return nil, err
	}
	info := infoFrom(key, resp.Header, resp.ContentLength)
	if rangeHeader != "" {
		info.Size, err = checkRange(resp, off)
		if err != nil {
			_ = resp.Body.Close()
			cancel()
			return nil, err
		}
	}
	if etag != "" && info.ETag != "" && info.ETag != etag {
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("%w: %s", ErrChanged, key)
	}
	return newBody(resp.Body, cancel, c.timeout, info), nil
}

// checkRange returns the object's full size from a ranged answer.
func checkRange(resp *http.Response, off int64) (int64, error) {
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("%w: range refused, status %d", ErrUnavailable, resp.StatusCode)
	}
	start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok || start != off {
		return 0, fmt.Errorf("%w: unexpected Content-Range %q", ErrUnavailable, resp.Header.Get("Content-Range"))
	}
	return total, nil
}

// parseContentRange reads "bytes start-end/total".
func parseContentRange(value string) (start, total int64, ok bool) {
	rest, found := strings.CutPrefix(value, "bytes ")
	if !found {
		return 0, 0, false
	}
	span, size, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	first, _, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	total, err = strconv.ParseInt(size, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return start, total, true
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
	resp, cancel, err := c.do(ctx, http.MethodGet, "", query, "") //nolint:bodyclose // closed through Body below
	if err != nil {
		return nil, err
	}
	body := newBody(resp.Body, cancel, c.timeout, Info{})
	defer func() { _ = body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(body, maxListBody))
	if err != nil {
		return nil, err
	}
	page := &listResult{}
	if err := xml.Unmarshal(raw, page); err != nil {
		return nil, fmt.Errorf("%w: bad listing: %v", ErrUnavailable, err)
	}
	return page, nil
}

// do runs one call with its retries. On success the caller owns the response
// and the cancel function, which ends the request.
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
		resp, cancel, err := c.attempt(ctx, method, key, query, rangeHeader)
		if err == nil {
			c.breaker.success()
			return resp, cancel, nil
		}
		if !retryable(err) {
			// An answer, however unwelcome, is the store working.
			if !errors.Is(err, ErrUnavailable) {
				c.breaker.success()
			}
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
	// itself; after that the body has its own.
	watchdog := time.AfterFunc(c.timeout, cancel)
	resp, err := c.http.Do(req)
	watchdog.Stop()
	if err != nil {
		timedOut := reqCtx.Err() != nil && ctx.Err() == nil
		cancel()
		if timedOut {
			return nil, nil, &transientError{fmt.Errorf("%w: no answer within %s", ErrUnavailable, c.timeout)}
		}
		return nil, nil, &transientError{fmt.Errorf("%w: %w", ErrUnavailable, err)}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, cancel, nil
	}
	defer cancel()
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
		// One failure after the rest closes it again; one success resets it.
		b.failures = breakerThreshold - 1
	}
}

// Body is an open object: its bytes and what the store said about them.
type Body struct {
	Info
	body     io.ReadCloser
	cancel   context.CancelFunc
	timeout  time.Duration
	watchdog *time.Timer
}

func newBody(body io.ReadCloser, cancel context.CancelFunc, timeout time.Duration, info Info) *Body {
	b := &Body{Info: info, body: body, cancel: cancel, timeout: timeout}
	b.watchdog = time.AfterFunc(timeout, cancel)
	b.watchdog.Stop()
	return b
}

// Read reads the body, failing if the store goes silent for the timeout.
func (b *Body) Read(p []byte) (int, error) {
	b.watchdog.Reset(b.timeout)
	n, err := b.body.Read(p)
	b.watchdog.Stop()
	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("%w: reading %s: %w", ErrUnavailable, b.Key, err)
	}
	return n, err
}

// Close ends the request.
func (b *Body) Close() error {
	b.watchdog.Stop()
	err := b.body.Close()
	b.cancel()
	return err
}
