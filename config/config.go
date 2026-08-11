package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"

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
	Redis              RedisConfig    `mapstructure:"redis" yaml:"redis"`
	Sessions           SessionsConfig `mapstructure:"sessions" yaml:"sessions"`
	App                AppConfig      `mapstructure:"app" yaml:"app"`
	Scanning           ScanningConfig `mapstructure:"scanning" yaml:"scanning"`
	Email              EmailConfig    `mapstructure:"email" yaml:"email"`

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
	SkipDuplicates             bool   `mapstructure:"skip_duplicates" yaml:"skip_duplicates"`
	EnableLanguageDetection    bool   `mapstructure:"enable_language_detection" yaml:"enable_language_detection"`
	EnableOpenAILangDetection  bool   `mapstructure:"enable_openai_lang_detection" yaml:"enable_openai_lang_detection"`
	OpenAILangDetectionTimeout string `mapstructure:"openai_lang_detection_timeout" yaml:"openai_lang_detection_timeout"`
	MaxConcurrentFiles         int    `mapstructure:"max_concurrent_files" yaml:"max_concurrent_files"`
	BatchSize                  int    `mapstructure:"batch_size" yaml:"batch_size"`
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

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			logging.Warn("Config file not found, using defaults and environment variables")
		} else {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Unmarshal config into struct
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	logging.Infof("Configuration loaded successfully from: %s", viper.ConfigFileUsed())
	return cfg, nil
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

// previewRedisDB is the default database number for the preview cache. DBs
// 0 and 1 are used by sessions (main and token), DB 2 by the rate limiter;
// 3 is free.
const previewRedisDB = 3

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

	// Redis defaults
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.db", 0)

	// Preview Redis defaults — separate keys from the main Redis so preview
	// can be moved to its own instance (or its own DB) without touching
	// sessions, rate limiter or other subsystems.
	viper.SetDefault("preview.redis.host", "localhost")
	viper.SetDefault("preview.redis.port", viper.GetInt("redis.port"))
	viper.SetDefault("preview.redis.db", previewRedisDB)
	viper.SetDefault("preview.redis.password", "")
	viper.SetDefault("preview.cache_ttl", "24h")

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
	viper.SetDefault("scanning.enable_openai_lang_detection", false)
	viper.SetDefault("scanning.openai_lang_detection_timeout", "5s")
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
