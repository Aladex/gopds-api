package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every statement that writes a credit selection returns the credit it wrote,
// and its caller marks those credits for the author display read model in one
// statement (creditMarks). A writer added later — the LLM worker's bulk
// re-selection among them — that wrote selections any other way would leave
// the model stale until the daily walk; so the writers are counted here, in
// the source of every non-test Go file of the repository, and each must be one
// of the statements below.
func TestEverySelectionWriteReturnsTheCreditItWrote(t *testing.T) {
	for name, statement := range map[string]string{
		"upsertSelectionSQL": upsertSelectionSQL, "leaveUnresolvedSQL": leaveUnresolvedSQL, "markInvalidSQL": markInvalidSQL,
	} {
		assert.True(t, strings.HasSuffix(strings.TrimSpace(statement), "RETURNING s.credit_id"), name)
	}

	write := regexp.MustCompile(`(?i)(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(public\.)?book_contributor_credit_selection\b`)
	var found []string
	const root = ".."
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() && path != root && (d.Name() == "node_modules" || d.Name() == "booksdump-frontend" ||
			strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path) // #nosec G304 -- the repository's own sources
		if readErr != nil {
			return readErr
		}
		for range write.FindAllIndex(src, -1) {
			found = append(found, filepath.ToSlash(path))
		}
		return nil
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"../database/author_normalization_results.go", // upsertSelectionSQL
		"../database/author_metadata_review.go",       // leaveUnresolvedSQL
		"../database/author_metadata_review.go",       // markInvalidSQL
	}, found, "a new selection writer must return its credits and mark them")
}
