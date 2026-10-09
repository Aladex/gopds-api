package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"testing"

	"gopds-api/internal/authornorm"
)

const acceptanceMigration = "25-author-acceptance-by-script.sql"

func acceptanceSQL(t *testing.T) string {
	t.Helper()
	body, err := fs.ReadFile(FS(), acceptanceMigration)
	if err != nil {
		t.Fatalf("reading %s: %v", acceptanceMigration, err)
	}
	return string(body)
}

// The shipped registrations cite the frozen evidence report by path and by
// the SHA-256 of its exact bytes: editing the report, or the hash, without the
// other fails here.
func TestShippedRegistrationsCiteTheEvidenceReport(t *testing.T) {
	sql := acceptanceSQL(t)
	refs := regexp.MustCompile(`'(internal/authornorm/policy_evidence/[^']+\.json)', 'shipped'`).FindAllStringSubmatch(sql, -1)
	hashes := regexp.MustCompile(`decode\('([0-9a-f]{64})', 'hex'\)`).FindAllStringSubmatch(sql, -1)
	if len(refs) != 2 || len(hashes) != 2 {
		t.Fatalf("found %d evidence refs and %d hashes, want the two shipped registrations", len(refs), len(hashes))
	}
	for i := range refs {
		report, err := os.ReadFile("../" + refs[i][1])
		if err != nil {
			t.Fatalf("the cited evidence report is not in the repository: %v", err)
		}
		sum := sha256.Sum256(report)
		if hashes[i][1] != hex.EncodeToString(sum[:]) {
			t.Errorf("registration %d cites hash %s, the report hashes to %x", i, hashes[i][1], sum)
		}
	}
}

// The schema's closed set of scripts is the normalizer's, element for element.
func TestAcceptanceScriptListIsTheNormalizers(t *testing.T) {
	sql := acceptanceSQL(t)
	body := regexp.MustCompile(`(?s)SELECT ARRAY\[(.*?)\]::text\[\]`).FindStringSubmatch(sql)
	if body == nil {
		t.Fatal("no script array in the migration")
	}
	var listed []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(body[1], -1) {
		listed = append(listed, m[1])
	}
	want := make([]string, 0, len(authornorm.Scripts()))
	for _, s := range authornorm.Scripts() {
		want = append(want, string(s))
	}
	if !slices.Equal(listed, want) {
		t.Errorf("migration lists %d scripts, the normalizer emits %d; they differ", len(listed), len(want))
	}
}
