package telegram

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopds-api/commands"
	"gopds-api/config"
	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"
	"gopds-api/services/kindlesend"
	"gopds-api/telegram/delivery"
	"gopds-api/telegram/router"
	"gopds-api/telegram/secrets"
	"gopds-api/telegram/tgapi"

	"github.com/go-pg/pg/v10"
	"github.com/go-redis/redis"
	tgbotapi "github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/spf13/viper"
)

// Config contains settings for bots
type Config struct {
	BaseURL string // base URL for webhooks
	// SiteURL is the library's address as readers know it; the bot's buttons
	// that send someone to the site point here.
	SiteURL string
	// API is the Bot API server the bots call; the zero value is Telegram's
	// cloud API. Tests point it at a tgtest fake.
	API tgapi.Endpoint
	// Secrets seals the bots' tokens, webhook secrets and button keys.
	Secrets *secrets.Box
	// Kindle sends books to Kindle from the bot; nil is the site's shared
	// sender (kindlesend.Default). Tests put a fake relay here.
	Kindle *kindlesend.Sender
	// PublicMediaURL is where Telegram can fetch covers from, for inline
	// results; "" means none.
	PublicMediaURL string
	// ChatWait is how long a v2 update waits for its chat, held by another
	// transition, before it is handed back; zero is the router's default.
	ChatWait time.Duration
	// Donate is how the library may be supported; none hides the button.
	Donate []config.DonateMethod
}

// BotManager serves the personal bots: it knows them by webhook path, takes
// their updates into the inbox and hands them to their handlers.
type BotManager struct {
	bots   map[string]*Bot // token -> Bot
	byPath map[string]*Bot // webhook path -> Bot
	byRef  map[int64]*Bot  // telegram_bot.id -> Bot
	mutex  sync.RWMutex

	config              *Config
	redis               *redis.Client
	conversationManager *ConversationManager
	// newProcessor builds command processors on the one shared search
	// service; there is no global default service to fall back to.
	newProcessor func() *commands.CommandProcessor

	// delivery sends book files as durable jobs; nil until SetDelivery.
	delivery *delivery.Service
	// search is the one shared search service; router serves the bots whose
	// router is v2, from SetDelivery on.
	search services.PublicSearch
	router *router.Router

	// process identifies this process as the holder of inbox leases.
	process  uuid.UUID
	inflight chan struct{}
	handling sync.WaitGroup
}

// Bot is one served bot.
type Bot struct {
	token         string
	bot           *tgbotapi.Bot
	ref           int64 // telegram_bot.id
	tgBotID       int64
	username      string
	addedBy       *int64 // the account that entered or owned it first, for logs
	webhookPath   string
	webhookSecret string
	newProcessor  func() *commands.CommandProcessor
	manager       *BotManager
}

// navigationRetention is how long an unused v2 view or node is kept
// (plan.md §2.4).
const navigationRetention = 90 * 24 * time.Hour

// maxInflightUpdates bounds how many updates one process handles at once.
const maxInflightUpdates = 16

// Sizes of a new bot's secrets, in random bytes.
const (
	// webhookSecretBytes, hex-encoded, give a 48-character secret_token
	// (Telegram allows 1–256 of [A-Za-z0-9_-]).
	webhookSecretBytes = 24
	// navKeyBytes is the HMAC key of the bot's signed buttons.
	navKeyBytes = 32
)

// Timeouts of the calls the manager makes itself.
const (
	setWebhookTimeout  = 10 * time.Second
	dropWebhookTimeout = 5 * time.Second
	healthCallTimeout  = 10 * time.Second
)

// NewBotManager creates a new bot manager whose bots build command
// processors on the one shared search service created in main, naming the
// authors of the books they list by lines (nil: the legacy authors).
func NewBotManager(config *Config, redisClient *redis.Client, search services.PublicSearch, lines *services.AuthorLines) *BotManager {
	return &BotManager{
		bots:                make(map[string]*Bot),
		byPath:              make(map[string]*Bot),
		byRef:               make(map[int64]*Bot),
		config:              config,
		redis:               redisClient,
		conversationManager: NewConversationManager(redisClient),
		newProcessor: func() *commands.CommandProcessor {
			return commands.NewCommandProcessor(search, lines)
		},
		search:   search,
		process:  uuid.New(),
		inflight: make(chan struct{}, maxInflightUpdates),
	}
}

