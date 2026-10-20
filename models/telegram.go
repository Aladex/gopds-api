//lint:file-ignore U1000 tableName is read by go-pg through reflection to
// name the table; staticcheck sees a field nobody mentions.

package models

import (
	"time"

	"github.com/google/uuid"
)

// Kinds of telegram_bot.
const (
	TelegramBotManual  = "manual"
	TelegramBotManaged = "managed"
)

// States of telegram_bot.status.
const (
	TelegramBotActive        = "active"
	TelegramBotTokenRejected = "token_rejected"
	TelegramBotOwnerChanged  = "owner_changed"
	TelegramBotDisabled      = "disabled"
)

// States of telegram_binding.status.
const (
	TelegramBindingActive  = "active"
	TelegramBindingRevoked = "revoked"
)

// TelegramBot is one personal bot the library talks to. The secrets are
// sealed by telegram/secrets; nothing here is readable without the key.
type TelegramBot struct {
	tableName        struct{} `pg:"telegram_bot"`
	ID               int64
	TGBotID          int64  `pg:"tg_bot_id"`
	Username         string `pg:",use_zero"`
	DisplayName      string `pg:",use_zero"`
	Kind             string
	AddedByUserID    *int64
	TokenEnc         []byte
	TokenSHA256      []byte `pg:"token_sha256"`
	KeyID            int16
	WebhookPath      uuid.UUID `pg:"type:uuid"`
	WebhookSecretEnc []byte
	NavKeyEnc        []byte
	NextGeneration   int32
	CreatorTGID      *int64 `pg:"creator_tg_id"`
	Status           string
	Router           string
	CreatedAt        time.Time `pg:"default:now()"`
	UpdatedAt        time.Time `pg:"default:now()"`
}

// TelegramBinding says which library account a bot serves and which
// Telegram user owns it.
type TelegramBinding struct {
	tableName        struct{} `pg:"telegram_binding"`
	ID               int64
	UserID           int64
	BotRef           int64
	TGUserID         int64  `pg:"tg_user_id"`
	TGDisplay        string `pg:"tg_display,use_zero"`
	Generation       int32
	Status           string
	BlockedAt        *time.Time
	ReplyKBRemovedAt *time.Time `pg:"reply_kb_removed_at"`
	CreatedAt        time.Time  `pg:"default:now()"`
	RevokedAt        *time.Time
}

// TelegramLinkToken is a one-time link from the site; only the secret's
// SHA-256 is kept.
type TelegramLinkToken struct {
	tableName    struct{} `pg:"telegram_link_token"`
	ID           int64
	UserID       int64
	BotRef       int64
	SecretSHA256 []byte `pg:"secret_sha256"`
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
	ConsumedByTG *int64 `pg:"consumed_by_tg"`
	BindingID    *int64
	CreatedAt    time.Time `pg:"default:now()"`
}

// TelegramInboxUpdate is one accepted update waiting to be, or already,
// handled.
type TelegramInboxUpdate struct {
	tableName      struct{} `pg:"telegram_update_inbox"`
	BotRef         int64    `pg:",pk"`
	UpdateID       int64    `pg:",pk"`
	Kind           string
	Payload        *string `pg:"type:jsonb"`
	State          string
	LeaseOwner     *uuid.UUID `pg:"type:uuid"`
	LeaseExpiresAt *time.Time
	Fence          int64
	ReplyStartedAt *time.Time
	Attempts       int16
	ReceivedAt     time.Time `pg:"default:now()"`
	FinishedAt     *time.Time
}

// States of telegram_delivery.state.
const (
	DeliveryQueued    = "queued"
	DeliveryPreparing = "preparing"
	DeliverySending   = "sending"
	DeliverySent      = "sent"
	DeliveryFailed    = "failed"
	DeliveryUncertain = "uncertain"
)

// Outcomes of a finished delivery.
const (
	OutcomeSent             = "sent"
	OutcomeNotFound         = "not_found"
	OutcomeUnavailable      = "unavailable"
	OutcomeTooLarge         = "too_large"
	OutcomeConversionFailed = "conversion_failed"
	OutcomeSendRejected     = "send_rejected"
	OutcomeBotBlocked       = "bot_blocked"
	OutcomeTokenRejected    = "token_rejected"
	OutcomeUncertain        = "uncertain"
	// OutcomeLibraryFailed: the library could not read or check what it
	// needed to send the file, attempt after attempt.
	OutcomeLibraryFailed = "library_failed"
	// OutcomeOwnerChanged: the bot was given to another owner in Telegram
	// before the file went out; nothing was sent.
	OutcomeOwnerChanged = "owner_changed"
)

// Origins of a delivery.
const (
	DeliveryFromBot  = "bot"
	DeliveryFromSite = "site"
)

// TelegramDelivery is one request to send a book file into a bound chat.
type TelegramDelivery struct {
	tableName       struct{} `pg:"telegram_delivery"`
	ID              int64
	UserID          int64
	BindingID       int64
	BotRef          int64
	ChatID          int64
	BookID          int64
	Format          string
	Origin          string
	TapKey          *string
	State           string
	Outcome         *string
	Attempt         int16
	NextAttemptAt   time.Time  `pg:"default:now()"`
	LeaseOwner      *uuid.UUID `pg:"type:uuid"`
	LeaseExpiresAt  *time.Time
	Fence           int64
	OriginMessageID *int64
	OriginRevision  *int32
	StatusMessageID *int64
	FileBytes       *int64
	TGMessageID     *int64    `pg:"tg_message_id"`
	CreatedAt       time.Time `pg:"default:now()"`
	SendStartedAt   *time.Time
	FinishedAt      *time.Time
}

