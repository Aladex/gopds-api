package tgapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-telegram/bot"
)

// maxGetMeBytes bounds getMe's answer.
const maxGetMeBytes = 64 << 10

// Me is what getMe says about a bot. It is read here rather than through the
// SDK, whose models.User names the inline flag "support_inline_queries":
// Telegram sends "supports_inline_queries", so the SDK's flag is always false.
type Me struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	FirstName      string `json:"first_name"`
	SupportsInline bool   `json:"supports_inline_queries"`
	// CanManageBots is Bot Management Mode, set in BotFather: the bot may
	// create and manage other bots (Managed Bots).
	CanManageBots bool `json:"can_manage_bots"`
}

// GetMe asks Telegram about the bot whose token this is. Errors name neither
// the token nor the URL; Telegram's refusals wrap the SDK's sentinel errors,
// so Classify reads them like any other call's.
func GetMe(ctx context.Context, token string, e Endpoint) (*Me, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.MethodURL(token, "getMe"), http.NoBody)
	if err != nil {
		return nil, RedactURL(err)
	}
	resp, err := e.Client().Do(req)
	if err != nil {
		return nil, RedactURL(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGetMeBytes))
	if err != nil {
		return nil, RedactURL(err)
	}
	var answer struct {
		OK          bool   `json:"ok"`
		Result      Me     `json:"result"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, fmt.Errorf("getMe: undecodable answer (HTTP %d)", resp.StatusCode)
	}
	if !answer.OK {
		return nil, fmt.Errorf("getMe: %w: %s", sentinel(answer.ErrorCode), answer.Description)
	}
	return &answer.Result, nil
}

// sentinel is the SDK's error for a Bot API error code.
func sentinel(code int) error {
	switch code {
	case http.StatusUnauthorized:
		return bot.ErrorUnauthorized
	case http.StatusForbidden:
		return bot.ErrorForbidden
	case http.StatusNotFound:
		return bot.ErrorNotFound
	}
	return bot.ErrorBadRequest
}
