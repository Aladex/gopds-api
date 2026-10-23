package database

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-pg/pg/v10"
)

// AuthorLLMEvalSentAttempt is one attempt of an eval run's job that reached
// a model and got content back: the job's stored input, the attempt's id in
// the call's batch, and the call it rode in. Fingerprint jobs are left out:
// their answers are measurements, not labels.
type AuthorLLMEvalSentAttempt struct {
	JobID            int64           `pg:"job_id"`
	AttemptNo        int             `pg:"attempt_no"`
	ItemKey          string          `pg:"item_key"`
	CallID           int64           `pg:"call_id"`
	Input            json.RawMessage `pg:"input,type:jsonb"`
	ExtractorVersion *string         `pg:"extractor_version"`
}

// AuthorLLMEvalSentAttempts lists the sent attempts of an eval run in job and
// attempt order.
func AuthorLLMEvalSentAttempts(ctx context.Context, db pg.DBI, runID int64) ([]AuthorLLMEvalSentAttempt, error) {
	var rows []AuthorLLMEvalSentAttempt
	_, err := db.QueryContext(ctx, &rows, `
		SELECT j.id AS job_id, a.attempt_no, a.item_key, a.call_id, j.input, j.extractor_version
		FROM author_llm_job j
		JOIN author_llm_attempt a ON a.job_id = j.id
		JOIN author_llm_call c ON c.id = a.call_id
		WHERE j.run_id = ?0 AND j.purpose <> 'eval_fingerprint' AND a.item_key IS NOT NULL
			AND c.response_content IS NOT NULL
		ORDER BY j.id, a.attempt_no`, runID)
	if err != nil {
		return nil, fmt.Errorf("reading an eval run's sent attempts: %w", err)
	}
	return rows, nil
}

// AuthorLLMCallContent returns the raw reply content of the given calls by
// id.
func AuthorLLMCallContent(ctx context.Context, db pg.DBI, callIDs []int64) (map[int64][]byte, error) {
	var rows []struct {
		ID      int64  `pg:"id"`
		Content string `pg:"response_content"`
	}
	if len(callIDs) == 0 {
		return map[int64][]byte{}, nil
	}
	_, err := db.QueryContext(ctx, &rows, `
		SELECT id, response_content FROM author_llm_call
		WHERE id = ANY (?0) AND response_content IS NOT NULL`, pg.Array(callIDs))
	if err != nil {
		return nil, fmt.Errorf("reading call contents: %w", err)
	}
	out := make(map[int64][]byte, len(rows))
	for _, r := range rows {
		out[r.ID] = []byte(r.Content)
	}
	return out, nil
}
