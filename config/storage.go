package config

import (
	"errors"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	// Two attempts of this and a backoff stay under the example server's write
	// timeout of ten seconds, so a dead store is an answer, not a dropped
	// connection.
	storageRequestTimeout = 4 * time.Second
	storageRetries        = 1
	// A whole cold book, directory included, stays under the same limit.
	storageBookReadTimeout  = 9 * time.Second
	storageMaxActive        = 16
	storageCacheDir         = "./cache/storage"
	storageThumbsPrefix     = "thumbs/"
	storageBookCacheGB      = 30
	storageDirectoryCacheMB = 64
	storageTempBudgetGB     = 20

	storageAccessKeyEnv    = "STORAGE_ACCESS_KEY_ID"
	storageAWSAccessKeyEnv = "AWS_ACCESS_KEY_ID"
	// #nosec G101 -- the name of an environment variable, not its value.
	storageSecretKeyEnv = "STORAGE_SECRET_ACCESS_KEY"
	// #nosec G101 -- the name of an environment variable, not its value.
	storageAWSSecretKeyEnv = "AWS_SECRET_ACCESS_KEY"

	storageAccessKeyKey = "storage.access_key_id"
	storageSecretKeyKey = "storage.secret_access_key"
)

// StorageConfig is optional object storage for archives and covers, over the
// generic S3 API. Leave the section out and the application reads the local
// disk and nothing else. With it, the disk still comes first: only a file
// missing from disk is looked for in the bucket. The only thing the application
// ever writes there is a thumbnail.
type StorageConfig struct {
	// Endpoint is the store's base URL.
	Endpoint string `mapstructure:"endpoint" yaml:"endpoint"`
	Region   string `mapstructure:"region" yaml:"region"`
	Bucket   string `mapstructure:"bucket" yaml:"bucket"`
	// ArchivesPrefix goes in front of a catalog path to make an object key.
	ArchivesPrefix string `mapstructure:"archives_prefix" yaml:"archives_prefix"`
	// CoversPrefix goes in front of a cover's relative path.
	CoversPrefix string `mapstructure:"covers_prefix" yaml:"covers_prefix"`
	// ThumbsPrefix is where thumbnails go in the bucket, and the only place the
	// application ever writes to it.
	ThumbsPrefix string `mapstructure:"thumbs_prefix" yaml:"thumbs_prefix"`
	// ThumbsUpload keeps thumbnails in the bucket. Off, a read-only key is
	// enough and the local thumbnail is the only copy.
	ThumbsUpload bool `mapstructure:"thumbs_upload" yaml:"thumbs_upload"`
	// AccessKeyID and SecretAccessKey are best given through the environment.
	// json:"-" keeps them out of anything that serializes this section.
	AccessKeyID     string `mapstructure:"access_key_id" yaml:"access_key_id" json:"-"`
	SecretAccessKey string `mapstructure:"secret_access_key" yaml:"secret_access_key" json:"-"`
	// PathStyle addresses the bucket as endpoint/bucket; off, as bucket.endpoint.
	PathStyle bool `mapstructure:"path_style" yaml:"path_style"`
	// RequestTimeout bounds one attempt up to the response headers, and any
	// silence inside a response.
	RequestTimeout time.Duration `mapstructure:"request_timeout" yaml:"request_timeout"`
	// Retries is how many times a failed request is tried again.
	Retries int `mapstructure:"retries" yaml:"retries"`
	// BookReadTimeout bounds reading one book from object storage, from the
	// first request to the last byte. A conversion comes on top of it.
	BookReadTimeout time.Duration `mapstructure:"book_read_timeout" yaml:"book_read_timeout"`
	// MaxActiveRequests bounds the requests to the store in progress at once.
	MaxActiveRequests int `mapstructure:"max_active_requests" yaml:"max_active_requests"`
	// CacheDir holds extracted books and the workers' temporary copies.
	CacheDir string `mapstructure:"cache_dir" yaml:"cache_dir"`
	// BookCacheGB bounds the extracted books on disk; 0 disables that cache.
	BookCacheGB int64 `mapstructure:"book_cache_gb" yaml:"book_cache_gb"`
	// DirectoryCacheMB bounds the archive directories kept in memory.
	DirectoryCacheMB int64 `mapstructure:"directory_cache_mb" yaml:"directory_cache_mb"`
	// TempBudgetGB is the largest archive a worker may copy to disk for a scan.
	TempBudgetGB int64 `mapstructure:"temp_budget_gb" yaml:"temp_budget_gb"`
}

