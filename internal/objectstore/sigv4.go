package objectstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	amzDateFormat   = "20060102T150405Z"
	amzShortDate    = "20060102"
	sigV4Algorithm  = "AWS4-HMAC-SHA256"
	sigV4Service    = "s3"
	sigV4Terminator = "aws4_request"
	// signedHeaders is what the signature covers, in the required order.
	signedHeaders = "host;x-amz-content-sha256;x-amz-date"
)

// sign adds a Signature Version 4 to a request without a body. The Range
// header stays outside the signature, as the protocol allows, so that a read
// ahead can be sized without re-deriving anything.
func (c *Client) sign(req *http.Request, now time.Time) {
	c.signPayload(req, now, emptyPayloadHash)
}

// signPayload signs a request whose body has this SHA-256.
func (c *Client) signPayload(req *http.Request, now time.Time, payloadHash string) {
	stamp := now.UTC().Format(amzDateFormat)
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Authorization",
		authorization(&c.cfg, req.Method, req.URL.Host, req.URL.EscapedPath(), req.URL.RawQuery, stamp, payloadHash))
}

// authorization is the header value for one signed request.
func authorization(cfg *Config, method, host, path, query, stamp, payloadHash string) string {
	day := stamp[:len(amzShortDate)]
	scope := strings.Join([]string{day, cfg.Region, sigV4Service, sigV4Terminator}, "/")
	canonical := strings.Join([]string{
		method,
		path,
		query,
		"host:" + host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + stamp + "\n",
		signedHeaders,
		payloadHash,
	}, "\n")
	digest := sha256.Sum256([]byte(canonical))
	toSign := strings.Join([]string{sigV4Algorithm, stamp, scope, hex.EncodeToString(digest[:])}, "\n")

	key := hmacSHA256([]byte("AWS4"+string(cfg.SecretAccessKey)), day)
	key = hmacSHA256(key, cfg.Region)
	key = hmacSHA256(key, sigV4Service)
	key = hmacSHA256(key, sigV4Terminator)
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	return sigV4Algorithm + " Credential=" + string(cfg.AccessKeyID) + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// uriEncode is the signature's own percent encoding: everything outside the
// unreserved set, as uppercase hex, with "/" kept in a key and encoded in a
// query value.
func uriEncode(value string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.', ch == '~':
			b.WriteByte(ch)
		case ch == '/' && !encodeSlash:
			b.WriteByte(ch)
		default:
			const hexDigits = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hexDigits[ch>>4])
			b.WriteByte(hexDigits[ch&0x0f])
		}
	}
	return b.String()
}

// canonicalQuery sorts and encodes a query the way the signature expects. The
// same string goes on the wire, so what is signed is what is sent.
func canonicalQuery(query url.Values) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range query[k] {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}
