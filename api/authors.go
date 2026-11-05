package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"

	"github.com/google/uuid"
)

// AuthorAnswer struct for authors list on search page
type AuthorAnswer struct {
	Authors []models.Author `json:"authors"`
	Length  int             `json:"length"`
	Total   int             `json:"total"`
}

// AuthorEntityAnswer is the author search page under the entity identity:
// the same shape, every author an entity with a UUID ID and, when it has a
// namesake, a label, and what the search card says of it. Total is the number
// of authors found, Length the number of pages.
type AuthorEntityAnswer struct {
	Authors []AuthorEntityCard `json:"authors"`
	Length  int                `json:"length"`
	Total   int                `json:"total"`
}

// AuthorEntityCard is one author of the search page. Genres, works and covers
// are arrays, empty and never null, and matched_name is empty until there is
// something to say: the summary fills in behind a live site.
type AuthorEntityCard struct {
	models.AuthorEntity
	Kind        string               `json:"kind"`
	Genres      []string             `json:"genres"`
	Works       []string             `json:"works"`
	Covers      []models.AuthorCover `json:"covers"`
	MatchedName string               `json:"matched_name"`
}

// authorEntityCards are the page's authors, in order, with their summaries.
func authorEntityCards(entities []models.AuthorEntity, summaries map[uuid.UUID]models.AuthorSummary) []AuthorEntityCard {
	cards := make([]AuthorEntityCard, len(entities))
	for i := range entities {
		s := summaries[entities[i].ID]
		card := AuthorEntityCard{AuthorEntity: entities[i], Kind: s.Kind, Genres: s.Genres, Works: s.Works,
			Covers: s.Covers, MatchedName: s.MatchedName}
		if card.Genres == nil {
			card.Genres = []string{}
		}
		if card.Works == nil {
			card.Works = []string{}
		}
		if card.Covers == nil {
			card.Covers = []models.AuthorCover{}
		}
		cards[i] = card
	}
	return cards
}

// pageCount turns a number of authors into the number of pages the search page
// draws its pager from.
//
// It rounds up, which the count/10+1 it replaced did not: a total that divided
// evenly offered a further page with nothing on it, and no results at all still
// offered page one.
func pageCount(total, limit int) int {
	if limit <= 0 {
		limit = defaultAuthorsPerPage
	}
	return (total + limit - 1) / limit
}

// defaultAuthorsPerPage is the page size assumed for a request that names none,
// matching what the search page asks for.
const defaultAuthorsPerPage = 10

// Author names the author whose books a list shows, for its heading
// Auth godoc
// @Summary Retrieve author information
// @Description Get the name of one author: a legacy catalog author by its number, or under
// @Description authors.identity=entity an author entity by its UUID — its display name and, when another
// @Description entity has the same name, the label that tells them apart. The other identity's ID is not found.
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param  request body models.AuthorRequest true "The author's ID: a number, or an entity UUID string"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.Author "Under authors.identity=entity: models.AuthorEntity"
// @Failure 400 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError "No such author under the identity in force"
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/books/author [post]
func (h *SearchHandler) Author(c *gin.Context) {
	var filter models.AuthorRequest
	if err := c.ShouldBindJSON(&filter); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}
	ref, ok := h.authorRef(c, string(filter.ID), errors.New("bad_request"))
	if !ok {
		return
	}
	if h.Entities != nil {
		entity, err := h.Entities.AuthorEntity(c.Request.Context(), ref.Entity)
		switch {
		case errors.Is(err, database.ErrAuthorEntityNotFound):
			httputil.NewError(c, http.StatusNotFound, errors.New("author_not_found"))
			return
		case err != nil:
			httputil.NewError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, entity)
		return
	}
	author, err := database.GetAuthor(ref.Legacy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, author)
}
