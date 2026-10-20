package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/profile"
	"gopds-api/telegram/secrets"
	"gopds-api/telegram/tgapi"

	"github.com/gin-gonic/gin"
	tgbotapi "github.com/go-telegram/bot"
	tgbot "github.com/go-telegram/bot/models"
	"github.com/google/uuid"
)

// Managed Bots (plan.md P1-1, concept §C.3): a technical manager bot creates
// each reader's personal bot from Telegram's own creation window, so nobody
// copies a token. The site starts a waiting connection; Telegram tells the
// manager a bot was made (managed_bot); the manager takes its token, restricts
// its access, points its webhook here and checks it answers; the site finds
// the bot by the label in its username, or by the username the reader gives;
// and the bot is bound only by the one-time link, whose /start must come from
// the bot's creator. The manager has no chat with readers.

// managedLinkTTL is how long «Создать личного бота» waits for its bot.
const managedLinkTTL = 15 * time.Minute

// managedSetupTimeout bounds one event's work with Telegram.
const managedSetupTimeout = 45 * time.Second

// managedBotName is the name the creation window suggests; the reader may
// change it.
const managedBotName = "Мои книги"

// The manager's webhook: its path and secret are derived, not stored, so
// every process and every start agree on them.
const (
	managerPathLabel   = "manager-webhook-path"
	managerSecretLabel = "manager-webhook-secret"
	managerSecretBytes = 32
)

// discoveryLabelBytes give the eight base32 characters of a label.
const discoveryLabelBytes = 5

// labelEncoding is lowercase base32 without padding: [a-z2-7], usable in a
// Telegram username.
var labelEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Manager is the technical bot that creates and manages readers' bots.
type Manager struct {
	bm       *BotManager
	client   *tgbotapi.Bot
	token    string
	username string
	path     string
	secret   string
}

// ErrManagerOff is the Managed path switched off: no manager token, or a
// manager Telegram does not let manage bots.
var ErrManagerOff = errors.New("telegram: the bot manager is off")

// StartManager turns the Managed path on when token is set and Telegram says
// the bot may manage bots (Bot Management Mode). Otherwise it stays off, and
// the site offers only an existing bot's token. The token is never logged.
func (bm *BotManager) StartManager(ctx context.Context, token string) error {
	if token == "" {
		logging.Info("Telegram manager: no token, Managed bots are off")
		return nil
	}
	if bm.config.Secrets == nil {
		return errors.New("telegram: no secrets box configured")
	}
	cctx, cancel := context.WithTimeout(ctx, identityTimeout)
	defer cancel()
	me, err := tgapi.GetMe(cctx, token, bm.config.API)
	if err != nil {
		return fmt.Errorf("telegram manager: getMe: %w", tgapi.RedactURL(err))
	}
	if !me.CanManageBots {
		logging.Warnf("Telegram manager @%s may not manage bots (Bot Management Mode is off): Managed bots are off", me.Username)
		return nil
	}
	client, err := tgapi.New(token, bm.config.API)
	if err != nil {
		return err
	}
	box := bm.config.Secrets
	path, err := uuid.FromBytes(box.Derive(managerPathLabel, len(uuid.UUID{})))
	if err != nil {
		return err
	}
	m := &Manager{
		bm: bm, client: client, token: token, username: me.Username,
		path:   path.String(),
		secret: hex.EncodeToString(box.Derive(managerSecretLabel, managerSecretBytes)),
	}
	if err := m.setWebhook(ctx); err != nil {
		return err
	}
	bm.mutex.Lock()
	bm.manager = m
	bm.mutex.Unlock()
	logging.Infof("Telegram manager @%s: Managed bots are on", me.Username)
	return nil
}

// managerOf is the manager, or nil when the Managed path is off.
func (bm *BotManager) managerOf() *Manager {
	bm.mutex.RLock()
	defer bm.mutex.RUnlock()
	return bm.manager
}

// setWebhook points the manager's webhook here; it wants managed_bot updates
// only.
func (m *Manager) setWebhook(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, setWebhookTimeout)
	defer cancel()
	_, err := m.client.SetWebhook(cctx, &tgbotapi.SetWebhookParams{
		URL:            fmt.Sprintf("%s/telegram/manager/%s", m.bm.config.BaseURL, m.path),
		SecretToken:    m.secret,
		AllowedUpdates: []string{tgbot.AllowedUpdateManagedBot},
	})
	if err != nil {
		return fmt.Errorf("telegram manager: setting the webhook: %w", tgapi.RedactURL(err))
	}
	return nil
}

