package authornorm

import (
	"errors"
	"testing"
)

func validMetadataFixture() SourceMetadata {
	first := "Иван"
	nickname := "ivanp"
	translatorFirst := "John"
	authorValue, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentFirst, Value: first},
		{Kind: ComponentNickname, Value: nickname},
	})
	if err != nil {
		panic(err)
	}
	translatorValue, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentFirst, Value: translatorFirst},
	})
	if err != nil {
		panic(err)
	}
	return SourceMetadata{
		BookID:           42,
		ArchivePath:      "lib/001.zip",
		EntryName:        "book.fb2",
		ExtractorVersion: "extractor-v1",
		BookMD5:          "0123456789abcdef0123456789abcdef",
		Title:            "Title",
		Contributors: []Contributor{
			{Role: RoleAuthor, Position: 0, Value: authorValue},
			{Role: RoleTranslator, Position: 0, Value: translatorValue},
			{Role: RoleAuthor, Position: 1, Value: authorValue},
		},
		Sequences: []Sequence{
			{Index: 0, Parent: -1, Name: "Saga"},
			{Index: 1, Parent: 0, Name: "Inner"},
		},
		Outcome: OutcomeExtracted,
	}
}

func TestSourceMetadataValidateAcceptsWellFormed(t *testing.T) {
	m := validMetadataFixture()
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestSourceMetadataValidateRejectsBadShapes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SourceMetadata)
		want   error
	}{
		{"zero book id", func(m *SourceMetadata) { m.BookID = 0 }, ErrInvalidSourceMetadata},
		{"empty extractor version", func(m *SourceMetadata) { m.ExtractorVersion = "" }, ErrEmptyVersion},
		{"unknown outcome", func(m *SourceMetadata) { m.Outcome = "partial" }, ErrInvalidSourceMetadata},
		{"unknown role", func(m *SourceMetadata) { m.Contributors[0].Role = "editor" }, ErrInvalidSourceMetadata},
		{"author position gap", func(m *SourceMetadata) { m.Contributors[2].Position = 5 }, ErrInvalidSourceMetadata},
		{"translator position gap", func(m *SourceMetadata) { m.Contributors[1].Position = 1 }, ErrInvalidSourceMetadata},
		{"sequence index not document order", func(m *SourceMetadata) { m.Sequences[1].Index = 7 }, ErrInvalidSourceMetadata},
		{"sequence parent points forward", func(m *SourceMetadata) { m.Sequences[0].Parent = 1 }, ErrInvalidSourceMetadata},
		{"sequence parent is self", func(m *SourceMetadata) { m.Sequences[1].Parent = 1 }, ErrInvalidSourceMetadata},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validMetadataFixture()
			tc.mutate(&m)
			if err := m.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSourceMetadataValidateAllowsNoAuthorOutcome(t *testing.T) {
	m := validMetadataFixture()
	m.Contributors = nil
	m.Outcome = OutcomeNoAuthor
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestSourceMetadataValidateOutcomeAuthorConsistency(t *testing.T) {
	t.Run("extracted without author credits", func(t *testing.T) {
		m := validMetadataFixture()
		m.Contributors = m.Contributors[1:] // drop both authors, keep translator
		m.Outcome = OutcomeExtracted
		if err := m.Validate(); !errors.Is(err, ErrInvalidSourceMetadata) {
			t.Fatalf("Validate() = %v, want %v", err, ErrInvalidSourceMetadata)
		}
	})
	t.Run("no-author outcome with an author credit", func(t *testing.T) {
		m := validMetadataFixture()
		m.Outcome = OutcomeNoAuthor
		if err := m.Validate(); !errors.Is(err, ErrInvalidSourceMetadata) {
			t.Fatalf("Validate() = %v, want %v", err, ErrInvalidSourceMetadata)
		}
	})
	t.Run("sequence parent below -1", func(t *testing.T) {
		m := validMetadataFixture()
		m.Sequences[0].Parent = -2
		if err := m.Validate(); !errors.Is(err, ErrInvalidSourceMetadata) {
			t.Fatalf("Validate() = %v, want %v", err, ErrInvalidSourceMetadata)
		}
	})
}
