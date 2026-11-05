package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopds-api/api"
	"gopds-api/config"
	"gopds-api/internal/coverstore"
	"gopds-api/internal/posters"
	"gopds-api/services"

	"github.com/go-pg/pg/v10"
	"github.com/spf13/viper"
)

// authorMetadataShutdownTimeout bounds how long shutdown waits for the author
// metadata workers. When they stop within it the database pool closes; when
// they do not, the pool stays open for them and the process exits with an
// error (see shutdownAuthorMetadataThenDatabase). An abandoned claim is safe:
// its lease expires and the next start claims it again.
const authorMetadataShutdownTimeout = 10 * time.Second

// buildAuthorMetadataWorkers assembles every embedded author metadata worker
// from the configuration: the extraction loop over the archives under
// archivesDir, and the local normalization loops. This is the one place
// stages are registered.
func buildAuthorMetadataWorkers(
	db *pg.DB,
	archivesDir string,
	c *config.AuthorMetadataConfig,
) ([]services.AuthorMetadataStageWorker, error) {
	return buildAuthorMetadataWorkersOn(db, archivesDir, c, config.LLM())
}

// buildAuthorMetadataWorkersOn is buildAuthorMetadataWorkers against the
// shared llm section given.
func buildAuthorMetadataWorkersOn(
	db *pg.DB,
	archivesDir string,
	c *config.AuthorMetadataConfig,
	shared config.LLMConfig,
) ([]services.AuthorMetadataStageWorker, error) {
	extraction, err := services.NewAuthorMetadataExtractionLoop(db, archivesDir, c)
	if err != nil {
		return nil, fmt.Errorf("building the extraction worker: %w", err)
	}
	workers := []services.AuthorMetadataStageWorker{extraction}

	local, err := services.NewAuthorMetadataLocalLoops(db, c)
	if err != nil {
		return nil, fmt.Errorf("building the local normalization workers: %w", err)
	}
	workers = append(workers, local...)

	// The LLM worker runs when the shared llm section and the participants
	// allow it; otherwise, or when it cannot be built, the other workers run
	// without it and the review queue works as it does without the LLM.
	llmWorker, reason, err := services.NewAuthorLLMWorkerFromConfig(context.Background(), db, archivesDir, c, shared)
	switch {
	case err != nil:
		services.LogAuthorLLMEvent(services.AuthorMetadataEventError, &services.AuthorLLMEvent{
			Name: services.AuthorLLMEventDisabled, SQLState: services.AuthorMetadataSQLState(err),
		})
	case llmWorker == nil:
		services.LogAuthorLLMEvent(services.AuthorMetadataEventInfo, &services.AuthorLLMEvent{
			Name: services.AuthorLLMEventDisabled, Class: reason,
		})
	default:
		workers = append(workers, llmWorker, llmWorker.EvalDriver(), llmWorker.ResolveDriver())
		// The merge configuration's worker (E4c) answers the merge questions
		// on the same endpoint, under its own version; the name
		// normalization's runs as without it. Its eval driver steps the merge
		// evals of its version (E4c-2), its merge run driver the merge runs
		// (E4d-2).
		mergeWorker, _, mergeErr := services.NewAuthorLLMMergeWorkerFromConfig(context.Background(), db, c, shared)
		switch {
		case mergeErr != nil:
			services.LogAuthorLLMEvent(services.AuthorMetadataEventError, &services.AuthorLLMEvent{
				Name: services.AuthorLLMEventDisabled, SQLState: services.AuthorMetadataSQLState(mergeErr),
			})
		case mergeWorker != nil:
			workers = append(workers, mergeWorker, mergeWorker.EvalDriver(), mergeWorker.MergeRunDriver())
		}
	}
	return workers, nil
}

// newAuthorMetadataRunsAPI builds the admin runs service on the run service
// and the same configuration the workers are built from, so a retry's budget
// is the budget of the workers that will claim it.
func newAuthorMetadataRunsAPI(db *pg.DB, c *config.AuthorMetadataConfig) (api.AuthorMetadataRunService, error) {
	admin, err := services.NewAuthorMetadataRunAdmin(services.NewAuthorMetadataRunService(db), db, c)
	if err != nil {
		return nil, err
	}
	return api.NewAuthorMetadataRunService(admin), nil
}

// initializeAuthorMetadataRunsAPI installs the admin runs service the admin
// routes mount. It is wired whether or not the workers run, so an operator
// can always read runs; a failure leaves the routes answering
// run_service_unavailable and is logged as a closed event, never its text.
func initializeAuthorMetadataRunsAPI(db *pg.DB, c *config.AuthorMetadataConfig) {
	svc, err := newAuthorMetadataRunsAPI(db, c)
	if err != nil {
		services.LogAuthorMetadataEvent(services.AuthorMetadataEventError, &services.AuthorMetadataEvent{
			Name: services.AuthorMetadataEventRunsAPINotWired, Stage: services.AuthorMetadataStageRunner,
			SQLState: services.AuthorMetadataSQLState(err),
		})
		return
	}
	api.SetAuthorMetadataRunService(svc)
}