// InitializeExistingBots copies bots connected the old way into
// telegram_bot, then serves every active bot and points its webhook here.
func (bm *BotManager) InitializeExistingBots() error {
	if bm.config.Secrets == nil {
		return errors.New("telegram: no secrets box configured")
	}
	ctx := context.Background()
	bm.adoptLegacyBots(ctx)

	bots, err := database.ListServableTelegramBots(ctx)
	if err != nil {
		return fmt.Errorf("listing telegram bots: %w", err)
	}
	served := 0
	v2 := map[int64]bool{}
	for i := range bots {
		b, err := bm.serve(&bots[i])
		if err != nil {
			logging.Errorf("Failed to serve telegram bot %d: %v", bots[i].ID, err)
			continue
		}
		v2[b.ref] = bots[i].Router == models.TelegramRouterV2
		if err := bm.setWebhook(b); err != nil {
			logging.Errorf("Failed to set webhook for telegram bot %d: %v", bots[i].ID, err)
		}
		served++
	}
	logging.Infof("Serving %d telegram bots", served)
	bm.handling.Add(1)
	go func() {
		defer bm.handling.Done()
		bm.refreshIdentities(v2)
	}()
	return nil
}

// identityWorkers bounds the getMe calls made at once to learn bot names.
const identityWorkers = 4

// identityTimeout bounds one getMe.
const identityTimeout = 10 * time.Second

// refreshIdentities learns the public username and name of every served bot
// that has none — the adopted legacy bots: their rows were made from a token
// alone — and whether each v2 bot answers inline queries (plan-ui.md §3.1).
// One getMe per bot, a few at a time; a bot that fails is logged and tried
// again by the next health check, and never holds up the others.
func (bm *BotManager) refreshIdentities(v2 map[int64]bool) {
	bm.mutex.RLock()
	var nameless []*Bot
	for _, b := range bm.byRef {
		if b.username == "" || v2[b.ref] {
			nameless = append(nameless, b)
		}
	}
	bm.mutex.RUnlock()

	slots := make(chan struct{}, identityWorkers)
	var wg sync.WaitGroup
	for _, b := range nameless {
		wg.Add(1)
		slots <- struct{}{}
		go func(b *Bot) {
			defer wg.Done()
			defer func() { <-slots }()
			bm.refreshIdentity(b)
		}(b)
	}
	wg.Wait()
}

// refreshIdentity asks Telegram for one bot's username and name, and whether
// it answers inline queries, and keeps them.
func (bm *BotManager) refreshIdentity(b *Bot) {
	ctx, cancel := context.WithTimeout(context.Background(), identityTimeout)
	defer cancel()
	me, err := tgapi.GetMe(ctx, b.token, bm.config.API)
	if err != nil {
		logging.Warnf("Telegram bot %d of user %s: reading its name: %v", b.ref, userOf(b.addedBy), tgapi.RedactURL(err))
		return
	}
	if err := database.SetTelegramBotIdentity(ctx, b.ref, me.Username, me.FirstName); err != nil {
		logging.Warnf("Telegram bot %d of user %s: storing its name: %v", b.ref, userOf(b.addedBy), err)
		return
	}
	bm.mutex.Lock()
	b.username = me.Username
	bm.mutex.Unlock()
	if _, err := database.SetTelegramInline(ctx, b.ref, me.SupportsInline); err != nil {
		logging.Warnf("Telegram bot %d: storing whether it answers inline queries: %v", b.ref, err)
	}
}

// CheckInline asks Telegram whether a served bot answers inline queries, and
// keeps the answer: «Проверить» on the bot's inline instructions.
func (bm *BotManager) CheckInline(ctx context.Context, ref int64) (bool, error) {
	b, err := bm.botByRef(ctx, ref)
	if err != nil {
		return false, err
	}
	if b == nil {
		return false, fmt.Errorf("telegram bot %d is not served", ref)
	}
	cctx, cancel := context.WithTimeout(ctx, identityTimeout)
	defer cancel()
	me, err := tgapi.GetMe(cctx, b.token, bm.config.API)
	if err != nil {
		return false, err
	}
	if _, err := database.SetTelegramInline(ctx, ref, me.SupportsInline); err != nil {
		return false, err
	}
	return me.SupportsInline, nil
}

