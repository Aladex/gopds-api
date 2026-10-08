package services

import (
	"encoding/json"
	"fmt"

	"gopds-api/database"
	"gopds-api/internal/authornorm"
	"gopds-api/models"
)

// The one mapping from a metadata extraction to the input of the atomic source
// repository (database.PersistExtraction). The backfill worker and the live
// dual write of new books both call it, so a stored snapshot never depends on
// which path wrote it. It is pure: no database, no logging — source names never
// leave the value it returns.

// creditProvenance locates one credit's source element inside the FB2
// description. Credits come from title-info only; Position is the element's
// zero-based index among the credit-creating elements of its role.
type creditProvenance struct {
	Section  string `json:"section"`
	Element  string `json:"element"`
	Position int    `json:"position"`
}

// snapshotProvenance keeps what the scalar snapshot columns cannot hold: every
// publish-info publisher, city and year in document order (the columns keep
// the first one).
type snapshotProvenance struct {
	PublishInfo publishInfoProvenance `json:"publish_info"`
}

type publishInfoProvenance struct {
	Publisher []string `json:"publisher"`
	City      []string `json:"city"`
	Year      []string `json:"year"`
}

// sourceSequence is the stored form of one FB2 sequence element.
type sourceSequence struct {
	Index  int     `json:"index"`
	Parent int     `json:"parent"`
	Name   string  `json:"name"`
	Number *string `json:"number"`
}

// titleInfoSection is the only description section that yields credits.
const titleInfoSection = "title-info"

// ExtractionInputFromSourceMetadata maps one validated extraction to the input
// of database.PersistExtraction for the given origin (a backfill run ID, or
// nil for the live path). Contributors keep their XML order, role and source
// id; every structural flag of a contributor element — the properties of the
// element that its stored components and fingerprint cannot reproduce on
// their own — goes into the credit's quality flags. Absent lists become empty
// ones, never nil. A metadata value that fails its own shape invariants is
// rejected with authornorm.ErrInvalidSourceMetadata (or the version error the
// invariants name).
func ExtractionInputFromSourceMetadata(
	md *authornorm.SourceMetadata,
	origin models.BookMetadataSnapshotOrigin,
	runID *int64,
) (*database.ExtractionInput, error) {
	if md == nil {
		return nil, authornorm.ErrInvalidSourceMetadata
	}
	if err := md.Validate(); err != nil {
		return nil, err
	}
	outcome, err := snapshotOutcome(md.Outcome)
	if err != nil {
		return nil, err
	}
	credits, err := extractionCredits(md.Contributors)
	if err != nil {
		return nil, err
	}
	sequences, err := json.Marshal(sourceSequences(md.Sequences))
	if err != nil {
		return nil, fmt.Errorf("encoding source sequences: %w", err)
	}
	provenance, err := json.Marshal(snapshotProvenance{PublishInfo: publishInfoProvenance{
		Publisher: nonNil(md.Publisher),
		City:      nonNil(md.City),
		Year:      nonNil(md.Year),
	}})
	if err != nil {
		return nil, fmt.Errorf("encoding snapshot provenance: %w", err)
	}

	return &database.ExtractionInput{
		BookID:                md.BookID,
		BookMD5:               md.BookMD5,
		ExtractorVersion:      md.ExtractorVersion,
		NormalizerVersion:     authornorm.NormalizerVersion,
		Origin:                origin,
		RunID:                 cloneInt64(runID),
		Outcome:               outcome,
		ArchivePath:           md.ArchivePath,
		EntryName:             md.EntryName,
		XMLProvenance:         provenance,
		SourceTitle:           cloneString(md.Title),
		SourceLang:            cloneString(md.Lang),
		SourceSrcLang:         cloneString(md.SrcLang),
		SourceISBNs:           nonNil(md.ISBNs),
		SourcePublisher:       firstOf(md.Publisher),
		SourceCity:            firstOf(md.City),
		SourceYear:            firstOf(md.Year),
		SourceDocumentID:      cloneString(md.DocumentID),
		SourceDocumentVersion: cloneString(md.DocumentVersion),
		SourceSequences:       sequences,
		QualityFlags:          []string{},
		Credits:               credits,
	}, nil
}

func snapshotOutcome(o authornorm.ExtractionOutcome) (models.BookMetadataSnapshotOutcome, error) {
	switch o {
	case authornorm.OutcomeExtracted:
		return models.BookMetadataSnapshotExtracted, nil
	case authornorm.OutcomeNoAuthor:
		return models.BookMetadataSnapshotExtractedNoAuthor, nil
	}
	return "", authornorm.ErrInvalidSourceMetadata
}

func creditRole(r authornorm.ContributorRole) (models.ContributorRole, error) {
	switch r {
	case authornorm.RoleAuthor:
		return models.ContributorRoleAuthor, nil
	case authornorm.RoleTranslator:
		return models.ContributorRoleTranslator, nil
	}
	return "", authornorm.ErrInvalidSourceMetadata
}

// extractionCredits keeps the contributor order exactly: the repository
// derives each stored position from the order within the role, which
// Validate has already shown to equal the extractor's positions.
func extractionCredits(contributors []authornorm.Contributor) ([]database.ExtractionCredit, error) {
	credits := make([]database.ExtractionCredit, 0, len(contributors))
	for _, c := range contributors {
		role, err := creditRole(c.Role)
		if err != nil {
			return nil, err
		}
		provenance, err := json.Marshal(creditProvenance{
			Section:  titleInfoSection,
			Element:  string(c.Role),
			Position: c.Position,
		})
		if err != nil {
			return nil, fmt.Errorf("encoding credit provenance: %w", err)
		}
		credits = append(credits, database.ExtractionCredit{
			Role:          role,
			Source:        c.Value,
			SourceID:      cloneString(c.SourceID),
			QualityFlags:  contributorStructuralFlags(c.Value),
			XMLProvenance: provenance,
		})
	}
	return credits, nil
}

// contributorStructuralFlags lists, in the canonical flag order, the
// structural properties of one contributor element: a name-bearing child that
// appeared more than once (only the first value reaches a named field, and the
// fingerprint does not carry the repetition), and a present-but-empty one.
// They come from the element's structure alone, never from the text.
func contributorStructuralFlags(v authornorm.SourceValue) []string {
	flags := []string{}
	if v.HasDuplicateComponent() {
		flags = append(flags, string(authornorm.FlagDuplicateComponent))
	}
	for _, field := range []*string{v.First(), v.Middle(), v.Last(), v.Nickname()} {
		if field != nil && *field == "" {
			flags = append(flags, string(authornorm.FlagEmptyComponent))
			break
		}
	}
	return flags
}

// sourceSequences only feeds json.Marshal inside the mapping, so the number
// pointers need no copy: the encoded bytes are what the input keeps.
func sourceSequences(in []authornorm.Sequence) []sourceSequence {
	out := make([]sourceSequence, len(in))
	for i, s := range in {
		out[i] = sourceSequence{Index: s.Index, Parent: s.Parent, Name: s.Name, Number: s.Number}
	}
	return out
}

// firstOf is the first value of a repeated element, nil when it never occurred;
// a present empty element stays a pointer to "".
func firstOf(values []string) *string {
	if len(values) == 0 {
		return nil
	}
	first := values[0]
	return &first
}

func nonNil(values []string) []string {
	return append([]string{}, values...)
}

func cloneString(s *string) *string {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}

func cloneInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	clone := *v
	return &clone
}