// initializeAuthorLLMAdminAPI installs the LLM block's admin: the same
// configuration identity and version the LLM worker runs under, or the
// reason the layer is off (then the block says so and starts nothing). A
// failure leaves the routes answering llm_service_unavailable and is logged
// as a closed event.
func initializeAuthorLLMAdminAPI(db *pg.DB, c *config.AuthorMetadataConfig, shared config.LLMConfig) {
	admin, err := services.NewAuthorLLMAdminFromConfig(context.Background(), db, c, shared)
	if err != nil {
		services.LogAuthorMetadataEvent(services.AuthorMetadataEventError, &services.AuthorMetadataEvent{
			Name: services.AuthorMetadataEventLLMAdminNotWired, Stage: services.AuthorMetadataStageRunner,
			SQLState: services.AuthorMetadataSQLState(err),
		})
		return
	}
	api.SetAuthorLLMAdminService(admin)
	// The merge configuration's admin (E4c) on the same routes under /merge.
	merge, err := services.NewAuthorLLMMergeAdminFromConfig(context.Background(), db, c, shared)
	if err != nil {
		services.LogAuthorMetadataEvent(services.AuthorMetadataEventError, &services.AuthorMetadataEvent{
			Name: services.AuthorMetadataEventLLMAdminNotWired, Stage: services.AuthorMetadataStageRunner,
			SQLState: services.AuthorMetadataSQLState(err),
		})
		return
	}
	api.SetAuthorLLMMergeAdminService(merge)
}

// authorAutopilotLLM gives the autopilot the LLM block's admin of the
// configuration the workers run, built on its first step (the database
// answers by then) and kept once built.
func authorAutopilotLLM(db *pg.DB, c *config.AuthorMetadataConfig) services.AuthorAutopilotLLM {
	var admin *services.AuthorLLMAdmin
	return func(ctx context.Context) (*services.AuthorLLMAdmin, error) {
		if admin != nil {
			return admin, nil
		}
		built, err := services.NewAuthorLLMAdminFromConfig(ctx, db, c, config.LLM())
		if err != nil {
			return nil, err
		}
		admin = built
		return admin, nil
	}
}

// newAuthorAutopilot is the autopilot's worker: the resolve check under the
// name normalization's admin, the merge step under the merge
// configuration's.
func newAuthorAutopilot(db *pg.DB, c *config.AuthorMetadataConfig) *services.AuthorAutopilot {
	return services.NewAuthorAutopilot(db, authorAutopilotLLM(db, c),
		services.AuthorAutopilotConfig{Quiet: c.Autopilot.QuietPeriod}).WithMerge(authorAutopilotMergeLLM(db, c))
}

// authorAutopilotMergeLLM gives the autopilot's merge step the merge
// configuration's admin (E4c), built and kept as authorAutopilotLLM keeps
// the name normalization's.
func authorAutopilotMergeLLM(db *pg.DB, c *config.AuthorMetadataConfig) services.AuthorAutopilotLLM {
	var admin *services.AuthorLLMAdmin
	return func(ctx context.Context) (*services.AuthorLLMAdmin, error) {
		if admin != nil {
			return admin, nil
		}
		built, err := services.NewAuthorLLMMergeAdminFromConfig(ctx, db, c, config.LLM())
		if err != nil {
			return nil, err
		}
		admin = built
		return admin, nil
	}
}

// errNoDatabase refuses to start the runner without a pool.
var errNoDatabase = errors.New("no database pool")

// startAuthorMetadataRunner hands the workers to a new runner and starts it
// once the database answers; no worker runs before that.
func startAuthorMetadataRunner(
	ctx context.Context,
	db *pg.DB,
	workers []services.AuthorMetadataStageWorker,
) (*services.AuthorMetadataRunner, error) {
	runner := services.NewAuthorMetadataRunner(workers...)
	ready := func(ctx context.Context) error {
		if db == nil {
			return errNoDatabase
		}
		_, err := db.ExecContext(ctx, `SELECT 1`)
		return err
	}
	if err := runner.Start(ctx, ready); err != nil {
		return nil, err
	}
	return runner, nil
}

// coverCheckTimeout bounds asking object storage about one cover. The summary
// batch holds a transaction while it waits.
const coverCheckTimeout = 3 * time.Second

// coverServable says whether the cover store has a book's cover: the disk,
// then the bucket when one is configured. A store that cannot answer right now
// is no evidence of a missing file, so the book's flag stands; the search card
// draws a letter for a cover that fails to load.
func coverServable(bookPath, fileName string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), coverCheckTimeout)
	defer cancel()
	_, err := coverstore.Default().Stat(ctx, viper.GetString("app.posters_path"), posters.RelativePath(bookPath, fileName))
	return err == nil || errors.Is(err, coverstore.ErrUnavailable)
}

