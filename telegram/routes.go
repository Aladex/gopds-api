package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/secrets"
	"gopds-api/telegram/tgapi"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Routes contains all routes for working with Telegram bots
type Routes struct {
	botManager *BotManager
}

// NewRoutes creates a new instance of routes
func NewRoutes(botManager *BotManager) *Routes {
	return &Routes{
		botManager: botManager,
	}
}

// SetBotTokenRequest structure of the request for setting the bot token
type SetBotTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

// ErrorResponse error structure. Error is a stable code the site translates:
// invalid_token, bot_taken, telegram_unavailable, webhook_failed,
// no_pending_bot, internal.
type ErrorResponse struct {
	Error string `json:"error"`
}

// BotStatusResponse is the account's Telegram connection as the site shows it.
type BotStatusResponse struct {
	// State is none, pending_link, connected, token_rejected or blocked.
	State string `json:"state"`
	// BotUsername and TelegramName describe the connected bot.
	BotUsername  string `json:"bot_username,omitempty"`
	TelegramName string `json:"telegram_name,omitempty"`
	// DefaultFormat is the format books are sent in from the site.
	DefaultFormat string `json:"default_format,omitempty"`
	// PendingBotUsername is a bot entered but not linked yet; while a
	// connection exists, this is a change of connection in progress.
	PendingBotUsername string `json:"pending_bot_username,omitempty"`
	// LinkURL is the one-time link, given only by the call that made it.
	LinkURL string `json:"link_url,omitempty"`
	// LinkExpiresAt is when the newest link of the pending bot stops working.
	LinkExpiresAt *time.Time `json:"link_expires_at,omitempty"`
}

// Error codes of ErrorResponse.
const (
	errInvalidToken        = "invalid_token"
	errBotTaken            = "bot_taken"
	errTelegramUnavailable = "telegram_unavailable"
	errWebhookFailed       = "webhook_failed"
	errNoPendingBot        = "no_pending_bot"
	errInternal            = "internal"
)

// Connection states of BotStatusResponse.
const (
	stateNone          = "none"
	statePendingLink   = "pending_link"
	stateConnected     = "connected"
	stateTokenRejected = "token_rejected"
	stateBlocked       = "blocked"
)

// userIDOf reads the authenticated user, answering 401 when there is none.
func userIDOf(c *gin.Context) (int64, bool) {
	value, exists := c.Get("user_id")
	userID, ok := value.(int64)
	if !exists || !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "User not authenticated"})
		return 0, false
	}
	return userID, true
}