// FromPanel reports a job pressed on a v2 panel: those carry the panel's
// revision, 1 or more; the legacy card's is 0, the site's none.
func (d *TelegramDelivery) FromPanel() bool {
	return d.OriginRevision != nil && *d.OriginRevision >= 1
}

// The routers a bot can be served by.
const (
	TelegramRouterLegacy      = "legacy"
	TelegramRouterV2          = "v2"
	TelegramRouterMaintenance = "maintenance"
)

// How a v2 panel is rendered; fixed when it is sent.
const (
	TelegramPanelPlain = "plain"
	TelegramPanelRich  = "rich"
)

// TelegramView is an immutable query scope of the v2 bot: what a list shows.
type TelegramView struct {
	tableName    struct{} `pg:"telegram_view"`
	ID           int64
	UserID       int64
	Kind         int16
	Query        string    `pg:",use_zero"`
	Lang         string    `pg:",use_zero"`
	AuthorID     int64     `pg:",use_zero"`
	SeriesID     int64     `pg:",use_zero"`
	CollectionID int64     `pg:",use_zero"`
	Favorites    bool      `pg:",use_zero"`
	CreatedAt    time.Time `pg:"default:now()"`
	LastUsedAt   time.Time `pg:"default:now()"`
}

// TelegramNode is one screen of one view, with the screen it was opened from.
type TelegramNode struct {
	tableName  struct{} `pg:"telegram_node"`
	ID         int64
	UserID     int64
	ViewID     int64 `pg:",use_zero"`
	Screen     int16
	EntityID   int64     `pg:",use_zero"`
	Page       int32     `pg:",use_zero"`
	ParentID   int64     `pg:",use_zero"`
	CreatedAt  time.Time `pg:"default:now()"`
	LastUsedAt time.Time `pg:"default:now()"`
}

// TelegramPrompt is a question the v2 bot asked with force_reply, waiting for
// the reader's reply.
type TelegramPrompt struct {
	tableName       struct{} `pg:"telegram_prompt"`
	BotRef          int64    `pg:",pk"`
	ChatID          int64    `pg:",pk"`
	PromptMessageID int64    `pg:",pk"`
	UserID          int64
	Kind            int16
	SourceMessageID int64
	SourceNodeID    int64
	SourceRevision  int32
	State           string
	ExpiresAt       time.Time
	CreatedAt       time.Time `pg:"default:now()"`
}

// TelegramPanel is a bot message that shows nodes and is edited in place.
type TelegramPanel struct {
	tableName    struct{} `pg:"telegram_panel"`
	BotRef       int64    `pg:",pk"`
	ChatID       int64    `pg:",pk"`
	MessageID    int64    `pg:",pk"`
	UserID       int64
	BindingID    int64
	NodeID       int64 `pg:",use_zero"`
	Revision     int32
	Mode         string
	IsHome       bool `pg:",use_zero"`
	PinnedAt     *time.Time
	PinCheckedAt *time.Time
	UpdatedAt    time.Time `pg:"default:now()"`
}

// TelegramPanelRepaint is a card's repaint owed after its job ended (migration 48).
type TelegramPanelRepaint struct {
	tableName struct{} `pg:"telegram_panel_repaint"`
	ID        int64
	BotRef    int64
	ChatID    int64
	MessageID int64
	BindingID int64
	BookID    int64
	// JobID is the delivery job whose end owes the repaint (migration 50);
	// 0 for repaints recorded before it.
	JobID     int64
	CreatedAt time.Time `pg:"default:now()"`
}

// The states of a telegram_pending_connection.
const (
	PendingConnectionWaiting  = "waiting"
	PendingConnectionFound    = "found"
	PendingConnectionCanceled = "canceled"
	PendingConnectionLinked   = "linked"
)

// TelegramPendingConnection is a reader's «Создать личного бота» waiting for
// Telegram to say the bot was made (migration 51).
type TelegramPendingConnection struct {
	tableName      struct{} `pg:"telegram_pending_connection"`
	ID             int64
	UserID         int64
	DiscoveryLabel string
	State          string
	BotRef         *int64
	ExpiresAt      time.Time
	CreatedAt      time.Time `pg:"default:now()"`
	UpdatedAt      time.Time `pg:"default:now()"`
}

// TelegramManagedBot is what the manager confirmed about a bot it manages.
type TelegramManagedBot struct {
	tableName        struct{} `pg:"telegram_managed_bot"`
	BotRef           int64    `pg:",pk"`
	AccessRestricted bool     `pg:",use_zero"`
	LastUpdateID     int64    `pg:",use_zero"`
	ReadyAt          *time.Time
	CheckedAt        time.Time `pg:"default:now()"`
}

// TelegramManagedEvent is one managed_bot update the manager got.
type TelegramManagedEvent struct {
	tableName      struct{}  `pg:"telegram_managed_event"`
	UpdateID       int64     `pg:",pk"`
	BotTGID        int64     `pg:"bot_tg_id"`
	CreatorTGID    int64     `pg:"creator_tg_id"`
	BotUsername    string    `pg:",use_zero"`
	ReceivedAt     time.Time `pg:"default:now()"`
	ClaimedBy      *uuid.UUID
	ClaimExpiresAt *time.Time
	ProcessedAt    *time.Time
}