// initializeAuthorMetadata starts the runner: the workers when the
// configuration enables them, and always the one-shot acceptance pass, which
// selects the credits waiting for a pair this release registered (the
// policy ships with the code, so nobody else would). A failure is logged and
// leaves the server running without them: the pipeline is background work,
// and its jobs wait in the database.
func initializeAuthorMetadata(db *pg.DB, archivesDir string, c *config.AuthorMetadataConfig) *services.AuthorMetadataRunner {
	// The admin review API is wired from the same configuration whatever the
	// worker switch says: reviewing is an administrator's action, not
	// background work. services.AuthorMetadataReviewConfigFrom shares the
	// local workers' retry budget, so a review retry is refused exactly when
	// they could not claim it.
	if err := api.SetAuthorMetadataReviewService(db, c); err != nil {
		authorMetadataReviewAPINotWired(err)
	}

	// The acceptance pass and the author display read model run whatever
	// the switch says: the policy ships with the code, and search and sorting
	// read the model, which the scan and the admin's edits keep marking. The
	// author entity build runs the passes an administrator or the autopilot
	// starts, and the Wikidata fetch only the fetches. The autopilot starts
	// its resolve runs, and its merge step its merge runs, only when the LLM
	// workers run (their admins say so).
	api.SetAuthorEntityAdminService(services.NewAuthorEntityAdmin(db).WithAutopilotQuiet(c.Autopilot.QuietPeriod))
	workers := []services.AuthorMetadataStageWorker{
		services.NewAuthorAcceptancePass(db),
		services.NewAuthorDisplayModelLoop(db, services.AuthorDisplayModelConfig{CoverExists: coverServable}),
		services.NewAuthorEntityBuildLoop(db, services.AuthorEntityBuildConfig{}),
		services.NewWikidataFetchLoop(db, services.NewQLeverSource(), services.WikidataFetchConfig{}),
		newAuthorAutopilot(db, c),
	}
	if c.Enabled {
		// Workers that cannot be built do not stop the acceptance pass.
		if stages, err := buildAuthorMetadataWorkers(db, archivesDir, c); err != nil {
			authorMetadataNotStarted(err)
		} else {
			workers = append(workers, stages...)
		}
	} else {
		services.LogAuthorMetadataEvent(services.AuthorMetadataEventInfo, &services.AuthorMetadataEvent{
			Name: services.AuthorMetadataEventWorkersDisabled, Stage: services.AuthorMetadataStageRunner,
		})
	}
	runner, err := startAuthorMetadataRunner(context.Background(), db, workers)
	if err != nil {
		authorMetadataNotStarted(err)
		return nil
	}
	return runner
}

// authorMetadataNotStarted records a failed start by its SQLSTATE only: the
// error text of a database failure can quote row data (contract 3.14).
func authorMetadataNotStarted(err error) {
	services.LogAuthorMetadataEvent(services.AuthorMetadataEventError, &services.AuthorMetadataEvent{
		Name: services.AuthorMetadataEventWorkersNotStarted, Stage: services.AuthorMetadataStageRunner,
		SQLState: services.AuthorMetadataSQLState(err),
	})
}

// authorMetadataReviewAPINotWired records a failed review API wiring as a
// closed event with the SQLSTATE at most; the routes then answer
// review_service_unavailable.
func authorMetadataReviewAPINotWired(err error) {
	services.LogAuthorMetadataEvent(services.AuthorMetadataEventError, &services.AuthorMetadataEvent{
		Name: services.AuthorMetadataEventReviewAPINotWired, Stage: services.AuthorMetadataStageReview,
		SQLState: services.AuthorMetadataSQLState(err),
	})
}

// shutdownAuthorMetadataThenDatabase stops the author metadata workers —
// canceling their claims and database calls, waiting at most timeout — and
// only then closes the database pool.
//
// If the deadline passes while a worker is still running, the pool is left
// open and the error is returned: a canceled goroutine stops only when it
// returns, and closing the pool under it would turn its last call into a
// use-after-close. The process owner then exits; the operating system ends
// the connections with the process, and the jobs the worker held keep only
// their leases, which expire and are claimed again on the next start.
func shutdownAuthorMetadataThenDatabase(runner *services.AuthorMetadataRunner, db *pg.DB, timeout time.Duration) error {
	if runner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := runner.Shutdown(ctx)
		cancel()
		if err != nil {
			// The database pool stays open for the workers still running
			// until the process exits.
			services.LogAuthorMetadataEvent(services.AuthorMetadataEventError, &services.AuthorMetadataEvent{
				Name: services.AuthorMetadataEventWorkersShutdownTimeout, Stage: services.AuthorMetadataStageRunner,
			})
			return err
		}
	}
	closeDatabaseConnection(db)
	return nil
}
