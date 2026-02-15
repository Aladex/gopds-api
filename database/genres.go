package database

import (
	"gopds-api/logging"
	"gopds-api/models"
	"strings"

	"github.com/go-pg/pg/v10"
)

// GenreAdmin is a flat representation of a genre for the admin API,
// bypassing the custom MarshalJSON on models.Genre.
type GenreAdmin struct {
	ID    int64  `json:"id"`
	Genre string `json:"genre"`
	Title string `json:"title"`
}

// GetAllGenres returns all genres ordered by genre tag.
func GetAllGenres() ([]GenreAdmin, error) {
	var genres []models.Genre
	err := db.Model(&genres).Order("genre ASC").Select()
	if err != nil {
		logging.Error(err)
		return nil, err
	}

	result := make([]GenreAdmin, len(genres))
	for i, g := range genres {
		result[i] = GenreAdmin{
			ID:    g.ID,
			Genre: g.Genre,
			Title: g.Title,
		}
	}
	return result, nil
}

// UpdateGenreTitle updates the title of a single genre by ID.
func UpdateGenreTitle(id int64, title string) error {
	genre := &models.Genre{ID: id}
	_, err := db.Model(genre).
		Set("title = ?", title).
		Where("id = ?", id).
		Update()
	if err != nil {
		logging.Error(err)
	}
	return err
}

// GetGenresForTitleGeneration returns genres where title equals genre (need LLM generation).
func GetGenresForTitleGeneration() ([]models.Genre, error) {
	var genres []models.Genre
	err := db.Model(&genres).
		Where("title = genre").
		Order("genre ASC").
		Select()
	if err != nil {
		logging.Error(err)
		return nil, err
	}
	return genres, nil
}

// GenreSampleBook holds minimal book info for LLM context.
type GenreSampleBook struct {
	Title      string `json:"title"`
	Authors    string `json:"authors"`
	Annotation string `json:"annotation"`
}

// GetSampleBooksForGenre returns up to 5 books belonging to the given genre.
func GetSampleBooksForGenre(genreID int64) ([]GenreSampleBook, error) {
	var bookIDs []int64
	err := db.Model(&models.OrderToGenre{}).
		Column("book_id").
		Where("genre_id = ?", genreID).
		Limit(5).
		Select(&bookIDs)
	if err != nil || len(bookIDs) == 0 {
		return nil, err
	}

	var books []models.Book
	err = db.Model(&books).
		Relation("Authors").
		Where("book.id IN (?)", pg.In(bookIDs)).
		Select()
	if err != nil {
		return nil, err
	}

	result := make([]GenreSampleBook, 0, len(books))
	for _, b := range books {
		var authorNames []string
		for _, a := range b.Authors {
			authorNames = append(authorNames, a.FullName)
		}
		annotation := b.Annotation
		if len(annotation) > 300 {
			annotation = annotation[:300] + "..."
		}
		result = append(result, GenreSampleBook{
			Title:      b.Title,
			Authors:    strings.Join(authorNames, ", "),
			Annotation: annotation,
		})
	}
	return result, nil
}
