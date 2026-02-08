package api

import (
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

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

	conn, err := websocket.Accept(c.Writer, c.Request, opts)
	if err != nil {
		return nil, err
	}

	return conn, nil
}