// CreateURL is Telegram's creation window for a bot named after label.
func (m *Manager) CreateURL(label string) string {
	return fmt.Sprintf("https://t.me/newbot/%s/%s?name=%s", m.username, usernameOf(label), url.QueryEscape(managedBotName))
}

// NewDiscoveryLabel is a fresh, non-secret label for a suggested username.
func NewDiscoveryLabel() string {
	return labelEncoding.EncodeToString(secrets.Random(discoveryLabelBytes))
}

// usernameOf is the suggested username of a label: bd{label}bot, 13
// characters of [a-z0-9], ending in «bot» as Telegram requires.
func usernameOf(label string) string { return "bd" + label + "bot" }

// labelOf reads a label back from a username, if it is one of ours.
func labelOf(username string) (string, bool) {
	u := strings.ToLower(username)
	if !strings.HasPrefix(u, "bd") || !strings.HasSuffix(u, "bot") {
		return "", false
	}
	label := strings.TrimSuffix(strings.TrimPrefix(u, "bd"), "bot")
	if len(label) != labelEncoding.EncodedLen(discoveryLabelBytes) {
		return "", false
	}
	if _, err := labelEncoding.DecodeString(label); err != nil {
		return "", false
	}
	return label, true
}

// maxManagerUpdateBytes bounds one update to the manager.
const maxManagerUpdateBytes = 1 << 20

// managedEventLease is how long a process holds an event it handles: past
// it, the event is another process's to handle (sweepEvents).
const managedEventLease = 2 * managedSetupTimeout

// managedSweepBatch bounds the events one sweep takes up.
const managedSweepBatch = 20

// HandleManagerWebhook takes the manager's updates: managed_bot ones, each
// handled once, by one process, whatever Telegram repeats. A failed handling
// answers 500 and lets the event go, so Telegram delivers it again and it is
// tried again; one a stopped process left half done is taken up by the
// sweep.
func (bm *BotManager) HandleManagerWebhook(c *gin.Context) {
	m := bm.managerOf()
	if m == nil || subtle.ConstantTimeCompare([]byte(c.Param("path")), []byte(m.path)) != 1 {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: errBotNotFound})
		return
	}
	if subtle.ConstantTimeCompare([]byte(c.GetHeader(webhookSecretHeader)), []byte(m.secret)) != 1 {
		logging.Warn("Telegram manager: webhook secret mismatch")
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errInvalidSecret})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxManagerUpdateBytes))
	update := new(tgbot.Update)
	if err != nil || json.Unmarshal(body, update) != nil {
		answerStatus(c, statusSkipped)
		return
	}
	ev := update.ManagedBot
	if ev == nil || ev.User == nil || ev.Bot == nil {
		answerStatus(c, statusSkipped)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), managedSetupTimeout)
	defer cancel()
	event := &models.TelegramManagedEvent{
		UpdateID: update.ID, BotTGID: ev.Bot.ID, CreatorTGID: ev.User.ID, BotUsername: ev.Bot.Username,
	}
	claimed, err := database.ClaimTelegramManagedEvent(ctx, event, bm.process, managedEventLease)
	if err != nil {
		logging.Errorf("Telegram manager: recording update %d: %v", update.ID, err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	if !claimed {
		// Handled already, or being handled by another process, which
		// answers for it.
		answerStatus(c, statusDuplicate)
		return
	}
	if err := m.run(ctx, event); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errUnavailable})
		return
	}
	answerStatus(c, statusOK)
}

// run handles a claimed event: marked handled when done, let go when not.
func (m *Manager) run(ctx context.Context, event *models.TelegramManagedEvent) error {
	if err := m.handle(ctx, event); err != nil {
		logging.Errorf("Telegram manager: handling update %d (bot %d): %v", event.UpdateID, event.BotTGID, tgapi.RedactURL(err))
		if relErr := database.ReleaseTelegramManagedEvent(context.WithoutCancel(ctx), event.UpdateID, m.bm.process); relErr != nil {
			logging.Warnf("Telegram manager: letting update %d go: %v", event.UpdateID, relErr)
		}
		return err
	}
	if err := database.MarkTelegramManagedEventProcessed(ctx, event.UpdateID); err != nil {
		logging.Warnf("Telegram manager: marking update %d handled: %v", event.UpdateID, err)
	}
	return nil
}