// servedByV2 reports a bot switched to the v2 router; false when unknown.
func (bm *BotManager) servedByV2(ctx context.Context, ref int64) bool {
	model, err := database.GetTelegramBot(ctx, ref)
	return err == nil && model.Router == models.TelegramRouterV2
}

// userOf names an account id in logs.
func userOf(id *int64) string {
	if id == nil {
		return "unknown"
	}
	return strconv.FormatInt(*id, 10)
}

// adoptLegacyBots copies every auth_user bot not yet in telegram_bot. Each
// row stands alone: one bad token is logged by user id and skipped. Running
// it again, or in two processes during a rollout, adopts nothing twice.
func (bm *BotManager) adoptLegacyBots(ctx context.Context) {
	legacy, err := database.ListLegacyTelegramBots(ctx)
	if err != nil {
		logging.Errorf("Listing legacy telegram bots: %v", err)
		return
	}
	adopted := 0
	for _, l := range legacy {
		bot, err := bm.legacyBotModel(l)
		if err != nil {
			logging.Warnf("Skipping the legacy telegram bot of user %d: %v", l.UserID, err)
			continue
		}
		ok, err := database.AdoptLegacyTelegramBot(ctx, bot, l.TelegramID)
		if err != nil {
			logging.Errorf("Adopting the legacy telegram bot of user %d: %v", l.UserID, err)
			continue
		}
		if ok {
			adopted++
		}
	}
	if adopted > 0 {
		logging.Infof("Adopted %d legacy telegram bots", adopted)
	}
}

// legacyBotModel builds the telegram_bot row of a legacy bot. It keeps the
// webhook path Telegram already calls; the secret is new.
func (bm *BotManager) legacyBotModel(l database.LegacyTelegramBot) (*models.TelegramBot, error) {
	tgBotID, err := botIDOfToken(l.Token)
	if err != nil {
		return nil, err
	}
	path, err := uuid.Parse(l.WebhookUUID)
	if err != nil {
		path = uuid.New()
	}
	userID := l.UserID
	bot := bm.newBotModel(tgBotID, l.Token, path)
	bot.AddedByUserID = &userID
	return bot, nil
}

// newBotModel seals a new bot's token and gives it fresh secrets.
func (bm *BotManager) newBotModel(tgBotID int64, token string, path uuid.UUID) *models.TelegramBot {
	box := bm.config.Secrets
	keyID, tokenEnc := box.Seal(secrets.PurposeToken, []byte(token))
	_, secretEnc := box.Seal(secrets.PurposeWebhookSecret, []byte(hex.EncodeToString(secrets.Random(webhookSecretBytes))))
	_, navEnc := box.Seal(secrets.PurposeNavKey, secrets.Random(navKeyBytes))
	return &models.TelegramBot{
		TGBotID:          tgBotID,
		Kind:             models.TelegramBotManual,
		TokenEnc:         tokenEnc,
		TokenSHA256:      secrets.TokenHash(token),
		KeyID:            keyID,
		WebhookPath:      path,
		WebhookSecretEnc: secretEnc,
		NavKeyEnc:        navEnc,
		NextGeneration:   1,
		Status:           models.TelegramBotActive,
		Router:           "legacy",
	}
}

// freshNavKey is a sealed navigation key for a bot whose generations wrap.
func (bm *BotManager) freshNavKey() []byte {
	_, navEnc := bm.config.Secrets.Seal(secrets.PurposeNavKey, secrets.Random(navKeyBytes))
	return navEnc
}

// botIDOfToken reads the bot's numeric id, the part of a token before ':'.
func botIDOfToken(token string) (int64, error) {
	prefix, _, ok := strings.Cut(token, ":")
	if !ok {
		return 0, errors.New("token has no bot id")
	}
	id, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("token has no bot id")
	}
	return id, nil
}

// serve opens a stored bot's secrets and starts serving it, replacing a bot
// already served under the same row.
func (bm *BotManager) serve(model *models.TelegramBot) (*Bot, error) {
	box := bm.config.Secrets
	token, err := box.Open(model.KeyID, secrets.PurposeToken, model.TokenEnc)
	if err != nil {
		return nil, err
	}
	secret, err := box.Open(model.KeyID, secrets.PurposeWebhookSecret, model.WebhookSecretEnc)
	if err != nil {
		return nil, err
	}
	b, err := bm.newBot(string(token), model)
	if err != nil {
		return nil, err
	}
	b.webhookSecret = string(secret)

	bm.mutex.Lock()
	defer bm.mutex.Unlock()
	if old, ok := bm.byRef[model.ID]; ok {
		delete(bm.bots, old.token)
		delete(bm.byPath, old.webhookPath)
	}
	bm.bots[b.token] = b
	bm.byPath[b.webhookPath] = b
	bm.byRef[b.ref] = b
	return b, nil
}

