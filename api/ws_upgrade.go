package api

import (
	"bufio"
	"fmt"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// ginHijackWriter wraps gin's ResponseWriter to fix WebSocket upgrade
// compatibility. coder/websocket calls WriteHeader(101) before Hijack(),
// but gin v1.9+ rejects Hijack() after WriteHeader() marks the response
// as "written". This wrapper overrides Hijack() to bypass gin's check
// by unwrapping to the underlying http.ResponseWriter.
type ginHijackWriter struct {
	http.ResponseWriter
}

func (w *ginHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// Unwrap through any middleware wrappers to reach the raw
	// net/http ResponseWriter whose Hijack() has no Written() guard.
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

func (w *ginHijackWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// upgradeWithOriginCheck upgrades an HTTP connection to WebSocket with Origin
// validation using coder/websocket's built-in OriginPatterns.
func upgradeWithOriginCheck(c *gin.Context) (*websocket.Conn, error) {
	patterns := viper.GetStringSlice("app.allowed_origins")

	if viper.GetBool("app.devel_mode") {
		patterns = append(patterns, "127.0.0.1:3000", "localhost:3000")
	}

	opts := &websocket.AcceptOptions{}
	if len(patterns) > 0 {
		opts.OriginPatterns = patterns
	} else {
		// No configured origins — accept all (InsecureSkipVerify equivalent)
		opts.InsecureSkipVerify = true
	}

	// Wrap gin's writer to fix Hijack() after WriteHeader(101).
	w := &ginHijackWriter{ResponseWriter: c.Writer}

	conn, err := websocket.Accept(w, c.Request, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to accept WebSocket connection: %w", err)
	}

	return conn, nil
}
