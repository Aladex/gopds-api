package tgtest

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
)

// The manager's methods, as the fake matches them (lower case).
const (
	methodGetManagedBotToken          = "getmanagedbottoken"
	methodReplaceManagedBotToken      = "replacemanagedbottoken"
	methodSetManagedBotAccessSettings = "setmanagedbotaccesssettings"
	methodGetManagedBotAccessSettings = "getmanagedbotaccesssettings"
)

// managed is the fake's Managed Bots side: the bots a manager created, by
// bot id, with their tokens and access settings.
type managed struct {
	managers map[string]bool
	tokens   map[int64]string
	names    map[int64]string
	access   map[int64]accessSettings
	replaced int
}

type accessSettings struct {
	restricted bool
	added      []int64
}

// Manager makes token a manager bot: its getMe says it can manage bots.
func (f *FakeBotAPI) Manager(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.managers == nil {
		f.managers = map[string]bool{}
	}
	f.managers[token] = true
}

// CreateManagedBot is a reader finishing Telegram's creation window: the bot
// of token, named username, now belongs to the manager, which can get its
// token; its getMe answers with that username.
func (f *FakeBotAPI) CreateManagedBot(token, username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens == nil {
		f.tokens = map[int64]string{}
		f.names = map[int64]string{}
	}
	f.tokens[botID(token)] = token
	f.names[botID(token)] = username
}

// ManagedToken is the bot's current token, as the manager would get it.
func (f *FakeBotAPI) ManagedToken(botID int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens[botID]
}

// AccessRestricted reports what setManagedBotAccessSettings last set for the
// bot.
func (f *FakeBotAPI) AccessRestricted(botID int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.access[botID].restricted
}

// Webhook is token's webhook URL, as getWebhookInfo answers it.
func (f *FakeBotAPI) Webhook(token string) string { return f.webhookOf(token) }

func (f *FakeBotAPI) webhookOf(token string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.webhooks[token]
}

func (f *FakeBotAPI) setWebhookOf(token, url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.webhooks == nil {
		f.webhooks = map[string]string{}
	}
	f.webhooks[token] = url
}

func (f *FakeBotAPI) meFor(token string) map[string]any {
	me := BotUser(token)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.managers[token] {
		me["can_manage_bots"] = true
	}
	if name, ok := f.names[botID(token)]; ok {
		me["username"] = name
	}
	return me
}

// managedReply answers the manager's calls about the bots it created.
func (f *FakeBotAPI) managedReply(w http.ResponseWriter, c *Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.managers[c.Token] {
		writeError(w, http.StatusBadRequest, "Bad Request: the bot can't manage bots")
		return
	}
	id := c.Int64("user_id")
	token, ok := f.tokens[id]
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad Request: bot not found")
		return
	}
	switch strings.ToLower(c.Method) {
	case methodGetManagedBotToken:
		writeResult(w, token)
	case methodReplaceManagedBotToken:
		f.replaced++
		token = fmt.Sprintf("%d:AAH-replaced-%d-0123456789abcdefgh", id, f.replaced)
		f.tokens[id] = token
		writeResult(w, token)
	case methodSetManagedBotAccessSettings:
		var added []int64
		if raw := c.Params["added_user_ids"]; raw != "" {
			if err := c.Decode("added_user_ids", &added); err != nil {
				writeError(w, http.StatusBadRequest, "Bad Request: can't parse added_user_ids")
				return
			}
		}
		restricted, _ := strconv.ParseBool(c.Params["is_access_restricted"])
		if f.access == nil {
			f.access = map[int64]accessSettings{}
		}
		f.access[id] = accessSettings{restricted: restricted, added: added}
		writeResult(w, true)
	case methodGetManagedBotAccessSettings:
		a := f.access[id]
		settings := models.BotAccessSettings{IsAccessRestricted: a.restricted}
		for _, u := range a.added {
			settings.AddedUsers = append(settings.AddedUsers, models.User{ID: u, FirstName: "Guest"})
		}
		writeResult(w, settings)
	}
}

// botID is the bot id a token starts with.
func botID(token string) int64 {
	prefix, _, _ := strings.Cut(token, ":")
	id, _ := strconv.ParseInt(prefix, 10, 64)
	return id
}
