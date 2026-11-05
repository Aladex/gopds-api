package main

import (
	"gopds-api/config"
	"gopds-api/internal/archivestore"
	"gopds-api/internal/coverstore"
	"gopds-api/internal/objectstore"
	"gopds-api/logging"
)

const (
	bytesInMB = 1 << 20
	bytesInGB = 1 << 30
)

// initializeStorage connects the archive and cover stores to object storage
// when the storage section is present. Without it the stores keep their
// defaults, which read the disk and nothing else. A section that passed
// validation and still cannot be used stops the start: running on half a
// library without saying so is worse.
func initializeStorage(c *config.StorageConfig, filesPath string) {
	if !c.Enabled() {
		return
	}
	retries := c.Retries
	if retries == 0 {
		retries = -1
	}
	thumbsPrefix := ""
	if c.ThumbsUpload {
		thumbsPrefix = config.KeyPrefix(c.ThumbsPrefix)
	}
	client, err := objectstore.New(&objectstore.Config{
		Endpoint:        c.Endpoint,
		Region:          c.Region,
		Bucket:          c.Bucket,
		AccessKeyID:     objectstore.Secret(c.AccessKeyID),
		SecretAccessKey: objectstore.Secret(c.SecretAccessKey),
		PathStyle:       c.PathStyle,
		RequestTimeout:  c.RequestTimeout,
		Retries:         retries,
		MaxActive:       c.MaxActiveRequests,
		WritePrefix:     thumbsPrefix,
	})
	if err != nil {
		logging.Errorf("Failed to set up object storage: %v", err)
		panic(err)
	}
	archives, err := archivestore.New(&archivestore.Config{
		Root:                filesPath,
		Client:              client,
		Prefix:              config.KeyPrefix(c.ArchivesPrefix),
		CacheDir:            c.CacheDir,
		BookCacheBytes:      c.BookCacheGB * bytesInGB,
		DirectoryCacheBytes: c.DirectoryCacheMB * bytesInMB,
		TempBudgetBytes:     c.TempBudgetGB * bytesInGB,
		ReadBudget:          c.BookReadTimeout,
	})
	if err != nil {
		logging.Errorf("Failed to set up the archive store: %v", err)
		panic(err)
	}
	archivestore.SetDefault(archives)
	coverstore.SetDefault(coverstore.New(client, config.KeyPrefix(c.CoversPrefix)).WithThumbs(thumbsPrefix))
	logging.Infof("Object storage enabled: bucket %s at %s", c.Bucket, c.Endpoint)
}