// Enabled reports whether object storage is configured at all. Credentials
// alone do not count: a machine may carry them for something else.
func (c *StorageConfig) Enabled() bool {
	return c.Endpoint != "" || c.Bucket != ""
}

func setStorageDefaults() {
	viper.SetDefault("storage.path_style", true)
	viper.SetDefault("storage.request_timeout", storageRequestTimeout)
	viper.SetDefault("storage.retries", storageRetries)
	viper.SetDefault("storage.book_read_timeout", storageBookReadTimeout)
	viper.SetDefault("storage.max_active_requests", storageMaxActive)
	viper.SetDefault("storage.thumbs_prefix", storageThumbsPrefix)
	viper.SetDefault("storage.thumbs_upload", true)
	viper.SetDefault("storage.cache_dir", storageCacheDir)
	viper.SetDefault("storage.book_cache_gb", storageBookCacheGB)
	viper.SetDefault("storage.directory_cache_mb", storageDirectoryCacheMB)
	viper.SetDefault("storage.temp_budget_gb", storageTempBudgetGB)
}

// bindStorageEnvAliases gives the credentials unprefixed names as well, so a
// secret mounted for any S3 tool works. bindEnvKeys already bound the GOPDS_
// twins, and viper checks those first.
func bindStorageEnvAliases() error {
	for key, aliases := range map[string][]string{
		storageAccessKeyKey: {storageAccessKeyEnv, storageAWSAccessKeyEnv},
		storageSecretKeyKey: {storageSecretKeyEnv, storageAWSSecretKeyEnv},
	} {
		if err := viper.BindEnv(append([]string{key}, aliases...)...); err != nil {
			return err
		}
	}
	return nil
}

// validate refuses a half-written section by naming what is missing, and never
// repeats a value. An absent section is valid.
func (c *StorageConfig) validate() error {
	if !c.Enabled() {
		return nil
	}
	var missing []string
	for _, field := range []struct{ key, value string }{
		{"storage.endpoint", c.Endpoint},
		{"storage.region", c.Region},
		{"storage.bucket", c.Bucket},
		{storageAccessKeyKey, c.AccessKeyID},
		{storageSecretKeyKey, c.SecretAccessKey},
		{"storage.cache_dir", c.CacheDir},
	} {
		if field.value == "" {
			missing = append(missing, field.key)
		}
	}
	if len(missing) > 0 {
		return errors.New("storage is configured but missing: " + strings.Join(missing, ", "))
	}
	if !strings.HasPrefix(c.Endpoint, "http://") && !strings.HasPrefix(c.Endpoint, "https://") {
		return errors.New("storage.endpoint must be a full http or https URL")
	}
	limits := []configLimit{
		{"storage.request_timeout", int64(c.RequestTimeout)},
		{"storage.book_read_timeout", int64(c.BookReadTimeout)},
		{"storage.max_active_requests", int64(c.MaxActiveRequests)},
		{"storage.directory_cache_mb", c.DirectoryCacheMB},
		{"storage.temp_budget_gb", c.TempBudgetGB},
	}
	for _, l := range limits {
		if l.value <= 0 {
			return limitError(l.key, l.value)
		}
	}
	if c.BookCacheGB < 0 {
		return rangeError("storage.book_cache_gb", "zero or more")
	}
	if c.Retries < 0 {
		return rangeError("storage.retries", "zero or more")
	}
	return c.validateThumbs()
}

// validateThumbs keeps the one place the application writes clear of the
// originals: a prefix shared with them could overwrite a cover or an archive.
func (c *StorageConfig) validateThumbs() error {
	if !c.ThumbsUpload {
		return nil
	}
	thumbs := KeyPrefix(c.ThumbsPrefix)
	if thumbs == "" {
		return errors.New("storage.thumbs_prefix is required with storage.thumbs_upload on")
	}
	for _, other := range []struct{ key, value string }{
		{"storage.archives_prefix", c.ArchivesPrefix},
		{"storage.covers_prefix", c.CoversPrefix},
	} {
		prefix := KeyPrefix(other.value)
		if strings.HasPrefix(thumbs, prefix) || strings.HasPrefix(prefix, thumbs) {
			return errors.New("storage.thumbs_prefix overlaps " + other.key +
				": give thumbnails their own prefix, or set storage.thumbs_upload to false")
		}
	}
	return nil
}

// KeyPrefix makes a configured prefix a directory, however it was written.
func KeyPrefix(prefix string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}
