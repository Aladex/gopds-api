package config

import (
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"gopds-api/internal/safeio"
	"gopds-api/logging"

	"github.com/spf13/viper"
)

// Config represents the main configuration structure
type Config struct {
	Server             ServerConfig   `mapstructure:"server" yaml:"server"`
	ProjectURL         string         `mapstructure:"project_url" yaml:"project_url"`
	Domain             string         `mapstructure:"project_domain" yaml:"project_domain"`
	TelegramWebhookURL string         `mapstructure:"telegram_webhook_url" yaml:"telegram_webhook_url"`
	SecretKey          string         `mapstructure:"secret_key" yaml:"secret_key"`
	Postgres           PostgresConfig `mapstructure:"postgres" yaml:"postgres"`
	Database           DatabaseConfig `mapstructure:"database" yaml:"database"`
	Redis              RedisConfig    `mapstructure:"redis" yaml:"redis"`
	Sessions           SessionsConfig `mapstructure:"sessions" yaml:"sessions"`
	App                AppConfig      `mapstructure:"app" yaml:"app"`
	Scanning           ScanningConfig `mapstructure:"scanning" yaml:"scanning"`
	Email              EmailConfig    `mapstructure:"email" yaml:"email"`
	Preview            PreviewConfig  `mapstructure:"preview" yaml:"preview"`
	LLM                LLMConfig      `mapstructure:"llm" yaml:"llm"`

	AuthorMetadata AuthorMetadataConfig `mapstructure:"author_metadata" yaml:"author_metadata"`
	Authors        AuthorsConfig        `mapstructure:"authors" yaml:"authors"`

	// Donate is deliberately a list rather than a fixed set of fields: which
	// ways of giving are offered is the operator's business, not this
	// application's. An empty list means the interface offers none, which is
	// what anyone running their own copy should get until they say otherwise.
	Donate []DonateMethod `mapstructure:"donate" yaml:"donate"`
}

