package bookvariants

import "testing"

func TestIsPlaceholderAuthor(t *testing.T) {
	placeholders := []string{
		"Unknown", "Admin", "User", "Неизвестный автор", "Автор Неизвестен", "неизвестен Автор",
		"Коллектив авторов", "Группа авторов", "Автор неизвестен -- Кулинария",
		"Автор неизвестен - Эпосы, мифы, легенды и сказания", "Невідомий автор", "(неизвестен автор)",
		"АНОНИМУС", "", "  ",
	}
	for _, name := range placeholders {
		if !IsPlaceholderAuthor(name) {
			t.Errorf("IsPlaceholderAuthor(%q) = false, want true", name)
		}
	}
	people := []string{
		"Алексей Анонимов", "Абдурахман Авторханов", "Народные сказки", "Народное творчество",
		"Лев Толстой", "Admin Smith",
	}
	for _, name := range people {
		if IsPlaceholderAuthor(name) {
			t.Errorf("IsPlaceholderAuthor(%q) = true, want false", name)
		}
	}
}
