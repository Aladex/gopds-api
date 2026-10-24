package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gopds-api/database"
	"gopds-api/internal/wikidata"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// The Wikidata reference of the author entity build (design E, phase E4w):
// an administrator starts a fetch from the «Сканирование» card, and this
// worker reads it from the source — QLever's SPARQL endpoint, from the pod —
// one chunk after another, each chunk written with its rows in one
// transaction, then publishes the extract (database.PublishWikidataExtract)
// and deletes the rows of the extracts it replaced. A failed chunk fails the
// fetch with its message; the next start goes on from the chunks written.
// The next entity build reads the published extract.

// WikidataFetchConfig bounds the worker: its lease on a fetch (renewed with
// every chunk) and its rest when there is nothing to fetch.
type WikidataFetchConfig struct {
	Lease time.Duration
	Poll  time.Duration
}

const (
	// A chunk is one SPARQL request of up to a few hundred thousand rows.
	defaultWikidataLease = 15 * time.Minute
	defaultWikidataPoll  = 10 * time.Second
	// wikidataRequestTimeout bounds one chunk's request and response.
	wikidataRequestTimeout = 10 * time.Minute
)

func (c WikidataFetchConfig) withDefaults() WikidataFetchConfig {
	if c.Lease <= 0 {
		c.Lease = defaultWikidataLease
	}
	if c.Poll <= 0 {
		c.Poll = defaultWikidataPoll
	}
	return c
}

// NewQLeverSource is the default source: QLever's public endpoint.
func NewQLeverSource() wikidata.Source {
	return &wikidata.QLever{Endpoint: wikidata.DefaultEndpoint, Client: &http.Client{Timeout: wikidataRequestTimeout}}
}

// WikidataFetchLoop runs the fetch an administrator started.
type WikidataFetchLoop struct {
	db     *pg.DB
	source wikidata.Source
	cfg    WikidataFetchConfig
	owner  uuid.UUID
}

// NewWikidataFetchLoop builds the worker over source.
func NewWikidataFetchLoop(db *pg.DB, source wikidata.Source, cfg WikidataFetchConfig) *WikidataFetchLoop {
	return &WikidataFetchLoop{db: db, source: source, cfg: cfg.withDefaults(), owner: uuid.New()}
}

// wikidataFetchName is the worker's log label.
const wikidataFetchName = string(AuthorMetadataStageWikidata)

// Name is the worker's log label.
func (l *WikidataFetchLoop) Name() string { return wikidataFetchName }

// Run works until ctx is canceled.
func (l *WikidataFetchLoop) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		worked, err := l.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
				Name: AuthorMetadataEventWikidataFetchStepFailed, Stage: AuthorMetadataStageWikidata,
				SQLState: AuthorMetadataSQLState(err), Worker: wikidataFetchName,
			})
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(l.cfg.Poll):
		}
	}
	return nil
}

// RunOnce fetches the running extract to its end, or until a chunk fails.
// It reports whether there was a fetch to work on.
func (l *WikidataFetchLoop) RunOnce(ctx context.Context) (bool, error) {
	ext, err := database.ClaimWikidataFetch(ctx, l.db, l.owner, l.cfg.Lease, l.source.Name())
	if err != nil || ext == nil {
		return false, err
	}
	done, err := database.WikidataChunksDone(ctx, l.db, ext.ID)
	if err != nil {
		return true, err
	}
	for _, c := range wikidata.Chunks() {
		if done[c.Name] {
			continue
		}
		parsed, chunkErr := l.chunk(ctx, ext.ID, c)
		if chunkErr != nil {
			return true, l.fail(ctx, ext.ID, chunkErr)
		}
		LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
			Name: AuthorMetadataEventWikidataChunkImported, Stage: AuthorMetadataStageWikidata, RunID: ext.ID,
			Count: parsed.Rows, Worker: wikidataFetchName,
		})
	}
	status, err := database.PublishWikidataExtract(ctx, l.db, ext.ID, l.owner, l.cfg.Lease)
	if err != nil {
		return true, l.fail(ctx, ext.ID, err)
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventWikidataPublished, Stage: AuthorMetadataStageWikidata, RunID: ext.ID,
		Status: status, Worker: wikidataFetchName,
	})
	// The replaced rows go after the swap: nothing reads them any more.
	_, err = database.PruneWikidataExtracts(ctx, l.db)
	return true, err
}

// chunk reads one chunk from the source and writes it.
func (l *WikidataFetchLoop) chunk(ctx context.Context, extractID int64, c wikidata.Chunk) (wikidata.Parsed, error) {
	body, err := l.source.Open(ctx, c)
	if err != nil {
		return wikidata.Parsed{}, err
	}
	defer body.Close()
	return database.ImportWikidataChunk(ctx, l.db, extractID, l.owner, l.cfg.Lease, c, body)
}

// fail ends the fetch with a message the card shows: the source's own error
// (an HTTP status, a network failure), or a database error's SQLSTATE only —
// its text can quote row data. A fetch another worker took is left to it.
func (l *WikidataFetchLoop) fail(ctx context.Context, extractID int64, cause error) error {
	if errors.Is(cause, database.ErrWikidataLeaseLost) || ctx.Err() != nil {
		return cause
	}
	msg := cause.Error()
	var pgErr pg.Error
	if errors.As(cause, &pgErr) {
		msg = fmt.Sprintf("database error, SQLSTATE %s", AuthorMetadataSQLState(cause))
	}
	LogAuthorMetadataEvent(AuthorMetadataEventWarn, &AuthorMetadataEvent{
		Name: AuthorMetadataEventWikidataFetchFailed, Stage: AuthorMetadataStageWikidata, RunID: extractID,
		SQLState: AuthorMetadataSQLState(cause), Worker: wikidataFetchName,
	})
	if err := database.FailWikidataExtract(ctx, l.db, extractID, l.owner, msg); err != nil {
		return err
	}
	return nil
}

// WikidataStatus is the Wikidata block of the card: the newest fetch with
// its progress, and the published extract the build reads.
type WikidataStatus struct {
	Latest    *database.WikidataExtract `json:"latest"`
	Published *database.WikidataExtract `json:"published"`
}

// WikidataStatus reads the card's Wikidata block.
func (a *AuthorEntityAdmin) WikidataStatus(ctx context.Context) (*WikidataStatus, error) {
	latest, published, err := database.LatestWikidataExtracts(ctx, a.db)
	if err != nil {
		return nil, err
	}
	return &WikidataStatus{Latest: latest, Published: published}, nil
}

// StartWikidataFetch records a fetch from QLever, or resumes the one that
// failed; the worker picks it up.
func (a *AuthorEntityAdmin) StartWikidataFetch(ctx context.Context, actor int64) (*database.WikidataExtract, error) {
	ext, err := database.StartWikidataFetch(ctx, a.db, actor, wikidata.DefaultEndpoint)
	if err != nil {
		return nil, err
	}
	LogAuthorMetadataEvent(AuthorMetadataEventInfo, &AuthorMetadataEvent{
		Name: AuthorMetadataEventWikidataFetchStarted, Stage: AuthorMetadataStageWikidata, RunID: ext.ID,
	})
	return ext, nil
}
