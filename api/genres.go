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

	for i, genre := range genres {
		if publisher != nil {
			publisher.PublishGenreTitleGenProgress(len(genres), i, genre.Genre)
		}

		title := llmSvc.GenerateGenreTitle(genre.Genre)
		if title != genre.Genre {
			if err := database.UpdateGenreTitle(genre.ID, title); err != nil {
				logging.Warnf("Failed to update genre %q title: %v", genre.Genre, err)
				continue
			}
			updated++
		}
	}

	durationMS := time.Since(start).Milliseconds()
	if publisher != nil {
		publisher.PublishGenreTitleGenCompleted(len(genres), updated, durationMS)
	}

	logging.Infof("Genre title generation completed: %d/%d updated in %dms", updated, len(genres), durationMS)
}
