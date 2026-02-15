package database

import (
	"gopds-api/logging"
	"gopds-api/models"
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
