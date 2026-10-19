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
