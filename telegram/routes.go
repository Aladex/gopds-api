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
	"gopds-api/telegram/profile"
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
// no_pending_bot, managed_unavailable, managed_not_found, internal.
type ErrorResponse struct {
	Error string `json:"error"`
}

// BotStatusResponse is the account's Telegram connection as the site shows it.
type BotStatusResponse struct {
	// State is none, pending_link, connected, token_rejected, blocked or
	// owner_changed.
	State string `json:"state"`
	// BotKind is how the connected bot came: manual (its token entered) or
	// managed (created through the manager).
	BotKind string `json:"bot_kind,omitempty"`
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
	// ProfileApplied, while there is a binding, is the library's avatar and
	// descriptions given to the bound bot (plan.md P1-2).
	ProfileApplied *bool `json:"profile_applied,omitempty"`
	// ManagedAvailable is the Managed path on: a bot can be created here.
	ManagedAvailable bool `json:"managed_available"`
	// Managed is a Managed connection in progress.
	Managed *ManagedStatus `json:"managed,omitempty"`
}

// ManagedStatus is a Managed connection in progress (concept §C.3).
type ManagedStatus struct {
	// State is waiting (no bot from Telegram yet), not_ready (a bot came, but
	// does not answer yet) or ready (the bot is the pending one, to link).
	State string `json:"state"`
	// CreateURL is Telegram's creation window, while waiting.
	CreateURL string `json:"create_url,omitempty"`
	// BotUsername is the bot that came, once one did.
	BotUsername string `json:"bot_username,omitempty"`
	// ExpiresAt is when a waiting connection stops waiting.
	ExpiresAt time.Time `json:"expires_at"`
}

// States of ManagedStatus.
const (
	managedWaiting  = "waiting"
	managedNotReady = "not_ready"
	managedReady    = "ready"
)

// Error codes of ErrorResponse.
const (
	errInvalidToken        = "invalid_token"
	errBotTaken            = "bot_taken"
	errTelegramUnavailable = "telegram_unavailable"
	errWebhookFailed       = "webhook_failed"
	errNoPendingBot        = "no_pending_bot"
	errManagedUnavailable  = "managed_unavailable"
	errManagedNotFound     = "managed_not_found"
	errInternal            = "internal"
)

// Answers to a webhook call that is not Telegram's: an unknown path, or a
// wrong secret token.
const (
	errBotNotFound   = "Bot not found"
	errInvalidSecret = "Invalid secret token"
)

