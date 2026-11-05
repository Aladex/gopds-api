package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopds-api/internal/coverstore"
	"gopds-api/internal/posters"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The summary's cover check asks the cover store about the same relative path
// the cover routes serve.
func TestCoverServableFollowsTheFileTheCoverRouteServes(t *testing.T) {
	dir := t.TempDir()
	previous := viper.GetString("app.posters_path")
	viper.Set("app.posters_path", dir)
	t.Cleanup(func() { viper.Set("app.posters_path", previous) })

	full := posters.FilePath(dir, "fb2-1.zip", "5.fb2")
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte("jpeg"), 0o600))

	assert.True(t, coverServable("fb2-1.zip", "5.fb2"))
	assert.False(t, coverServable("fb2-1.zip", "6.fb2"), "the flag says a cover, the disk says none")
	assert.False(t, coverServable("../..", "passwd"), "a path outside the posters is no cover")

	_, err := coverstore.Default().Stat(context.Background(), dir, "gone.jpg")
	assert.True(t, errors.Is(err, coverstore.ErrNotFound), "the error coverServable tells from an outage")
}
