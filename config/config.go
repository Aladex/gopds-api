package config

import (
	"fmt"
	"os"
	"strings"

	"gopds-api/logging"

	"github.com/spf13/viper"
)

// Config represents the main configuration structure
type Config struct {
	Server     ServerConfig   `mapstructure:"server" yaml:"server"`
	ProjectURL string         `mapstructure:"project_url" yaml:"project_url"`
	Domain     string         `mapstructure:"project_domain" yaml:"project_domain"`
	SecretKey  string         `mapstructure:"secret_key" yaml:"secret_key"`
	Postgres   PostgresConfig `mapstructure:"postgres" yaml:"postgres"`
	Redis      RedisConfig    `mapstructure:"redis" yaml:"redis"`
	Sessions   SessionsConfig `mapstructure:"sessions" yaml:"sessions"`
	App        AppConfig      `mapstructure:"app" yaml:"app"`
	Email      EmailConfig    `mapstructure:"email" yaml:"email"`
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
	DevelMode         bool   `mapstructure:"devel_mode" yaml:"devel_mode"`
	CDN               string `mapstructure:"cdn" yaml:"cdn"`
	FilesPath         string `mapstructure:"files_path" yaml:"files_path"`
	UsersPath         string `mapstructure:"users_path" yaml:"users_path"`
	BookCDNKey        string `mapstructure:"book_cdn_key" yaml:"book_cdn_key"`
	PostersPath       string `mapstructure:"posters_path" yaml:"posters_path"`
	FileBookCDN       string `mapstructure:"file_book_cdn" yaml:"file_book_cdn"`
	MobiConversionDir string `mapstructure:"mobi_conversion_dir" yaml:"mobi_conversion_dir"`
}

// EmailConfig holds email configuration
type EmailConfig struct {
	From       string        `mapstructure:"from" yaml:"from"`
	User       string        `mapstructure:"user" yaml:"user"`
	Password   string        `mapstructure:"password" yaml:"password"`
	SMTPServer string        `mapstructure:"smtp_server" yaml:"smtp_server"`
	Messages   MessageConfig `mapstructure:"messages" yaml:"messages"`
}

// MessageConfig holds email message templates configuration
type MessageConfig struct {
	Registration EmailTemplate `mapstructure:"registration" yaml:"registration"`
	Reset        EmailTemplate `mapstructure:"reset" yaml:"reset"`
}

// EmailTemplate holds email template configuration
type EmailTemplate struct {
	Subject string `mapstructure:"subject" yaml:"subject"`
	Title   string `mapstructure:"title" yaml:"title"`
	Message string `mapstructure:"message" yaml:"message"`
	Button  string `mapstructure:"button" yaml:"button"`
	Thanks  string `mapstructure:"thanks" yaml:"thanks"`
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

	// App defaults
	viper.SetDefault("app.devel_mode", false)
	viper.SetDefault("app.files_path", "./files/")
	viper.SetDefault("app.users_path", "./users/")
	viper.SetDefault("app.posters_path", "./posters/")
	viper.SetDefault("app.mobi_conversion_dir", "./mobi/")
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
		if err := os.MkdirAll(path, 0755); err != nil {
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
	if c.Domain != "" {
		return c.Domain
	}
	if c.ProjectURL != "" {
		return c.ProjectURL
	}
	// Fallback to local address
	return fmt.Sprintf("http://%s:%d", c.Server.Host, c.Server.Port)
}
