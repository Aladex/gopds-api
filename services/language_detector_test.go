package services

import (
	"testing"
	"time"
)

func TestStandardizeLanguage(t *testing.T) {
	detector := NewLanguageDetector(false, 5*time.Second)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Russian
		{"Russian ru", "ru", "ru"},
		{"Russian rus", "rus", "ru"},
		{"Russian full", "russian", "ru"},
		{"Russian uppercase", "RU", "ru"},

		// English
		{"English en", "en", "en"},
		{"English eng", "eng", "en"},
		{"English full", "english", "en"},

		// German
		{"German de", "de", "de"},
		{"German ger", "ger", "de"},
		{"German deu", "deu", "de"},
		{"German full", "german", "de"},

		// French
		{"French fr", "fr", "fr"},
		{"French fre", "fre", "fr"},
		{"French fra", "fra", "fr"},

		// Spanish
		{"Spanish es", "es", "es"},
		{"Spanish spa", "spa", "es"},

		// Ukrainian
		{"Ukrainian uk", "uk", "uk"},
		{"Ukrainian ukr", "ukr", "uk"},

		// Polish
		{"Polish pl", "pl", "pl"},
		{"Polish pol", "pol", "pl"},

		// Edge cases
		{"Empty string", "", ""},
		{"Unknown long", "swahili", "unknown"},
		{"Unknown short", "xx", "xx"},
		{"With spaces", " ru ", "ru"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.StandardizeLanguage(tt.input)
			if result != tt.expected {
				t.Errorf("StandardizeLanguage(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStandardizeLanguageCache(t *testing.T) {
	detector := NewLanguageDetector(false, 5*time.Second)

	// First call - should cache
	result1 := detector.StandardizeLanguage("russian")
	if result1 != "ru" {
		t.Errorf("First call failed: got %q, want 'ru'", result1)
	}

	// Second call - should use cache
	result2 := detector.StandardizeLanguage("russian")
	if result2 != "ru" {
		t.Errorf("Second call failed: got %q, want 'ru'", result2)
	}

	// Clear cache
	detector.ClearCache()

	// Third call - should work after cache clear
	result3 := detector.StandardizeLanguage("russian")
	if result3 != "ru" {
		t.Errorf("After cache clear: got %q, want 'ru'", result3)
	}
}

func TestDetectLanguageWithRussianText(t *testing.T) {

	// Russian text sample
	russianText := `
	Это книга о приключениях молодого человека в России. 
	Он путешествовал по разным городам и встречал интересных людей.
	История начинается в Москве, где главный герой работал программистом.
	Каждый день он ходил на работу и мечтал о путешествиях.
	Однажды он решил изменить свою жизнь и отправился в большое приключение.
	`

	tests := []struct {
		name           string
		tagLang        string
		expectedLang   string
		expectedMethod DetectionMethod
		minConfidence  float64
	}{
		{
			name:           "Correct Russian tag",
			tagLang:        "ru",
			expectedLang:   "ru",
			expectedMethod: MethodTagMatch,
			minConfidence:  0.85,
		},
		{
			name:           "Wrong English tag",
			tagLang:        "en",
			expectedLang:   "ru",
			expectedMethod: MethodLinguaConfident,
			minConfidence:  0.85,
		},
		{
			name:           "No tag",
			tagLang:        "",
			expectedLang:   "ru",
			expectedMethod: MethodLinguaConfident,
			minConfidence:  0.85,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create new detector for each test to avoid cache issues
			detector := NewLanguageDetector(false, 5*time.Second)
			result := detector.DetectLanguage(tt.tagLang, russianText)

			if result.Language != tt.expectedLang {
				t.Errorf("Language = %q, want %q", result.Language, tt.expectedLang)
			}

			if result.Method != tt.expectedMethod {
				t.Errorf("Method = %q, want %q", result.Method, tt.expectedMethod)
			}

			if result.Confidence < tt.minConfidence {
				t.Errorf("Confidence = %.2f, want >= %.2f", result.Confidence, tt.minConfidence)
			}
		})
	}
}

func TestDetectLanguageWithEnglishText(t *testing.T) {
	englishText := `
	This is a story about a young man's adventures in America.
	He traveled across different cities and met interesting people.
	The story begins in New York, where the main character worked as a programmer.
	Every day he went to work and dreamed about traveling the world.
	One day he decided to change his life and embarked on a great adventure.
	`

	tests := []struct {
		name           string
		tagLang        string
		expectedLang   string
		expectedMethod DetectionMethod
	}{
		{
			name:           "Correct English tag",
			tagLang:        "en",
			expectedLang:   "en",
			expectedMethod: MethodTagMatch,
		},
		{
			name:           "Wrong Russian tag",
			tagLang:        "ru",
			expectedLang:   "en",
			expectedMethod: MethodLinguaConfident,
		},
		{
			name:           "No tag",
			tagLang:        "",
			expectedLang:   "en",
			expectedMethod: MethodLinguaConfident,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create new detector for each test to avoid cache issues
			detector := NewLanguageDetector(false, 5*time.Second)
			result := detector.DetectLanguage(tt.tagLang, englishText)

			if result.Language != tt.expectedLang {
				t.Errorf("Language = %q, want %q", result.Language, tt.expectedLang)
			}

			if result.Method != tt.expectedMethod {
				t.Errorf("Method = %q, want %q", result.Method, tt.expectedMethod)
			}
		})
	}
}

func TestDetectLanguageEdgeCases(t *testing.T) {
	detector := NewLanguageDetector(false, 5*time.Second)

	t.Run("Empty text with tag", func(t *testing.T) {
		result := detector.DetectLanguage("ru", "")
		if result.Language != "ru" {
			t.Errorf("Expected 'ru', got %q", result.Language)
		}
		if result.Method != MethodTagFallback {
			t.Errorf("Expected MethodTagFallback, got %q", result.Method)
		}
	})

	t.Run("Very short text", func(t *testing.T) {
		result := detector.DetectLanguage("en", "Hello")
		if result.Language != "en" {
			t.Errorf("Expected 'en', got %q", result.Language)
		}
		if result.Method != MethodTagFallback {
			t.Errorf("Expected MethodTagFallback, got %q", result.Method)
		}
	})

	t.Run("No text and no tag", func(t *testing.T) {
		result := detector.DetectLanguage("", "")
		if result.Language != "" {
			t.Errorf("Expected empty string, got %q", result.Language)
		}
		if result.Method != MethodTagFallback {
			t.Errorf("Expected MethodTagFallback, got %q", result.Method)
		}
	})

	t.Run("Numbers and symbols only", func(t *testing.T) {
		result := detector.DetectLanguage("en", "123 456 789 !@# $%^ &*() []{}...")
		// Should fallback to tag since no real text
		if result.Language != "en" {
			t.Errorf("Expected 'en', got %q", result.Language)
		}
	})
}

func TestDetectLanguageCache(t *testing.T) {
	detector := NewLanguageDetector(false, 5*time.Second)

	russianText := "Это русский текст о программировании и разработке программного обеспечения."

	// First detection
	result1 := detector.DetectLanguage("ru", russianText)
	if result1.Language != "ru" {
		t.Errorf("First detection failed: got %q, want 'ru'", result1.Language)
	}

	// Second detection with same text - should use cache
	result2 := detector.DetectLanguage("en", russianText)
	if result2.Language != result1.Language {
		t.Errorf("Cache should return same result: got %q, want %q", result2.Language, result1.Language)
	}

	// Clear cache
	detector.ClearCache()

	// Third detection after cache clear - should detect again
	result3 := detector.DetectLanguage("en", russianText)
	// This time with wrong tag, should detect as Russian
	if result3.Language != "ru" {
		t.Errorf("After cache clear: got %q, want 'ru'", result3.Language)
	}
}

func TestDetectLanguageGerman(t *testing.T) {
	detector := NewLanguageDetector(false, 5*time.Second)

	germanText := `
	Dies ist eine Geschichte über die Abenteuer eines jungen Mannes in Deutschland.
	Er reiste durch verschiedene Städte und traf interessante Menschen.
	Die Geschichte beginnt in Berlin, wo die Hauptfigur als Programmierer arbeitete.
	Jeden Tag ging er zur Arbeit und träumte vom Reisen um die Welt.
	`

	result := detector.DetectLanguage("de", germanText)

	if result.Language != "de" {
		t.Errorf("Language = %q, want 'de'", result.Language)
	}

	if result.Confidence < 0.85 {
		t.Errorf("Confidence too low: %.2f", result.Confidence)
	}
}

func TestDetectLanguageFrench(t *testing.T) {
	detector := NewLanguageDetector(false, 5*time.Second)

	frenchText := `
	C'est une histoire sur les aventures d'un jeune homme en France.
	Il a voyagé à travers différentes villes et a rencontré des gens intéressants.
	L'histoire commence à Paris, où le personnage principal travaillait comme programmeur.
	Chaque jour, il allait au travail et rêvait de voyager dans le monde entier.
	`

	result := detector.DetectLanguage("fr", frenchText)

	if result.Language != "fr" {
		t.Errorf("Language = %q, want 'fr'", result.Language)
	}

	if result.Confidence < 0.85 {
		t.Errorf("Confidence too low: %.2f", result.Confidence)
	}
}

func TestConcurrentAccess(t *testing.T) {
	detector := NewLanguageDetector(false, 5*time.Second)

	russianText := "Это русский текст для тестирования конкурентного доступа."
	englishText := "This is English text for testing concurrent access."

	done := make(chan bool, 10)

	// Run multiple goroutines simultaneously
	for i := 0; i < 10; i++ {
		go func(id int) {
			if id%2 == 0 {
				result := detector.DetectLanguage("ru", russianText)
				if result.Language != "ru" {
					t.Errorf("Goroutine %d: expected 'ru', got %q", id, result.Language)
				}
			} else {
				result := detector.DetectLanguage("en", englishText)
				if result.Language != "en" {
					t.Errorf("Goroutine %d: expected 'en', got %q", id, result.Language)
				}
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
