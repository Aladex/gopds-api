package telegram

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gopds-api/telegram/tgapi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// net/http names the request URL in a transport error, and a Bot API URL
// carries the token. The health check logs these errors.
func TestHealthCheckErrorsKeepTheTokenOut(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close() // every call now fails at dial, naming the URL

	bm := &BotManager{config: &Config{API: tgapi.Endpoint{ServerURL: closed.URL}}}
	const token = "123456789:AAH-secret-part-of-the-token-0123456789"

	_, err := bm.validateBotToken(token)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-part")

	_, err = bm.checkWebhookStatus(&Bot{token: token})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-part")
}