// sweepEvents handles the events a stopped process left unhandled.
func (m *Manager) sweepEvents(ctx context.Context) {
	events, err := database.ClaimStaleTelegramManagedEvents(ctx, m.bm.process, managedEventLease, managedSweepBatch)
	if err != nil {
		logging.Warnf("Telegram manager: sweeping events: %v", err)
		return
	}
	for i := range events {
		ectx, cancel := context.WithTimeout(ctx, managedSetupTimeout)
		_ = m.run(ectx, &events[i])
		cancel()
	}
}

// handle acts on one managed_bot event: a new bot is taken on, a known one
// looked at again (handleKnown). Events of one bot apply in update_id
// order: an older one delivered late changes nothing.
func (m *Manager) handle(ctx context.Context, event *models.TelegramManagedEvent) error {
	known, err := database.GetTelegramBotByTGID(ctx, event.BotTGID)
	if err != nil {
		return err
	}
	if known != nil {
		return m.handleKnown(ctx, known, event)
	}
	bot, applied, err := m.takeOn(ctx, event)
	if err != nil || !applied || bot.Status != models.TelegramBotActive {
		return err
	}
	err = m.setUp(ctx, bot)
	if err != nil {
		return err
	}
	label, ok := labelOf(event.BotUsername)
	if !ok {
		return nil
	}
	userID, err := database.FindTelegramPendingConnection(ctx, label, bot.ID)
	if err == nil && userID != 0 {
		logging.Infof("Telegram manager: bot %d found by user %d's connection", bot.ID, userID)
	}
	return err
}

// handleKnown acts on an event about a bot already registered: one with
// another creator changed owner and is suspended; one of the same creator (a
// token change, or a repeat) is set up again only if it never got ready. A
// changed token is not taken by itself: restoring is the owner's click
// (concept §C.3, W-TOKEN-ERROR).
func (m *Manager) handleKnown(ctx context.Context, known *models.TelegramBot, event *models.TelegramManagedEvent) error {
	if known.Kind != models.TelegramBotManaged {
		// A bot connected by its token before is not the manager's to take.
		logging.Warnf("Telegram manager: bot %d is connected by token, not taken over", known.ID)
		return nil
	}
	applied, changed, err := database.ApplyTelegramManagedOwner(ctx, known.ID, event.UpdateID, event.CreatorTGID)
	switch {
	case err != nil:
		return err
	case !applied:
		logging.Infof("Telegram manager: update %d about bot %d is older than one applied; ignored", event.UpdateID, known.ID)
		return nil
	case changed:
		logging.Infof("Telegram manager: bot %d changed owner, suspended until linked again", known.ID)
		return nil
	}
	if known.TokenEnc == nil || known.Status != models.TelegramBotActive {
		return nil
	}
	state, err := database.GetTelegramManagedBot(ctx, known.ID)
	if err != nil || (state != nil && state.ReadyAt != nil) {
		return err
	}
	return m.setUp(ctx, known)
}

// takeOn registers a bot the manager has just been told of, with the token
// Telegram gives the manager. applied is false when an event newer than
// this one registered the bot meanwhile.
func (m *Manager) takeOn(ctx context.Context, event *models.TelegramManagedEvent) (*models.TelegramBot, bool, error) {
	token, err := m.client.GetManagedBotToken(ctx, &tgbotapi.GetManagedBotTokenParams{UserID: event.BotTGID})
	if err != nil {
		return nil, false, fmt.Errorf("getting the token: %w", tgapi.RedactURL(err))
	}
	model := m.bm.newBotModel(event.BotTGID, token, uuid.New())
	model.Kind = models.TelegramBotManaged
	creator := event.CreatorTGID
	model.CreatorTGID = &creator
	model.Username = event.BotUsername
	model.DisplayName = managedBotName
	return database.RegisterManagedTelegramBot(ctx, model, event.UpdateID)
}

