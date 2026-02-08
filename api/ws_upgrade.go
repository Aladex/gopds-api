package api

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// ginHijackWriter wraps gin's ResponseWriter to fix WebSocket upgrade
// compatibility with coder/websocket.
//
// Two issues exist between gin v1.9+ and coder/websocket:
//
//  1. gin rejects Hijack() after WriteHeader() sets the "written" flag.
//     coder/websocket calls WriteHeader(101) before Hijack(), which is
//     standard WebSocket protocol. Our Hijack() bypasses gin by unwrapping
//     to the raw net/http ResponseWriter.
//
//  2. coder/websocket has a gin-specific workaround: it checks if the writer
//     implements WriteHeaderNow() and calls it to flush the 101 response to
//     the wire before hijacking. We must expose this method so the workaround
//     fires; otherwise the 101 is never sent and the handshake fails silently.
type ginHijackWriter struct {
	http.ResponseWriter
}

// WriteHeaderNow exposes gin's WriteHeaderNow so that coder/websocket's
// gin workaround (accept.go lines 153-157) can flush the 101 status and
// headers to the wire before Hijack(). This also marks gin's writer as
// "written", preventing a duplicate WriteHeader after the handler returns.
func (w *ginHijackWriter) WriteHeaderNow() {
	type writeHeaderNower interface {
		WriteHeaderNow()
	}
	if g, ok := w.ResponseWriter.(writeHeaderNower); ok {
		g.WriteHeaderNow()
	}
}

// Hijack bypasses gin's "response already written" guard by unwrapping
// to the raw net/http ResponseWriter whose Hijack() has no such check.
func (w *ginHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	type unwrapper interface {
		Unwrap() http.ResponseWriter
	}
	rw := w.ResponseWriter
	for {
		if u, ok := rw.(unwrapper); ok {
			rw = u.Unwrap()
		} else {
			break
		}
	}
	if hj, ok := rw.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying writer %T doesn't support hijacking", rw)
}

// Flush delegates to the underlying writer's Flush.
func (w *ginHijackWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// allowedOriginPatterns returns the list of origin patterns from config,
// with localhost entries added in devel mode.
func allowedOriginPatterns() []string {
	patterns := viper.GetStringSlice("app.allowed_origins")
	if viper.GetBool("app.devel_mode") {
		patterns = append(patterns, "127.0.0.1:3000", "localhost:3000")
	}
	return patterns
}

// OriginCheckMiddleware validates the Origin header BEFORE auth, so that
// cross-origin requests get 403 Forbidden regardless of authentication state.
// Same-origin requests (no Origin header, or Origin matching the Host) pass through.
func OriginCheckMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			// No Origin header = same-origin or non-browser client. Allow.
			c.Next()
			return
		}

		u, err := url.Parse(origin)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid origin"})
			return
		}

		// Same-origin: Origin host matches request Host.
		if strings.EqualFold(c.Request.Host, u.Host) {
			c.Next()
			return
		}

		patterns := allowedOriginPatterns()

		// No configured patterns and not devel mode → reject cross-origin.
		if len(patterns) == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
			return
		}

		for _, pattern := range patterns {
			target := u.Host
			if strings.Contains(pattern, "://") {
				target = u.Scheme + "://" + u.Host
			}
			if strings.EqualFold(pattern, target) {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
	}
}

// upgradeWebSocket upgrades an HTTP connection to WebSocket.
// Origin is already validated by OriginCheckMiddleware, so we skip
// origin verification in websocket.Accept.
func upgradeWebSocket(c *gin.Context) (*websocket.Conn, error) {
	opts := &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Origin already checked by middleware.
	}

	w := &ginHijackWriter{ResponseWriter: c.Writer}

	conn, err := websocket.Accept(w, c.Request, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to accept WebSocket connection: %w", err)
	}

	return conn, nil
}
