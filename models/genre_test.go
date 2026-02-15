package models

import (
	"encoding/json"
	"testing"
)

func TestGenre_DisplayName_WithTitle(t *testing.T) {
	g := Genre{ID: 1, Genre: "sf", Title: "Science Fiction"}
	if got := g.DisplayName(); got != "Science Fiction" {
		t.Errorf("DisplayName() = %q, want %q", got, "Science Fiction")
	}
}

func TestGenre_DisplayName_FallbackToGenre(t *testing.T) {
	g := Genre{ID: 2, Genre: "fantasy", Title: ""}
	if got := g.DisplayName(); got != "fantasy" {
		t.Errorf("DisplayName() = %q, want %q", got, "fantasy")
	}
}

func TestGenre_MarshalJSON_UsesTitle(t *testing.T) {
	g := Genre{ID: 1, Genre: "sf", Title: "Science Fiction"}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if result["genre"] != "Science Fiction" {
		t.Errorf("genre = %q, want %q", result["genre"], "Science Fiction")
	}
	if int64(result["id"].(float64)) != 1 {
		t.Errorf("id = %v, want 1", result["id"])
	}
}

func TestGenre_MarshalJSON_FallbackToGenre(t *testing.T) {
	g := Genre{ID: 2, Genre: "fantasy"}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if result["genre"] != "fantasy" {
		t.Errorf("genre = %q, want %q", result["genre"], "fantasy")
	}
}
