package main

import (
	"testing"

	"gopds-api/config"
	"gopds-api/internal/archivestore"
	"gopds-api/internal/coverstore"

	"github.com/stretchr/testify/assert"
)

func TestNoStorageSectionLeavesTheDiskAlone(t *testing.T) {
	initializeStorage(&config.StorageConfig{}, t.TempDir())
	assert.False(t, archivestore.Default().Remote())
	assert.Equal(t, &coverstore.Store{}, coverstore.Default())
}

func TestStorageSectionConnectsBothStores(t *testing.T) {
	previousArchives, previousCovers := archivestore.Default(), coverstore.Default()
	t.Cleanup(func() {
		archivestore.SetDefault(previousArchives)
		coverstore.SetDefault(previousCovers)
	})
	initializeStorage(&config.StorageConfig{
		Endpoint: "https://s3.example.com", Region: "r", Bucket: "b",
		AccessKeyID: "k", SecretAccessKey: "s", CacheDir: t.TempDir(),
		DirectoryCacheMB: 1, TempBudgetGB: 1,
	}, t.TempDir())
	assert.True(t, archivestore.Default().Remote())
	assert.NotEqual(t, &coverstore.Store{}, coverstore.Default())
}

func TestKeyPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "/": "", "archives": "archives/", "archives/": "archives/", "/a/b/": "a/b/",
	} {
		assert.Equal(t, want, keyPrefix(in), in)
	}
}
