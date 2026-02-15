package api

import (
	"errors"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/llm"
	"gopds-api/logging"
	"gopds-api/models"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type updateGenreTitleRequest struct {
	Title string `json:"title" binding:"required"`
}

// GetGenres returns all genres with raw id/genre/title fields.
func GetGenres(c *gin.Context) {
	genres, err := database.GetAllGenres()
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, models.Result{
		Result: genres,
		Error:  nil,
	})
}

// UpdateGenreTitle updates a single genre's title.
func UpdateGenreTitle(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_genre_id"))
		return
	}

	var req updateGenreTitleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_request_body"))
		return
	}

	if err := database.UpdateGenreTitle(id, req.Title); err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, models.Result{
		Result: "ok",
		Error:  nil,
	})
}

// GenerateGenreTitles launches async genre title generation via LLM.
func GenerateGenreTitles(c *gin.Context) {
	go runGenreTitleGeneration()
	c.JSON(http.StatusAccepted, models.Result{
		Result: "generation_started",
		Error:  nil,
	})
}

func runGenreTitleGeneration() {
	publisher := newScanEventPublisher()

	genres, err := database.GetGenresForTitleGeneration()
	if err != nil {
		logging.Errorf("Failed to get genres for title generation: %v", err)
		return
	}

	if len(genres) == 0 {
		if publisher != nil {
			publisher.PublishGenreTitleGenCompleted(0, 0, 0)
		}
		return
	}

	if publisher != nil {
		publisher.PublishGenreTitleGenStarted(len(genres))
	}

	llmSvc := llm.NewLLMService()
	start := time.Now()
	updated := 0

	// Build a set of existing titles to detect duplicates.
	allGenres, _ := database.GetAllGenres()
	existingTitles := make(map[string]bool, len(allGenres))
	for _, g := range allGenres {
		existingTitles[g.Title] = true
	}

	for i, genre := range genres {
		if publisher != nil {
			publisher.PublishGenreTitleGenProgress(len(genres), i, genre.Genre)
		}

		// Skip nonsensical tags like "?".
		if strings.TrimSpace(genre.Genre) == "?" {
			continue
		}

		// Fetch sample books for context.
		var bookCtx []llm.GenreBookContext
		samples, err := database.GetSampleBooksForGenre(genre.ID)
		if err == nil {
			for _, s := range samples {
				bookCtx = append(bookCtx, llm.GenreBookContext{
					Title:      s.Title,
					Authors:    s.Authors,
					Annotation: s.Annotation,
				})
			}
		}

		title := llmSvc.GenerateGenreTitleWithBooks(genre.Genre, bookCtx)

		// Retry up to 3 times if LLM keeps returning already-existing titles.
		var excluded []string
		for attempts := 0; attempts < 10 && title != genre.Genre && existingTitles[title]; attempts++ {
			excluded = append(excluded, title)
			logging.Infof("Genre title %q for %q collides with existing (attempt %d), retrying", title, genre.Genre, attempts+1)
			title = llmSvc.GenerateGenreTitleUnique(genre.Genre, bookCtx, excluded)
		}

		if title != genre.Genre && !existingTitles[title] {
			if err := database.UpdateGenreTitle(genre.ID, title); err != nil {
				logging.Warnf("Failed to update genre %q title: %v", genre.Genre, err)
				continue
			}
			existingTitles[title] = true
			updated++
		}
	}

	durationMS := time.Since(start).Milliseconds()
	if publisher != nil {
		publisher.PublishGenreTitleGenCompleted(len(genres), updated, durationMS)
	}

	logging.Infof("Genre title generation completed: %d/%d updated in %dms", updated, len(genres), durationMS)
}