// newBot builds the SDK client of one bot and registers its handlers.
// Handlers run synchronously: the inbox dispatcher decides concurrency and
// needs to know when an update is done.
func (bm *BotManager) newBot(token string, model *models.TelegramBot) (*Bot, error) {
	ref := model.ID
	teleBot, err := tgapi.New(token, bm.config.API,
		tgbotapi.WithHTTPClient(time.Minute, bm.observedClient(ref)),
		tgbotapi.WithNotAsyncHandlers(),
		tgbotapi.WithMiddlewares(recoverMiddleware, privateChatMiddleware),
		tgbotapi.WithDefaultHandler(func(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update) {
			logging.Debugf("Unhandled update type in telegram bot %d", ref)
		}),
	)
	if err != nil {
		return nil, err
	}
	b := &Bot{
		token:        token,
		bot:          teleBot,
		ref:          ref,
		tgBotID:      model.TGBotID,
		username:     model.Username,
		addedBy:      model.AddedByUserID,
		webhookPath:  model.WebhookPath.String(),
		newProcessor: bm.newProcessor,
		manager:      bm,
	}
	b.setupHandlers(bm.conversationManager)
	// The v2 bot has no commands; its menu is cleared by the router.
	if model.Router == models.TelegramRouterLegacy {
		if err := b.registerCommands(); err != nil {
			logging.Warnf("Failed to register commands for telegram bot %d: %v", ref, err)
		}
	}
	return b, nil
}

// botByPath finds a served bot by webhook path. A bot this process does not
// know yet — connected through another process during a rollout — is loaded
// from the database.
func (bm *BotManager) botByPath(ctx context.Context, path string) (*Bot, error) {
	bm.mutex.RLock()
	b, ok := bm.byPath[path]
	bm.mutex.RUnlock()
	if ok {
		return b, nil
	}
	id, err := uuid.Parse(path)
	if err != nil {
		return nil, nil
	}
	model, err := database.GetTelegramBotByWebhookPath(ctx, id)
	if errors.Is(err, pg.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if model.Status != models.TelegramBotActive || model.TokenEnc == nil {
		return nil, nil
	}
	return bm.serve(model)
}

// botByRef finds a served bot by its row id, loading it when needed.
func (bm *BotManager) botByRef(ctx context.Context, ref int64) (*Bot, error) {
	bm.mutex.RLock()
	b, ok := bm.byRef[ref]
	bm.mutex.RUnlock()
	if ok {
		return b, nil
	}
	model, err := database.GetTelegramBot(ctx, ref)
	if err != nil {
		return nil, err
	}
	if model.Status != models.TelegramBotActive || model.TokenEnc == nil {
		return nil, nil
	}
	return bm.serve(model)
}

// SetDelivery gives the bots the service that sends their files, and builds
// the v2 router on it.
func (bm *BotManager) SetDelivery(d *delivery.Service) {
	bm.delivery = d
	bm.router = router.New(&router.Config{
		Search:       bm.search,
		Delivery:     d,
		SiteURL:      bm.config.SiteURL,
		ChatWait:     bm.config.ChatWait,
		Donate:       bm.config.Donate,
		MediaBaseURL: bm.config.PublicMediaURL,
		Kindle:       bm.config.Kindle,
		PostersPath:  func() string { return viper.GetString("app.posters_path") },
	}, bm)
	d.SetCards(bm.router)
}

// V2Bot is a served bot as the v2 router knows it, or nil when the bot is
// not served by the v2 router. The router, the navigation key and the
// binding are read fresh: a bot is switched by its row, without a restart.
func (bm *BotManager) V2Bot(ctx context.Context, ref int64) (*router.Bot, error) {
	b, err := bm.botByRef(ctx, ref)
	if err != nil || b == nil {
		return nil, err
	}
	return bm.v2Bot(ctx, b)
}

func (bm *BotManager) v2Bot(ctx context.Context, b *Bot) (*router.Bot, error) {
	if bm.router == nil {
		return nil, nil
	}
	model, err := database.GetTelegramBot(ctx, b.ref)
	if err != nil {
		return nil, err
	}
	if model.Router != models.TelegramRouterV2 {
		return nil, nil
	}
	navKey, err := bm.config.Secrets.Open(model.KeyID, secrets.PurposeNavKey, model.NavKeyEnc)
	if err != nil {
		return nil, err
	}
	binding, err := database.GetActiveTelegramBinding(ctx, b.ref)
	if err != nil {
		return nil, err
	}
	return &router.Bot{Client: b.bot, Ref: b.ref, TGBotID: b.tgBotID, Username: b.username,
		Active: model.Status == models.TelegramBotActive, NavKey: navKey, Binding: binding}, nil
}

// Client is the SDK client of a served bot, for the delivery service.
func (bm *BotManager) Client(ctx context.Context, botRef int64) (*tgbotapi.Bot, error) {
	b, err := bm.botByRef(ctx, botRef)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("telegram bot %d is not served", botRef)
	}
	return b.bot, nil
}

