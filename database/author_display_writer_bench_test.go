package database

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"gopds-api/internal/authornorm"

	"github.com/stretchr/testify/require"
)

// The cost the writers pay for the read model's marks, measured on demand
// (AUTHOR_DISPLAY_WRITER_BENCH=1): a scan-sized run of new snapshots, and one
// input resolved for thousands of credits at once — the shape a bulk
// re-selection takes, as the LLM worker's will — first changing every
// selection, then again changing none. The same file runs on the code
// without the marks, so the two runs compare the writers alone.
func TestAuthorDisplayWriterBench(t *testing.T) {
	if os.Getenv("AUTHOR_DISPLAY_WRITER_BENCH") == "" {
		t.Skip("set AUTHOR_DISPLAY_WRITER_BENCH=1 to measure the writers")
	}
	storeDB = jobsDB(t)
	t.Cleanup(func() { storeDB = nil })
	const books, rounds = 2000, 5
	ctx := context.Background()

	var persist, resolveChanged, resolveSame []time.Duration
	for round := 0; round < rounds; round++ {
		f := withStoreTx(t)
		start := time.Now()
		for i := 0; i < books; i++ {
			f.persistBook(authorCredit(structuredSource(t)))
		}
		persist = append(persist, time.Since(start))

		r := normalizedAs(t, structuredSource(t), authornorm.ClassStructuredPerson)
		out := decide(t, registeredPolicy(t, f), &r, f.storeResult(&r))
		start = time.Now()
		report, err := ResolveCredits(ctx, f.tx, r.SourceFingerprint[:], r.ExtractorVersion, out)
		require.NoError(t, err)
		resolveChanged = append(resolveChanged, time.Since(start))
		require.Equal(t, books, report.States["selected"])

		start = time.Now()
		_, err = ResolveCredits(ctx, f.tx, r.SourceFingerprint[:], r.ExtractorVersion, out)
		require.NoError(t, err)
		resolveSame = append(resolveSame, time.Since(start))
		require.NoError(t, f.tx.Rollback())
	}
	t.Logf("%d books, median of %d rounds: persist %s, resolve (all change) %s, resolve (no change) %s",
		books, rounds, median(persist), median(resolveChanged), median(resolveSame))
}

func median(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2].Round(time.Millisecond)
}
