package database

import (
	"gopds-api/models"
	"strings"
)

func SearchAuthors(query string, limit int) ([]models.Author, error) {
	var authors []models.Author
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return authors, nil
	}

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	lowerQuery := strings.ToLower(trimmed)
	words := strings.Fields(lowerQuery)
	if len(words) == 0 {
		return authors, nil
	}

	whereParts := make([]string, 0, len(words))
	whereArgs := make([]interface{}, 0, len(words))
	for _, word := range words {
		if word == "" {
			continue
		}
		whereParts = append(whereParts, "lower(full_name) LIKE ?")
		whereArgs = append(whereArgs, "%"+word+"%")
	}

	if len(whereParts) == 0 {
		return authors, nil
	}

	err := db.Model(&authors).
		Column("id", "full_name").
		Where("full_name IS NOT NULL").
		Where("full_name != ''").
		Where(strings.Join(whereParts, " AND "), whereArgs...).
		OrderExpr("similarity(full_name, ?) DESC", trimmed).
		Order("full_name ASC").
		Limit(limit).
		Select()
	if err != nil {
		return nil, err
	}

	return authors, nil
}

func SearchSeries(query string, limit int) ([]models.Series, error) {
	var series []models.Series
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return series, nil
	}

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	lowerQuery := strings.ToLower(trimmed)
	words := strings.Fields(lowerQuery)
	if len(words) == 0 {
		return series, nil
	}

	whereParts := make([]string, 0, len(words))
	whereArgs := make([]interface{}, 0, len(words))
	for _, word := range words {
		if word == "" {
			continue
		}
		whereParts = append(whereParts, "lower(ser) LIKE ?")
		whereArgs = append(whereArgs, "%"+word+"%")
	}

	if len(whereParts) == 0 {
		return series, nil
	}

	err := db.Model(&series).
		Column("id", "ser").
		Where("ser IS NOT NULL").
		Where("ser != ''").
		Where(strings.Join(whereParts, " AND "), whereArgs...).
		OrderExpr("similarity(ser, ?) DESC", trimmed).
		Order("ser ASC").
		Limit(limit).
		Select()
	if err != nil {
		return nil, err
	}

	return series, nil
}