// forget stops serving a bot in this process.
func (bm *BotManager) forget(ref int64) {
	bm.mutex.Lock()
	defer bm.mutex.Unlock()
	if b, ok := bm.byRef[ref]; ok {
		delete(bm.bots, b.token)
		delete(bm.byPath, b.webhookPath)
		delete(bm.byRef, ref)
	}
}

// setWebhook points the bot's webhook at this service with its secret.
// setWebhook replaces whatever is registered, so there is nothing to delete
// first.
func (bm *BotManager) setWebhook(b *Bot) error {
	ctx, cancel := context.WithTimeout(context.Background(), setWebhookTimeout)
	defer cancel()
	webhookURL := fmt.Sprintf("%s/telegram/%s", bm.config.BaseURL, b.webhookPath)
	_, err := b.bot.SetWebhook(ctx, &tgbotapi.SetWebhookParams{
		URL:         webhookURL,
		SecretToken: b.webhookSecret,
	})
	if err != nil {
		return fmt.Errorf("setting the webhook: %w", tgapi.RedactURL(err))
	}
	logging.Infof("Webhook set for telegram bot %d", b.ref)
	return nil
}

// dropWebhook removes a bot's webhook. DropPendingUpdates carries the request
// as much as it states the intent: every field of these params is omitempty,
// and an empty form is rejected by Telegram.
func (bm *BotManager) dropWebhook(token string) {
	client, err := tgapi.New(token, bm.config.API)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dropWebhookTimeout)
	defer cancel()
	if _, err := client.DeleteWebhook(ctx, &tgbotapi.DeleteWebhookParams{DropPendingUpdates: true}); err != nil {
		logging.Warnf("Failed to remove a telegram webhook: %v", tgapi.RedactURL(err))
	}
}

// GetBotCount returns the number of served bots.
func (bm *BotManager) GetBotCount() int {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()
	return len(bm.bots)
}

// ListActiveBots lists the served bots by row id and masked token.
func (bm *BotManager) ListActiveBots() []string {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()

	tokens := make([]string, 0, len(bm.bots))
	for token, bot := range bm.bots {
		tokens = append(tokens, fmt.Sprintf("%s (bot %d)", maskToken(token), bot.ref))
	}
	return tokens
}

// GetConversationManager returns the conversation manager instance
func (bm *BotManager) GetConversationManager() *ConversationManager {
	return bm.conversationManager
}

// GetConversationContext returns conversation context for a specific user and bot
func (bm *BotManager) GetConversationContext(token string, userID int64) (*ConversationContext, error) {
	return bm.conversationManager.GetContext(token, userID)
}

// GetConversationContextAsString returns conversation context as formatted string
func (bm *BotManager) GetConversationContextAsString(token string, userID int64) (string, error) {
	return bm.conversationManager.GetContextAsString(token, userID)
}

// ClearConversationContext clears conversation context for a specific user and bot
func (bm *BotManager) ClearConversationContext(token string, userID int64) error {
	return bm.conversationManager.ClearContext(token, userID)
}