// DonateMethod is one way of supporting the service.
type DonateMethod struct {
	// ID is a stable handle for the method, independent of its label.
	ID string `mapstructure:"id" yaml:"id" json:"id"`
	// Label is shown as-is: these are proper nouns, not translated strings.
	Label string `mapstructure:"label" yaml:"label" json:"label"`
	// Kind tells the interface how to present Value: "address" for something
	// to be copied, "card" for a payment card, "link" for somewhere to go.
	Kind string `mapstructure:"kind" yaml:"kind" json:"kind"`
	// Value is the address, the card number or the URL, by Kind.
	Value string `mapstructure:"value" yaml:"value" json:"value"`
	// Link is an optional way to pay that accompanies a Value worth showing,
	// such as a bank's transfer page beside a card number.
	Link string `mapstructure:"link" yaml:"link" json:"link,omitempty"`
	// QR asks for a scannable code. A wallet address or a URL is worth
	// scanning; a card number is not, so this is opt-in per method.
	QR bool `mapstructure:"qr" yaml:"qr" json:"qr"`
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	Host           string `mapstructure:"host" yaml:"host"`
	Port           int    `mapstructure:"port" yaml:"port"`
	ReadTimeout    int    `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout   int    `mapstructure:"write_timeout" yaml:"write_timeout"`
	MaxHeaderBytes int    `mapstructure:"max_header_bytes" yaml:"max_header_bytes"`
}

// PostgresConfig holds database configuration
type PostgresConfig struct {
	DBUser   string `mapstructure:"dbuser" yaml:"dbuser"`
	DBPass   string `mapstructure:"dbpass" yaml:"dbpass"`
	DBName   string `mapstructure:"dbname" yaml:"dbname"`
	DBHost   string `mapstructure:"dbhost" yaml:"dbhost"`
	MaxConns int    `mapstructure:"max_conns" yaml:"max_conns"`
}

// DatabaseConfig holds what the server does with the schema at start.
type DatabaseConfig struct {
	// AutoMigrate applies pending migrations before anything else touches
	// the database. Off, a start only reports what is pending.
	AutoMigrate bool `mapstructure:"auto_migrate" yaml:"auto_migrate"`
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Host     string `mapstructure:"host" yaml:"host"`
	Port     int    `mapstructure:"port" yaml:"port"`
	Password string `mapstructure:"password" yaml:"password"`
	DB       int    `mapstructure:"db" yaml:"db"`
}

// SessionsConfig holds session configuration
type SessionsConfig struct {
	Key     string `mapstructure:"key" yaml:"key"`
	Refresh string `mapstructure:"refresh" yaml:"refresh"`
}

// AppConfig holds application-specific configuration
type AppConfig struct {
	DevelMode         bool     `mapstructure:"devel_mode" yaml:"devel_mode"`
	CDN               string   `mapstructure:"cdn" yaml:"cdn"`
	FilesPath         string   `mapstructure:"files_path" yaml:"files_path"`
	UsersPath         string   `mapstructure:"users_path" yaml:"users_path"`
	BookCDNKey        string   `mapstructure:"book_cdn_key" yaml:"book_cdn_key"`
	PostersPath       string   `mapstructure:"posters_path" yaml:"posters_path"`
	FileBookCDN       string   `mapstructure:"file_book_cdn" yaml:"file_book_cdn"`
	MobiConversionDir string   `mapstructure:"mobi_conversion_dir" yaml:"mobi_conversion_dir"`
	AllowedOrigins    []string `mapstructure:"allowed_origins" yaml:"allowed_origins"`
}

// ScanningConfig holds scanning-specific configuration
type ScanningConfig struct {
	SkipDuplicates          bool   `mapstructure:"skip_duplicates" yaml:"skip_duplicates"`
	EnableLanguageDetection bool   `mapstructure:"enable_language_detection" yaml:"enable_language_detection"`
	EnableLLMLangDetection  bool   `mapstructure:"enable_llm_lang_detection" yaml:"enable_llm_lang_detection"`
	LLMLangDetectionTimeout string `mapstructure:"llm_lang_detection_timeout" yaml:"llm_lang_detection_timeout"`
	MaxConcurrentFiles      int    `mapstructure:"max_concurrent_files" yaml:"max_concurrent_files"`
	BatchSize               int    `mapstructure:"batch_size" yaml:"batch_size"`
}

// PreviewConfig holds the book-preview pipeline settings. Every key carries
// a default in setDefaults, so the section is usually absent from config
// files; it exists so the gates and budgets can be re-tuned after a catalog
// re-measurement without a code change.
type PreviewConfig struct {
	// Redis is the optional separate Redis for the preview cache. Any field
	// left unset falls back to the main Redis connection (see
	// GetPreviewRedisAddress): the preview runs on the shared instance — in
	// its own database — until an operator moves it out.
	Redis PreviewRedisConfig `mapstructure:"redis" yaml:"redis"`

	// CacheTTL is the lifetime of one cached preview (manifest, chunks and
	// prepared images share it).
	CacheTTL time.Duration `mapstructure:"cache_ttl" yaml:"cache_ttl"`
	// BuildTimeout bounds one cold build. The build context is detached from
	// the reader's request, so without the bound a hung loader or a hung
	// Redis would pin a build slot and its singleflight key forever.
	BuildTimeout time.Duration `mapstructure:"build_timeout" yaml:"build_timeout"`
	// MaxConcurrentBuilds is the ceiling on simultaneous cold builds.
	MaxConcurrentBuilds int `mapstructure:"max_concurrent_builds" yaml:"max_concurrent_builds"`

	// The input gates, re-derived from the full-catalog census (537 628
	// books). The phase-0 sample of 488 systematically under-reported: its
	// maximum was a quantile estimate, not the true maximum.
	MaxFB2Bytes int `mapstructure:"max_fb2_bytes" yaml:"max_fb2_bytes"`
	MaxBinaries int `mapstructure:"max_binaries" yaml:"max_binaries"`
	// MaxNodes caps the element nodes one document may carry. Enforced
	// during the parse — the parse stops at the exceeding element rather
	// than building the whole tree and comparing afterwards.
	MaxNodes int `mapstructure:"max_nodes" yaml:"max_nodes"`
	// MaxPreparedImageBytes caps the total weight of prepared preview
	// images — the sum of len(Payload) across imageSet.Images(), measured
	// AFTER preparation, not on the source binaries. Transcoding changes
	// the size, and the prepared bytes are what live in memory and in the
	// cache. The old gate on raw binary weight was removed: base64 expands
	// 3:4, so decoded binaries never exceed 3/4 of the FB2 file — under a
	// 64 MiB file cap any weight gate at or above 48 MiB is unreachable,
	// and one below discriminates against illustrated books at equal file
	// size without added safety.
	MaxPreparedImageBytes int `mapstructure:"max_prepared_image_bytes" yaml:"max_prepared_image_bytes"`
}

// AuthorMetadataConfig configures the embedded author metadata workers: the
// extraction stream (plan phase 8), the local normalization stream (phase
// 10) and the LLM stream of design K. The LLM stream has no key or endpoint
// of its own: it uses the shared llm section.
// Every limit has a default; zero or negative values are refused at load,
// naming the key, so no setting can turn into an unbounded claim, a lease
// that never expires or endless retries.
type AuthorMetadataConfig struct {
	// Enabled starts the workers with the server. Off by default: the
	// pipeline is switched on by the operator.
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
	// MetadataMaxBytes caps how much of one FB2 file the extractor reads
	// looking for its metadata.
	MetadataMaxBytes int64 `mapstructure:"metadata_max_bytes" yaml:"metadata_max_bytes"`
	// PollInterval is how long an idle worker waits before claiming again.
	PollInterval       time.Duration             `mapstructure:"poll_interval" yaml:"poll_interval"`
	Extraction         AuthorMetadataStageConfig `mapstructure:"extraction" yaml:"extraction"`
	LocalNormalization AuthorMetadataStageConfig `mapstructure:"local_normalization" yaml:"local_normalization"`
	// LLM is the author layer's own part of the LLM settings; the endpoint
	// and the key are the shared llm section's.
	LLM AuthorLLMConfig `mapstructure:"llm" yaml:"llm"`
}

// AuthorMetadataStageConfig bounds one leased stream: how many workers run
// it, how many rows one claim takes, how long a claimed row stays the
// worker's without a heartbeat, and how many attempts a row gets.
type AuthorMetadataStageConfig struct {
	Concurrency int           `mapstructure:"concurrency" yaml:"concurrency"`
	ClaimSize   int           `mapstructure:"claim_size" yaml:"claim_size"`
	Lease       time.Duration `mapstructure:"lease" yaml:"lease"`
	MaxAttempts int           `mapstructure:"max_attempts" yaml:"max_attempts"`
}

// AuthorMetadataMaxBytes is the default metadata read limit, 4 MiB. The one
// constant: services.AuthorMetadataMaxBytes, used by the live dual write,
// refers to it.
const AuthorMetadataMaxBytes = 4 << 20

// Config keys of the author metadata settings that both carry a default and
// are named by validation.
const (
	authorMetadataMaxBytesKey     = "author_metadata.metadata_max_bytes"
	authorMetadataPollIntervalKey = "author_metadata.poll_interval"
)

// Defaults of the author metadata streams. Extraction runs one worker by
// default (contract 3.8); the local stream's numbers are the phase-10 worker
// defaults.
const (
	authorMetadataPollInterval     = 5 * time.Second
	authorMetadataExtractionClaim  = 50
	authorMetadataExtractionLease  = 2 * time.Minute
	authorMetadataLocalClaim       = 100
	authorMetadataLocalLease       = time.Minute
	authorMetadataStageMaxAttempts = 5
	authorMetadataStageConcurrency = 1
)

// PreviewRedisConfig is the separate Redis destination for the preview
// cache. Empty host/port/password mean "take the main Redis value" — see
// GetPreviewRedisAddress and GetPreviewRedisPassword. DB is the exception:
// it has its own default, because sharing the instance is the default while
// sharing the keyspace never is.
type PreviewRedisConfig struct {
	Host     string `mapstructure:"host" yaml:"host"`
	Port     int    `mapstructure:"port" yaml:"port"`
	Password string `mapstructure:"password" yaml:"password"`
	DB       int    `mapstructure:"db" yaml:"db"`
}

// LLMDefaultModel is the chat model asked for when nothing names one.
const LLMDefaultModel = "gpt-4o-mini"

// Default values of the llm section.
const (
	llmDefaultBaseURL = "https://api.openai.com/v1"
	llmDefaultTimeout = "30s"
)

// The section's own unprefixed environment names.
const (
	llmEnvBaseURL = "LLM_BASE_URL"
	llmKeyEnvName = "LLM_API_KEY"
	llmEnvModel   = "LLM_MODEL"
)

// LLMConfig is the one LLM provider section every model-backed feature reads:
// search query parsing, genre titles, curated-collection matching and language
// detection all send through it, so an installation points everything at one
// OpenAI-compatible endpoint (plain OpenAI or an in-cluster gateway) with one
// optional key.
type LLMConfig struct {
	// BaseURL is the root of the OpenAI-compatible endpoint; requests go to
	// BaseURL + "/chat/completions". The default is the public OpenAI API.
	// An explicitly empty value switches every LLM feature off.
	BaseURL string `mapstructure:"base_url" yaml:"base_url"`
	// APIKey is sent as the Authorization bearer token. Optional: empty means
	// no header at all, which is a valid configuration behind a keyless
	// gateway that authenticates at the network layer.
	APIKey string `mapstructure:"api_key" yaml:"api_key"`
	// Model is the chat model asked for; the default is LLMDefaultModel.
	Model string `mapstructure:"model" yaml:"model"`
	// Timeout bounds one LLM HTTP request.
	Timeout time.Duration `mapstructure:"timeout" yaml:"timeout"`
}

// isOpenAIPublicAPI reports whether the endpoint's host is api.openai.com.
func (c LLMConfig) isOpenAIPublicAPI() bool {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}

// Configured reports whether the LLM features may talk to a provider. The
// rule: a base URL must be set, and either a key is configured or the endpoint
// is not the public OpenAI API — a keyless gateway in front of the models is a
// valid provider, while keyless requests to api.openai.com are always a
// mistake. Everything else reads as "not configured", and every feature
// degrades to its no-LLM behavior instead of sending anything.
func (c LLMConfig) Configured() bool {
	if c.BaseURL == "" {
		return false
	}
	return c.APIKey != "" || !c.isOpenAIPublicAPI()
}

// loadedLLM is the llm section of the last successful Load. Callers with no
// *Config at hand — the zero-argument llm constructors — read it through LLM.
var loadedLLM LLMConfig

// LLM returns the llm section of the loaded configuration, legacy fallbacks
// included. Before Load it is the zero value: no base URL, so every LLM
// feature reads "not configured".
func LLM() LLMConfig {
	return loadedLLM
}

// EmailConfig holds email configuration
type EmailConfig struct {
	From       string `mapstructure:"from" yaml:"from"`
	User       string `mapstructure:"user" yaml:"user"`
	Password   string `mapstructure:"password" yaml:"password"`
	SMTPServer string `mapstructure:"smtp_server" yaml:"smtp_server"`

	// Language picks which of the two sets below is sent. Both are always
	// carried, so changing it is a restart rather than a rewrite.
	Language string `mapstructure:"language" yaml:"language"`

	// ProductName names this installation in the emails it sends. Anyone
	// running their own copy should not have to send mail signed Booksdump.
	ProductName string `mapstructure:"product_name" yaml:"product_name"`

	Messages MessageConfig `mapstructure:"messages" yaml:"messages"`
}

// MessageConfig holds one set of wordings per language.
//
// Named fields rather than a map keyed by language: a map has no leaves for
// bindEnvKeys to walk, so every string in it would be unreachable from the
// environment — and the environment is where this deployment would rather keep
// its configuration. Two languages is what the interface itself offers.
type MessageConfig struct {
	RU LanguageMessages `mapstructure:"ru" yaml:"ru"`
	EN LanguageMessages `mapstructure:"en" yaml:"en"`
}

// LanguageMessages holds every email this application sends, in one language.
type LanguageMessages struct {
	Registration EmailTemplate `mapstructure:"registration" yaml:"registration"`
	Reset        EmailTemplate `mapstructure:"reset" yaml:"reset"`
}

// EmailTemplate holds email template configuration.
//
// The last three used to be written into the markup, in English, while
// everything above them came from here in whatever language the operator chose
// — which is how a Russian email came to explain itself in English halfway
// through. Every field is optional: what is left empty falls back to the
// wording built into the application for that language.
type EmailTemplate struct {
	Subject string `mapstructure:"subject" yaml:"subject"`
	Title   string `mapstructure:"title" yaml:"title"`
	Message string `mapstructure:"message" yaml:"message"`
	Button  string `mapstructure:"button" yaml:"button"`
	Thanks  string `mapstructure:"thanks" yaml:"thanks"`

	// LinkFallback introduces the copyable address.
	LinkFallback string `mapstructure:"link_fallback" yaml:"link_fallback"`
	// Warning is the caution shown above the signature, where one is warranted.
	Warning string `mapstructure:"warning" yaml:"warning"`
	// Footer says why the email arrived at all.
	Footer string `mapstructure:"footer" yaml:"footer"`
}

// Load initializes and loads the configuration
func Load() (*Config, error) {
	cfg := &Config{}

	// Set default values
	setDefaults()

	// Setup viper
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// Enable environment variables
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.SetEnvPrefix("GOPDS")

	// AutomaticEnv alone is not enough: Unmarshal only visits keys viper already
	// knows about, so any key without a default or a config-file entry is read as
	// empty no matter what the environment says. That covers every secret
	// (secret_key, postgres credentials, session keys, ...), which .env.example
	// documents as configurable. Binding each key explicitly makes them reachable.
	if err := bindEnvKeys(reflect.TypeOf(Config{}), ""); err != nil {
		return nil, fmt.Errorf("error binding environment variables: %w", err)
	}
	if err := bindLLMEnvAliases(); err != nil {
		return nil, fmt.Errorf("error binding environment variables: %w", err)
	}

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			logging.Warn("Config file not found, using defaults and environment variables")
		} else {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Pre-rename keys a config file may still carry: warn, never honor.
	warnObsoleteScanningKeys()

	// Unmarshal config into struct
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// The resolved llm section is what LLM() hands to the zero-argument
	// llm constructors.
	loadedLLM = cfg.LLM

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	logging.Infof("Configuration loaded successfully from: %s", viper.ConfigFileUsed())
	return cfg, nil
}

// The renamed scanning language-detection keys and the obsolete names they
// replace, shared by the defaults and the obsolete-key warnings.
const (
	scanningLLMLangDetectionKey             = "scanning.enable_llm_lang_detection"
	scanningLLMLangDetectionTimeoutKey      = "scanning.llm_lang_detection_timeout"
	scanningObsoleteLangDetectionKey        = "scanning.enable_openai_lang_detection"
	scanningObsoleteLangDetectionTimeoutKey = "scanning.openai_lang_detection_timeout"
)

// obsoleteScanningKeys are the pre-rename scanning keys a config file may
// still carry (the production one lives in Vault). They are ignored — never
// honored as aliases — and each one present earns a single startup warning
// naming its replacement, so a stale file says what to fix instead of
// silently switching the LLM language arbiter off.
var obsoleteScanningKeys = map[string]string{
	scanningObsoleteLangDetectionKey:        scanningLLMLangDetectionKey,
	scanningObsoleteLangDetectionTimeoutKey: scanningLLMLangDetectionTimeoutKey,
}

// warnObsoleteScanningKeys logs one warning per obsolete scanning key present
// in the loaded configuration. Key names only: no configured value reaches
// the log.
func warnObsoleteScanningKeys() {
	for obsolete, replacement := range obsoleteScanningKeys {
		if viper.IsSet(obsolete) {
			logging.Warnf("config: %s is obsolete and ignored; set %s instead", obsolete, replacement)
		}
	}
}

// bindEnvKeys walks a configuration struct and registers every leaf key with
// viper, so environment variables reach keys that have neither a default nor an
// entry in the config file. The prefix carries the dotted path of the enclosing
// struct, matching the mapstructure tags used for unmarshaling.
func bindEnvKeys(t reflect.Type, prefix string) error {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		tag := field.Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}

		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}

		if field.Type.Kind() == reflect.Struct {
			if err := bindEnvKeys(field.Type, key); err != nil {
				return err
			}
			continue
		}

		// A list of structures has no sensible single environment variable to
		// come from; binding one would only give viper a string where it
		// expects a list. Such settings live in the config file.
		if field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct {
			continue
		}

		if err := viper.BindEnv(key); err != nil {
			return err
		}
	}
	return nil
}

// bindLLMEnvAliases gives the llm keys their own unprefixed environment names:
// LLM_BASE_URL, LLM_API_KEY and LLM_MODEL are the documented variables for the
// section. bindEnvKeys above already bound the GOPDS_-prefixed twins, and
// viper checks the prefixed name first, so the effective precedence is
// GOPDS_LLM_BASE_URL, then LLM_BASE_URL, then the config file, then the
// built-in default.
func bindLLMEnvAliases() error {
	for key, alias := range map[string]string{
		"llm.base_url": llmEnvBaseURL,
		"llm.api_key":  llmKeyEnvName,
		"llm.model":    llmEnvModel,
	} {
		if err := viper.BindEnv(key, alias); err != nil {
			return err
		}
	}
	return nil
}

// previewRedisDB is the default database number for the preview cache.
const previewRedisDB = 3

// previewMaxConcurrentBuilds is the default ceiling on simultaneous cold
// builds — the same value the service falls back to when constructed with a
// zero.
const previewMaxConcurrentBuilds = 2

// Input gate defaults, re-derived from the full-catalog census (537 628
// books). The previous phase-0 sample (488 books) systematically
// under-reported: its maximum was a quantile estimate, not the true maximum.
// 1500 binaries and 100 000 nodes are ceilings on the number of operations
// and objects, NOT headroom above an observed maximum — the catalog tail is
// shaped by file size, not by binary count, so a small book can in principle
// carry more small binaries than any book in the measured set.
// Exported because the service keeps its own fallback for a zero-valued
// limits struct: a second copy of the number would let the two drift apart
// silently, and only one of them would be pinned by a test.
const (
	PreviewMaxFB2Bytes           = 64 << 20 // 64 MiB
	PreviewMaxBinaries           = 1500
	PreviewMaxNodes              = 100_000
	PreviewMaxPreparedImageBytes = 48 << 20 // 48 MiB
)

// Where the book list's author line comes from.
const (
	// AuthorsDisplayLegacy keeps the line on the catalog's legacy authors:
	// the list carries no author line of its own.
	AuthorsDisplayLegacy = "legacy"
	// AuthorsDisplayLayer reads the line from the author layer.
	AuthorsDisplayLayer = "layer"
)

// AuthorsConfig switches what readers are shown of a book's authors.
type AuthorsConfig struct {
	// DisplaySource is AuthorsDisplayLegacy or AuthorsDisplayLayer.
	DisplaySource string `mapstructure:"display_source" yaml:"display_source"`
}

// DisplayFromLayer reports whether the book list shows the author line from
// the author layer.
func (c AuthorsConfig) DisplayFromLayer() bool {
	return c.DisplaySource == AuthorsDisplayLayer
}

func (c AuthorsConfig) validate() error {
	switch c.DisplaySource {
	case AuthorsDisplayLegacy, AuthorsDisplayLayer:
		return nil
	}
	return fmt.Errorf("authors.display_source must be %q or %q, got %q",
		AuthorsDisplayLegacy, AuthorsDisplayLayer, c.DisplaySource)
}

// setDefaults sets default configuration values
func setDefaults() {
	// Server defaults
	viper.SetDefault("server.host", "127.0.0.1")
	viper.SetDefault("server.port", 8085)
	viper.SetDefault("server.read_timeout", 10)
	viper.SetDefault("server.write_timeout", 10)
	viper.SetDefault("server.max_header_bytes", 1048576) // 1 << 20

	// Database defaults
	viper.SetDefault("postgres.dbhost", "localhost:5432")
	viper.SetDefault("postgres.max_conns", 10)

	// A server start applies pending migrations unless told not to.
	viper.SetDefault("database.auto_migrate", true)

	// Redis defaults
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.db", 0)

	// Preview Redis defaults — separate keys from the main Redis so preview
	// can be moved to its own instance (or its own DB) without touching
	// sessions, rate limiter or other subsystems. Host and port default to
	// EMPTY on purpose: empty is the fallback marker — resolution
	// (GetPreviewRedisAddress) then points at the main Redis, so a config
	// that says nothing about preview.redis uses the shared instance and
	// never a hardcoded localhost.
	viper.SetDefault("preview.redis.host", "")
	viper.SetDefault("preview.redis.port", 0)
	viper.SetDefault("preview.redis.db", previewRedisDB)
	viper.SetDefault("preview.redis.password", "")
	viper.SetDefault("preview.cache_ttl", "24h")

	// Cold-build timeout: one preview build may not take longer than this.
	// The build context is detached from the reader's request, so without
	// the bound a hung loader or a hung Redis would pin a build slot and
	// its singleflight key forever.
	viper.SetDefault("preview.build_timeout", "2m")

	// Ceiling on simultaneous cold builds.
	viper.SetDefault("preview.max_concurrent_builds", previewMaxConcurrentBuilds)

	// Preview input gates — derived from the phase-0 catalog measurement.
	// Each can be overridden in config without touching code.
	viper.SetDefault("preview.max_fb2_bytes", PreviewMaxFB2Bytes)
	viper.SetDefault("preview.max_binaries", PreviewMaxBinaries)
	viper.SetDefault("preview.max_nodes", PreviewMaxNodes)
	viper.SetDefault("preview.max_prepared_image_bytes", PreviewMaxPreparedImageBytes)

	// Author metadata workers: off until the operator enables them, with
	// bounded defaults for every stream.
	viper.SetDefault("author_metadata.enabled", false)
	viper.SetDefault(authorMetadataMaxBytesKey, AuthorMetadataMaxBytes)
	viper.SetDefault(authorMetadataPollIntervalKey, authorMetadataPollInterval)
	viper.SetDefault("author_metadata.extraction.concurrency", authorMetadataStageConcurrency)
	viper.SetDefault("author_metadata.extraction.claim_size", authorMetadataExtractionClaim)
	viper.SetDefault("author_metadata.extraction.lease", authorMetadataExtractionLease)
	viper.SetDefault("author_metadata.extraction.max_attempts", authorMetadataStageMaxAttempts)
	viper.SetDefault("author_metadata.local_normalization.concurrency", authorMetadataStageConcurrency)
	viper.SetDefault("author_metadata.local_normalization.claim_size", authorMetadataLocalClaim)
	viper.SetDefault("author_metadata.local_normalization.lease", authorMetadataLocalLease)
	viper.SetDefault("author_metadata.local_normalization.max_attempts", authorMetadataStageMaxAttempts)
	setAuthorLLMDefaults()

	// The book list's author line stays on the legacy catalog until the
	// operator switches it.
	viper.SetDefault("authors.display_source", AuthorsDisplayLegacy)

	// The LLM provider section — one endpoint and one key for every
	// model-backed feature.
	viper.SetDefault("llm.base_url", llmDefaultBaseURL)
	viper.SetDefault("llm.model", LLMDefaultModel)
	viper.SetDefault("llm.timeout", llmDefaultTimeout)

	// App defaults
	viper.SetDefault("app.devel_mode", false)
	viper.SetDefault("app.files_path", "./files/")
	viper.SetDefault("app.users_path", "./users/")
	viper.SetDefault("app.posters_path", "./posters/")
	viper.SetDefault("app.mobi_conversion_dir", "./mobi/")
	viper.SetDefault("app.allowed_origins", []string{})

	// Scanning defaults
	viper.SetDefault("scanning.skip_duplicates", true)
	viper.SetDefault("scanning.enable_language_detection", true)
	viper.SetDefault(scanningLLMLangDetectionKey, false)
	viper.SetDefault(scanningLLMLangDetectionTimeoutKey, "5s")
	viper.SetDefault("scanning.max_concurrent_files", 1)
	viper.SetDefault("scanning.batch_size", 50)
}

// validateConfig validates the loaded configuration
func validateConfig(cfg *Config) error {
	// Validate required fields
	if cfg.SecretKey == "" {
		return fmt.Errorf("secret_key is required")
	}

	if cfg.Postgres.DBUser == "" || cfg.Postgres.DBName == "" {
		return fmt.Errorf("postgres configuration is incomplete")
	}

	if cfg.Sessions.Key == "" || cfg.Sessions.Refresh == "" {
		return fmt.Errorf("session keys are required")
	}

	// Validate port range
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", cfg.Server.Port)
	}

	if err := cfg.AuthorMetadata.validate(); err != nil {
		return err
	}
	if err := cfg.Authors.validate(); err != nil {
		return err
	}

	// An LLM request must be bounded; a non-positive timeout is refused even
	// with no provider configured, so the bad value surfaces when it is
	// written, not on the day the LLM is switched on.
	if cfg.LLM.Timeout <= 0 {
		return fmt.Errorf("llm.timeout must be positive, got %s", cfg.LLM.Timeout)
	}

	// Validate paths exist or can be created
	paths := []string{
		cfg.App.FilesPath,
		cfg.App.UsersPath,
		cfg.App.PostersPath,
		cfg.App.MobiConversionDir,
	}

	for _, path := range paths {
		if err := ensureDirectoryExists(path); err != nil {
			return fmt.Errorf("failed to ensure directory %s: %w", path, err)
		}
	}

	return nil
}

// configLimit is one setting that must be positive, by its config key.
type configLimit struct {
	key   string
	value int64
}

// validate refuses any non-positive limit, enabled or not: a bad value is
// reported when it is written, not on the day the workers are switched on.
func (c *AuthorMetadataConfig) validate() error {
	limits := []configLimit{
		{authorMetadataMaxBytesKey, c.MetadataMaxBytes},
		{authorMetadataPollIntervalKey, int64(c.PollInterval)},
	}
	limits = append(limits, c.Extraction.limits("author_metadata.extraction.")...)
	limits = append(limits, c.LocalNormalization.limits("author_metadata.local_normalization.")...)
	for _, l := range limits {
		if l.value <= 0 {
			return limitError(l.key, l.value)
		}
	}
	return c.LLM.validate()
}

// limitError reports a non-positive limit by its key.
func limitError(key string, value int64) error {
	return fmt.Errorf("%s must be positive, got %d", key, value)
}

// rangeError reports a setting outside its allowed range by its key.
func rangeError(key, allowed string) error {
	return fmt.Errorf("%s must be %s", key, allowed)
}

func (s *AuthorMetadataStageConfig) limits(prefix string) []configLimit {
	return []configLimit{
		{prefix + "concurrency", int64(s.Concurrency)},
		{prefix + "claim_size", int64(s.ClaimSize)},
		{prefix + "lease", int64(s.Lease)},
		{prefix + "max_attempts", int64(s.MaxAttempts)},
	}
}

// ensureDirectoryExists creates directory if it doesn't exist
func ensureDirectoryExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, safeio.DirMode); err != nil {
			return err
		}
		logging.Infof("Created directory: %s", path)
	}
	return nil
}

// GetServerAddress returns the full server address
func (c *Config) GetServerAddress() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// GetPostgresConnectionString returns PostgreSQL connection string
func (c *Config) GetPostgresConnectionString() string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		c.Postgres.DBUser,
		c.Postgres.DBPass,
		c.Postgres.DBHost,
		c.Postgres.DBName,
	)
}

// GetRedisAddress returns Redis address
func (c *Config) GetRedisAddress() string {
	return fmt.Sprintf("%s:%d", c.Redis.Host, c.Redis.Port)
}

// GetPreviewRedisAddress resolves where the preview cache lives. A preview
// host or port left unset inherits the main Redis connection — the fallback
// exists so the preview works out of the box on the shared instance while
// remaining movable to its own instance through configuration alone.
func (c *Config) GetPreviewRedisAddress() string {
	host := c.Preview.Redis.Host
	if host == "" {
		host = c.Redis.Host
	}
	port := c.Preview.Redis.Port
	if port == 0 {
		port = c.Redis.Port
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// GetPreviewRedisPassword mirrors GetPreviewRedisAddress for the password:
// an unset preview password means "the main Redis password".
func (c *Config) GetPreviewRedisPassword() string {
	if c.Preview.Redis.Password != "" {
		return c.Preview.Redis.Password
	}
	return c.Redis.Password
}

// IsDevelopment returns true if running in development mode
func (c *Config) IsDevelopment() bool {
	return c.App.DevelMode
}

// GetServerBaseURL returns base URL for webhook endpoints
func (c *Config) GetServerBaseURL() string {
	var baseURL string

	if c.Domain != "" {
		baseURL = c.Domain
		logging.Infof("Using Domain for BaseURL: %s", baseURL)
	} else if c.ProjectURL != "" {
		baseURL = c.ProjectURL
		logging.Infof("Using ProjectURL for BaseURL: %s", baseURL)
	} else {
		// Fallback to local address
		baseURL = fmt.Sprintf("http://%s:%d", c.Server.Host, c.Server.Port)
		logging.Infof("Using fallback local address for BaseURL: %s", baseURL)
	}

	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "https://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	logging.Infof("Final BaseURL for webhooks: %s", baseURL)
	return baseURL
}

// GetTelegramWebhookBaseURL returns the base URL Telegram should call to
// deliver webhooks. If telegram_webhook_url is set, it overrides the
// project domain — useful when the main host is unreachable from Telegram
// (e.g. hosted in a network blocked from Telegram's servers) and a CDN
// proxy on a separate hostname is needed.
func (c *Config) GetTelegramWebhookBaseURL() string {
	if c.TelegramWebhookURL == "" {
		return c.GetServerBaseURL()
	}

	u := c.TelegramWebhookURL
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	u = strings.TrimRight(u, "/")
	logging.Infof("Using TelegramWebhookURL for webhooks: %s", u)
	return u
}