// setUp makes a managed bot ready: access restricted to its owner, the
// webhook here, and both confirmed by reading them back (concept §C.3: a
// token alone is not «ready»). What did not work is left for «Проверить ещё
// раз»; an error is only a failure to record.
func (m *Manager) setUp(ctx context.Context, bot *models.TelegramBot) error {
	restricted := m.restrict(ctx, bot)
	b, err := m.bm.serve(bot)
	if err != nil {
		return err
	}
	ready := restricted && m.profiled(ctx, b) && m.bm.setWebhook(b) == nil && m.answers(ctx, b)
	if !ready {
		logging.Warnf("Telegram manager: bot %d is not ready yet", bot.ID)
	}
	return database.SetTelegramManagedBot(ctx, bot.ID, restricted, ready)
}

// profiled gives a new managed bot the library's avatar and descriptions,
// once (plan.md P1-2), and reports whether it has them. A bot that has them
// is not given them again: its owner may have changed them since.
func (m *Manager) profiled(ctx context.Context, b *Bot) bool {
	done, err := database.TelegramBotProfiled(ctx, b.ref)
	if err != nil {
		logging.Warnf("Telegram manager: reading bot %d's profile: %v", b.ref, err)
		return false
	}
	if done {
		return true
	}
	if err := profile.Apply(ctx, b.bot); err != nil {
		logging.Warnf("Telegram manager: giving bot %d its profile: %v", b.ref, tgapi.RedactURL(err))
		return false
	}
	if err := database.MarkTelegramBotProfiled(ctx, b.ref); err != nil {
		logging.Warnf("Telegram manager: recording bot %d's profile: %v", b.ref, err)
		return false
	}
	return true
}

// restrict lets only the bot's owner use it, and reports whether Telegram
// says so when asked back.
func (m *Manager) restrict(ctx context.Context, bot *models.TelegramBot) bool {
	if _, err := m.client.SetManagedBotAccessSettings(ctx, &tgbotapi.SetManagedBotAccessSettingsParams{
		UserID: bot.TGBotID, IsAccessRestricted: true,
	}); err != nil {
		logging.Warnf("Telegram manager: restricting bot %d: %v", bot.ID, tgapi.RedactURL(err))
		return false
	}
	settings, err := m.client.GetManagedBotAccessSettings(ctx, &tgbotapi.GetManagedBotAccessSettingsParams{UserID: bot.TGBotID})
	if err != nil {
		logging.Warnf("Telegram manager: reading bot %d's access: %v", bot.ID, tgapi.RedactURL(err))
		return false
	}
	return settings.IsAccessRestricted && len(settings.AddedUsers) == 0
}

// answers reports a bot whose webhook points here and whose token works.
func (m *Manager) answers(ctx context.Context, b *Bot) bool {
	info, err := b.bot.GetWebhookInfo(ctx)
	if err != nil || info.URL != fmt.Sprintf("%s/telegram/%s", m.bm.config.BaseURL, b.webhookPath) {
		return false
	}
	_, err = tgapi.GetMe(ctx, b.token, m.bm.config.API)
	return err == nil
}

// Recheck is «Проверить ещё раз»: the found bot is set up again.
func (m *Manager) Recheck(ctx context.Context, bot *models.TelegramBot) error {
	return m.setUp(ctx, bot)
}

// Restore is W-TOKEN-ERROR's «Восстановить подключение» for a managed bot,
// on its owner's click only: Telegram issues a new token through the
// manager, and the bot is set up with it.
func (m *Manager) Restore(ctx context.Context, bot *models.TelegramBot) error {
	token, err := m.client.ReplaceManagedBotToken(ctx, &tgbotapi.ReplaceManagedBotTokenParams{UserID: bot.TGBotID})
	if err != nil {
		return fmt.Errorf("replacing the token: %w", tgapi.RedactURL(err))
	}
	keyID, sealed := m.bm.config.Secrets.Seal(secrets.PurposeToken, []byte(token))
	err = database.ReplaceTelegramBotToken(ctx, bot.ID, keyID, sealed, secrets.TokenHash(token))
	if err != nil {
		return err
	}
	fresh, err := database.GetTelegramBot(ctx, bot.ID)
	if err != nil {
		return err
	}
	return m.setUp(ctx, fresh)
}