// SetBotToken connects an existing bot by its token and returns a one-time
// link: the connection is made when the owner opens it in Telegram.
// @Summary Connect an existing Telegram bot
// @Description Checks the token with Telegram, registers the bot for the current user
// @Description and returns a one-time link to confirm the connection from Telegram
// @Tags telegram
// @Accept json
// @Produce json
// @Param request body SetBotTokenRequest true "Bot token"
// @Success 200 {object} BotStatusResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/telegram/bot [post]
func (r *Routes) SetBotToken(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	var req SetBotTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil || !isValidToken(strings.TrimSpace(req.Token)) {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errInvalidToken})
		return
	}
	token := strings.TrimSpace(req.Token)
	ctx := c.Request.Context()
	bm := r.botManager

	client, err := tgapi.New(token, bm.config.API)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errInvalidToken})
		return
	}
	me, err := client.GetMe(ctx)
	if err != nil {
		class, _ := tgapi.Classify(err)
		if class == tgapi.ClassUnauthorized || class == tgapi.ClassBadRequest {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: errInvalidToken})
			return
		}
		logging.Warnf("User %d: checking a bot token with Telegram: %v", userID, tgapi.RedactURL(err))
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: errTelegramUnavailable})
		return
	}

	model := bm.newBotModel(me.ID, token, uuid.New())
	model.Username = me.Username
	model.DisplayName = me.FirstName
	registered, err := database.RegisterManualTelegramBot(ctx, userID, model)
	if errors.Is(err, database.ErrTelegramBotTaken) {
		c.JSON(http.StatusConflict, ErrorResponse{Error: errBotTaken})
		return
	}
	if err != nil {
		logging.Errorf("User %d: registering telegram bot %d: %v", userID, me.ID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	b, err := bm.serve(registered)
	if err != nil {
		logging.Errorf("User %d: serving telegram bot %d: %v", userID, registered.ID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	if err := bm.setWebhook(b); err != nil {
		logging.Warnf("User %d: %v", userID, err)
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: errWebhookFailed})
		return
	}
	logging.Infof("User %d registered telegram bot %d", userID, registered.ID)
	r.respondWithLink(c, userID, registered.ID, registered.Username)
}

// NewLink gives a fresh one-time link for the bot the user entered last.
// @Summary Get a new Telegram link
// @Description Returns a new one-time link that confirms the pending bot of the current user
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Router /api/telegram/link [post]
func (r *Routes) NewLink(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	state, err := database.GetTelegramUserState(c.Request.Context(), userID)
	if err != nil {
		logging.Errorf("User %d: reading the telegram status: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	if state.Pending == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: errNoPendingBot})
		return
	}
	r.respondWithLink(c, userID, state.Pending.ID, state.Pending.Username)
}

// respondWithLink stores a new link for botRef and answers with the status
// and the link itself — the only time the secret leaves the server.
func (r *Routes) respondWithLink(c *gin.Context, userID, botRef int64, username string) {
	ctx := c.Request.Context()
	secret, hash := NewLinkSecret()
	if err := database.CreateTelegramLinkToken(ctx, userID, botRef, hash, time.Now().Add(LinkTTL)); err != nil {
		logging.Errorf("User %d: storing a telegram link: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	status, err := statusOf(ctx, userID)
	if err != nil {
		logging.Errorf("User %d: reading the telegram status: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	status.LinkURL = LinkURL(username, secret)
	c.JSON(http.StatusOK, status)
}

// RemoveBotToken disconnects the account's bot: the binding ends, the token
// is forgotten and the webhook removed. Favorites and files in the chat stay.
// @Summary Disconnect the Telegram bot
// @Description Ends the current user's Telegram connection and forgets the bot token
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/telegram/bot [delete]
func (r *Routes) RemoveBotToken(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	bm := r.botManager
	bots, err := database.DisconnectTelegram(c.Request.Context(), userID)
	if err != nil {
		logging.Errorf("User %d: disconnecting telegram: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	for i := range bots {
		bm.forget(bots[i].ID)
		token, err := bm.config.Secrets.Open(bots[i].KeyID, secrets.PurposeToken, bots[i].TokenEnc)
		if err == nil {
			bm.dropWebhook(string(token))
		}
	}
	logging.Infof("User %d disconnected telegram (%d bots)", userID, len(bots))
	c.JSON(http.StatusOK, BotStatusResponse{State: stateNone})
}

// GetBotStatus gets the status of the bot for the current user
// @Summary Get Telegram bot status
// @Description The current user's Telegram connection
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/telegram/bot/status [get]
func (r *Routes) GetBotStatus(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	status, err := statusOf(c.Request.Context(), userID)
	if err != nil {
		logging.Errorf("User %d: reading the telegram status: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	c.JSON(http.StatusOK, status)
}

// statusOf reads the account's connection.
func statusOf(ctx context.Context, userID int64) (BotStatusResponse, error) {
	state, err := database.GetTelegramUserState(ctx, userID)
	if err != nil {
		return BotStatusResponse{}, err
	}
	status := BotStatusResponse{State: stateNone}
	if state.Binding != nil {
		status.BotUsername = state.Bot.Username
		status.TelegramName = state.Binding.TGDisplay
		if status.DefaultFormat, err = database.GetTelegramDefaultFormat(ctx, userID); err != nil {
			return BotStatusResponse{}, err
		}
		switch {
		case state.Bot.Status == models.TelegramBotTokenRejected:
			status.State = stateTokenRejected
		case state.Binding.BlockedAt != nil:
			status.State = stateBlocked
		default:
			status.State = stateConnected
		}
	}
	if state.Pending != nil {
		status.PendingBotUsername = state.Pending.Username
		if state.Binding == nil {
			status.State = statePendingLink
		}
		if status.LinkExpiresAt, err = database.LatestTelegramLinkExpiry(ctx, userID, state.Pending.ID); err != nil {
			return BotStatusResponse{}, err
		}
	}
	return status, nil
}

// HandleWebhook handles webhook from Telegram
func (r *Routes) HandleWebhook(c *gin.Context) {
	r.botManager.HandleWebhook(c)
}

// isValidToken checks the format of the Telegram bot token
// minAuthTokenLength is the shortest second half a Telegram bot token has:
// the part after the colon, which BotFather issues as a fixed-width secret.
const minAuthTokenLength = 20

func isValidToken(token string) bool {
	// Telegram bot token format: number:string_of_letters_digits_dashes_underscores
	// For example: 5106077210:AAEtczjlz4LAnpb5ANSvFe26lm-bxmdQeeo
	parts := strings.Split(token, ":")
	if len(parts) != 2 {
		return false
	}

	// First part must be a number
	botID := parts[0]
	if len(botID) < 1 {
		return false
	}
	for _, char := range botID {
		if char < '0' || char > '9' {
			return false
		}
	}

	authToken := parts[1]
	return len(authToken) >= minAuthTokenLength
}