// StartHealthCheck starts a periodic health check goroutine for all bots
func (bm *BotManager) StartHealthCheck(ctx context.Context, interval time.Duration) {
	go func() {
		logging.Infof("Bot health check started with interval %v", interval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logging.Info("Bot health check stopped")
				return
			case <-ticker.C:
				bm.runHealthCheck()
				// The v2 bot's views and nodes: what was not used for
				// navigationRetention is gone, and its old buttons say so.
				if err := database.PruneTelegramNavigation(ctx, navigationRetention); err != nil {
					logging.Warnf("Pruning telegram navigation: %v", err)
				}
			}
		}
	}()
}

// runHealthCheck checks every served bot. A token Telegram rejects stops the
// bot being served and marks it token_rejected — the connection is kept, so
// the owner sees on the site that the bot needs a new token rather than
// finding it silently gone. A webhook pointing elsewhere is set again.
func (bm *BotManager) runHealthCheck() {
	bm.mutex.RLock()
	served := make([]*Bot, 0, len(bm.byRef))
	for _, b := range bm.byRef {
		served = append(served, b)
	}
	bm.mutex.RUnlock()

	logging.Infof("Running health check for %d bots", len(served))
	ctx := context.Background()
	for _, b := range served {
		valid, err := bm.validateBotToken(b.token)
		if err != nil {
			logging.Warnf("Health check: failed to validate telegram bot %d: %v", b.ref, err)
			continue
		}
		if !valid {
			logging.Warnf("Health check: telegram bot %d has a rejected token", b.ref)
			if markErr := database.SetTelegramBotStatus(ctx, b.ref, models.TelegramBotTokenRejected); markErr != nil {
				logging.Errorf("Health check: failed to mark telegram bot %d: %v", b.ref, markErr)
			}
			bm.forget(b.ref)
			continue
		}

		bm.mutex.RLock()
		nameless := b.username == ""
		bm.mutex.RUnlock()
		if nameless || bm.servedByV2(ctx, b.ref) {
			bm.refreshIdentity(b)
		}

		isCorrect, err := bm.checkWebhookStatus(b)
		if err != nil {
			logging.Warnf("Health check: failed to check the webhook of telegram bot %d: %v", b.ref, err)
			continue
		}
		if !isCorrect {
			logging.Infof("Health check: webhook of telegram bot %d misconfigured, resetting", b.ref)
			if setErr := bm.setWebhook(b); setErr != nil {
				logging.Errorf("Health check: %v", setErr)
			}
		}
	}
	logging.Info("Bot health check completed")
}

// rawClient is the client of the health check's direct calls: the endpoint's
// own when it names one, otherwise a short-lived default; like every client
// of the endpoint, it does not follow redirects.
func (bm *BotManager) rawClient() *http.Client {
	client := bm.config.API.Client()
	if bm.config.API.HTTPClient == nil {
		client.Timeout = rawCallTimeout
	}
	return client
}

// rawCallTimeout bounds one direct health-check call.
const rawCallTimeout = 8 * time.Second

// checkWebhookStatus reports whether the bot's webhook points at this service.
func (bm *BotManager) checkWebhookStatus(b *Bot) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), healthCallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bm.config.API.MethodURL(b.token, "getWebhookInfo"), http.NoBody)
	if err != nil {
		return false, tgapi.RedactURL(err)
	}
	resp, err := bm.rawClient().Do(req)
	if err != nil {
		return false, tgapi.RedactURL(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("getWebhookInfo returned status %d", resp.StatusCode)
	}
	var result struct {
		Ok     bool `json:"ok"`
		Result struct {
			URL string `json:"url"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	if !result.Ok {
		return false, errors.New("getWebhookInfo returned ok=false")
	}
	return result.Result.URL == fmt.Sprintf("%s/telegram/%s", bm.config.BaseURL, b.webhookPath), nil
}

// validateBotToken checks if a bot token is valid by calling Telegram's getMe API
func (bm *BotManager) validateBotToken(token string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), healthCallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bm.config.API.MethodURL(token, "getMe"), http.NoBody)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %v", tgapi.RedactURL(err))
	}
	resp, err := bm.rawClient().Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to call getMe: %v", tgapi.RedactURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("getMe returned status %d", resp.StatusCode)
	}
	var result struct {
		Ok bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode getMe response: %v", err)
	}
	return result.Ok, nil
}

// maskToken names a token in logs by its bot id only: the part after the
// colon is the secret, and no character of it is logged.
func maskToken(token string) string {
	const hidden = "***"
	prefix, _, ok := strings.Cut(token, ":")
	if !ok || prefix == "" {
		return hidden
	}
	return prefix + ":" + hidden
}