// Connection states of BotStatusResponse.
const (
	stateNone          = "none"
	statePendingLink   = "pending_link"
	stateConnected     = "connected"
	stateTokenRejected = "token_rejected"
	stateBlocked       = "blocked"
	stateOwnerChanged  = "owner_changed"
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
	switch {
	case state.Pending != nil:
		r.respondWithLink(c, userID, state.Pending.ID, state.Pending.Username)
	case state.Binding != nil && state.Bot.Status == models.TelegramBotOwnerChanged:
		// The bot's new owner links it again to take it over.
		r.respondWithLink(c, userID, state.Bot.ID, state.Bot.Username)
	default:
		c.JSON(http.StatusConflict, ErrorResponse{Error: errNoPendingBot})
	}
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
	status, err := r.statusOf(ctx, userID)
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
	if err := database.CancelTelegramPendingConnection(c.Request.Context(), userID); err != nil {
		logging.Errorf("User %d: canceling a managed connection: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
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
	c.JSON(http.StatusOK, BotStatusResponse{State: stateNone, ManagedAvailable: bm.managerOf() != nil})
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
	r.respondWithStatus(c, userID)
}

// respondWithStatus answers with the account's connection.
func (r *Routes) respondWithStatus(c *gin.Context, userID int64) {
	status, err := r.statusOf(c.Request.Context(), userID)
	if err != nil {
		logging.Errorf("User %d: reading the telegram status: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	c.JSON(http.StatusOK, status)
}

// statusOf reads the account's connection.
func (r *Routes) statusOf(ctx context.Context, userID int64) (BotStatusResponse, error) {
	state, err := database.GetTelegramUserState(ctx, userID)
	if err != nil {
		return BotStatusResponse{}, err
	}
	m := r.botManager.managerOf()
	status := BotStatusResponse{State: stateNone, ManagedAvailable: m != nil}
	if state.Binding != nil {
		status.BotUsername = state.Bot.Username
		status.BotKind = state.Bot.Kind
		var applied bool
		if applied, err = database.TelegramBotProfiled(ctx, state.Bot.ID); err != nil {
			return BotStatusResponse{}, err
		}
		status.ProfileApplied = &applied
		status.TelegramName = state.Binding.TGDisplay
		if status.DefaultFormat, err = database.GetTelegramDefaultFormat(ctx, userID); err != nil {
			return BotStatusResponse{}, err
		}
		switch {
		case state.Bot.Status == models.TelegramBotTokenRejected:
			status.State = stateTokenRejected
		case state.Bot.Status == models.TelegramBotOwnerChanged:
			status.State = stateOwnerChanged
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
	if status.Managed, err = managedStatusOf(ctx, m, userID); err != nil {
		return BotStatusResponse{}, err
	}
	return status, nil
}

// managedStatusOf is the account's Managed connection in progress, if any.
func managedStatusOf(ctx context.Context, m *Manager, userID int64) (*ManagedStatus, error) {
	pc, err := database.OpenTelegramPendingConnection(ctx, userID)
	if err != nil || pc == nil {
		return nil, err
	}
	if pc.BotRef == nil {
		if m == nil {
			return nil, nil
		}
		return &ManagedStatus{State: managedWaiting, CreateURL: m.CreateURL(pc.DiscoveryLabel), ExpiresAt: pc.ExpiresAt}, nil
	}
	bot, err := database.GetTelegramBot(ctx, *pc.BotRef)
	if err != nil {
		return nil, err
	}
	managed, err := database.GetTelegramManagedBot(ctx, bot.ID)
	if err != nil {
		return nil, err
	}
	status := &ManagedStatus{State: managedNotReady, BotUsername: bot.Username, ExpiresAt: pc.ExpiresAt}
	if managed != nil && managed.ReadyAt != nil {
		status.State = managedReady
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

// ClaimManagedRequest names a bot the reader already created.
type ClaimManagedRequest struct {
	Username string `json:"username" binding:"required"`
}

// StartManaged is «Создать личного бота»: a connection waits for the bot
// Telegram's creation window makes. Any earlier waiting one is canceled.
// @Summary Start creating a personal bot
// @Description Starts a Managed connection and returns Telegram's creation link
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Router /api/telegram/managed [post]
func (r *Routes) StartManaged(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	if r.botManager.managerOf() == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: errManagedUnavailable})
		return
	}
	label := NewDiscoveryLabel()
	if err := database.StartTelegramPendingConnection(c.Request.Context(), userID, label, time.Now().Add(managedLinkTTL)); err != nil {
		logging.Errorf("User %d: starting a managed connection: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	r.respondWithStatus(c, userID)
}

// ClaimManaged is «Я уже создал бота»: a bot of this manager, named by its
// username, becomes the one the connection waits on — for when the reader
// changed the suggested username. It proves nothing: only the bot's creator
// can open its link.
// @Summary Name the personal bot already created
// @Tags telegram
// @Accept json
// @Produce json
// @Param request body ClaimManagedRequest true "Bot username"
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Router /api/telegram/managed/claim [post]
func (r *Routes) ClaimManaged(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	if r.botManager.managerOf() == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: errManagedUnavailable})
		return
	}
	var req ClaimManagedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: errManagedNotFound})
		return
	}
	username := strings.TrimPrefix(strings.TrimSpace(req.Username), "@")
	_, err := database.ClaimTelegramManagedBot(c.Request.Context(), userID, username)
	switch {
	case errors.Is(err, database.ErrManagedBotNotFound):
		c.JSON(http.StatusNotFound, ErrorResponse{Error: errManagedNotFound})
		return
	case errors.Is(err, database.ErrTelegramBotTaken):
		c.JSON(http.StatusConflict, ErrorResponse{Error: errBotTaken})
		return
	case err != nil:
		logging.Errorf("User %d: claiming a managed bot: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	r.respondWithStatus(c, userID)
}

// CheckManaged is «Проверить ещё раз»: the bot the connection found, if it
// is not ready yet, is set up again.
// @Summary Check the personal bot again
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/telegram/managed/check [post]
func (r *Routes) CheckManaged(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if m := r.botManager.managerOf(); m != nil {
		if err := r.recheck(ctx, m, userID); err != nil {
			logging.Errorf("User %d: checking the managed bot: %v", userID, tgapi.RedactURL(err))
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
			return
		}
	}
	r.respondWithStatus(c, userID)
}

// recheck sets up the found bot of userID's connection again, if it is not
// ready.
func (r *Routes) recheck(ctx context.Context, m *Manager, userID int64) error {
	pc, err := database.OpenTelegramPendingConnection(ctx, userID)
	if err != nil || pc == nil || pc.BotRef == nil {
		return err
	}
	managed, err := database.GetTelegramManagedBot(ctx, *pc.BotRef)
	if err != nil || (managed != nil && managed.ReadyAt != nil) {
		return err
	}
	bot, err := database.GetTelegramBot(ctx, *pc.BotRef)
	if err != nil {
		return err
	}
	if !servable(bot) {
		return nil
	}
	return m.Recheck(ctx, bot)
}

// CancelManaged is «Отменить подключение»: the connection stops waiting. The
// bot in Telegram stays; the reader may delete it there.
// @Summary Cancel creating a personal bot
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/telegram/managed [delete]
func (r *Routes) CancelManaged(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	if err := database.CancelTelegramPendingConnection(c.Request.Context(), userID); err != nil {
		logging.Errorf("User %d: canceling a managed connection: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	r.respondWithStatus(c, userID)
}

// RestoreManaged is W-TOKEN-ERROR's «Восстановить подключение» for a bot the
// manager made: Telegram issues it a new token, on this click only.
// @Summary Restore the personal bot's token
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/telegram/managed/restore [post]
func (r *Routes) RestoreManaged(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	m := r.botManager.managerOf()
	if m == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: errManagedUnavailable})
		return
	}
	ctx := c.Request.Context()
	state, err := database.GetTelegramUserState(ctx, userID)
	if err != nil {
		logging.Errorf("User %d: reading the telegram status: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	if state.Binding == nil || state.Bot.Kind != models.TelegramBotManaged || state.Bot.Status != models.TelegramBotTokenRejected {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: errManagedNotFound})
		return
	}
	if err := m.Restore(ctx, state.Bot); err != nil {
		logging.Errorf("User %d: restoring managed bot %d: %v", userID, state.Bot.ID, tgapi.RedactURL(err))
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: errTelegramUnavailable})
		return
	}
	logging.Infof("User %d restored managed bot %d", userID, state.Bot.ID)
	r.respondWithStatus(c, userID)
}

// ApplyProfile is «Оформить бота»: the connected bot gets the library's
// avatar and descriptions, replacing its owner's — so only on the owner's
// own request, never by itself.
// @Summary Give the connected bot the library's look
// @Tags telegram
// @Produce json
// @Success 200 {object} BotStatusResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/telegram/profile [post]
func (r *Routes) ApplyProfile(c *gin.Context) {
	userID, ok := userIDOf(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	state, err := database.GetTelegramUserState(ctx, userID)
	if err != nil {
		logging.Errorf("User %d: reading the telegram status: %v", userID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	if state.Binding == nil || state.Bot.Status != models.TelegramBotActive {
		c.JSON(http.StatusConflict, ErrorResponse{Error: errNoPendingBot})
		return
	}
	b, err := r.botManager.botByRef(ctx, state.Bot.ID)
	if err != nil || b == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: errNoPendingBot})
		return
	}
	if err := profile.Apply(ctx, b.bot); err != nil {
		logging.Warnf("User %d: giving bot %d the library's profile: %v", userID, b.ref, tgapi.RedactURL(err))
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: errTelegramUnavailable})
		return
	}
	if err := database.MarkTelegramBotProfiled(ctx, b.ref); err != nil {
		logging.Errorf("User %d: recording bot %d's profile: %v", userID, b.ref, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errInternal})
		return
	}
	logging.Infof("User %d gave bot %d the library's profile", userID, b.ref)
	r.respondWithStatus(c, userID)
}
