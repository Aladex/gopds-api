package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completeStorage() StorageConfig {
	return StorageConfig{
		Endpoint: "https://s3.example.com", Region: "eu", Bucket: "lib",
		AccessKeyID: "visible-key", SecretAccessKey: "visible-secret",
		RequestTimeout: time.Second, BookReadTimeout: time.Second, MaxActiveRequests: 1,
		CacheDir: "/cache", DirectoryCacheMB: 1, TempBudgetGB: 1,
	}
}

func TestAbsentStorageIsValidAndOff(t *testing.T) {
	var c StorageConfig
	assert.False(t, c.Enabled())
	assert.NoError(t, c.validate())
}

func TestCredentialsAloneDoNotEnableStorage(t *testing.T) {
	c := StorageConfig{AccessKeyID: "k", SecretAccessKey: "s"}
	assert.False(t, c.Enabled())
	assert.NoError(t, c.validate())
}

func TestCompleteStorageIsValid(t *testing.T) {
	c := completeStorage()
	assert.True(t, c.Enabled())
	assert.NoError(t, c.validate())

	c.BookCacheGB = 0
	assert.NoError(t, c.validate(), "zero turns the book cache off")
}

func TestPartialStorageNamesWhatIsMissingAndNoValues(t *testing.T) {
	c := completeStorage()
	c.Region, c.SecretAccessKey = "", ""

	err := c.validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.region")
	assert.Contains(t, err.Error(), "storage.secret_access_key")
	assert.NotContains(t, err.Error(), "visible-key")
}

func TestStorageLimitsAreChecked(t *testing.T) {
	for name, breakIt := range map[string]func(*StorageConfig){
		"endpoint":  func(c *StorageConfig) { c.Endpoint = "s3.example.com" },
		"timeout":   func(c *StorageConfig) { c.RequestTimeout = 0 },
		"read":      func(c *StorageConfig) { c.BookReadTimeout = 0 },
		"active":    func(c *StorageConfig) { c.MaxActiveRequests = 0 },
		"directory": func(c *StorageConfig) { c.DirectoryCacheMB = 0 },
		"budget":    func(c *StorageConfig) { c.TempBudgetGB = 0 },
		"cache":     func(c *StorageConfig) { c.BookCacheGB = -1 },
		"retries":   func(c *StorageConfig) { c.Retries = -1 },
	} {
		c := completeStorage()
		breakIt(&c)
		assert.Error(t, c.validate(), name)
	}
}

func TestStorageCredentialsComeFromTheEnvironment(t *testing.T) {
	const key, secret = "key-from-the-environment", "secret-from-the-environment"
	for name, env := range map[string][2]string{
		"own names": {storageAccessKeyEnv, storageSecretKeyEnv},
		"aws names": {storageAWSAccessKeyEnv, storageAWSSecretKeyEnv},
		"prefixed":  {"GOPDS_STORAGE_ACCESS_KEY_ID", "GOPDS_STORAGE_SECRET_ACCESS_KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			t.Setenv(env[0], key)
			t.Setenv(env[1], secret)
			viper.SetEnvPrefix("GOPDS")
			viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
			require.NoError(t, bindEnvKeys(reflect.TypeOf(Config{}), ""))
			require.NoError(t, bindStorageEnvAliases())
			assert.Equal(t, key, viper.GetString(storageAccessKeyKey))
			assert.Equal(t, secret, viper.GetString(storageSecretKeyKey))
		})
	}
}

func TestStorageCredentialsAreNotSerialized(t *testing.T) {
	encoded, err := json.Marshal(completeStorage())
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "visible-key")
	assert.NotContains(t, string(encoded), "visible-secret")
}
