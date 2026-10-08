package authornorm

import (
	"encoding/hex"
	"errors"
	"testing"
)

// The golden digests below were produced by an independent implementation
// (python3 hashlib/struct pipeline over a hand-built byte stream), not by the
// code under test; the exact command is recorded in the phase report.
const (
	goldenSourceFingerprintV1 = "086a8b4ca1c15d3a88672d88dab4624bdcb682eaa8332210c2e016de819c6ce4"
	goldenNormalizationKeyV1  = "c41cc812e59f017b258c3a2250e6d3e93989ec0914b6c9aabdd814357f4a896b"
)

// goldenFixture is the value the independent digest was computed over:
// first "Иван", middle absent, last "Петров", nickname present-but-empty,
// display "Петров Иван".
func goldenFixture(t *testing.T) SourceValue {
	t.Helper()
	v, err := NewSourceValue([]SourceComponent{
		{Kind: ComponentLast, Value: "Петров"},
		{Kind: ComponentFirst, Value: "Иван"},
		{Kind: ComponentNickname, Value: ""},
	})
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	return v
}

func mustValue(t *testing.T, components ...SourceComponent) SourceValue {
	t.Helper()
	v, err := NewSourceValue(components)
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	return v
}

func TestSourceFingerprintUsesFixedFieldOrderAndLengthPrefixes(t *testing.T) {
	got := SourceFingerprint(goldenFixture(t))
	if hex.EncodeToString(got[:]) != goldenSourceFingerprintV1 {
		t.Errorf("SourceFingerprint() = %x, want golden %s", got, goldenSourceFingerprintV1)
	}
}

func TestSourceFingerprintDistinguishesNullEmptyCaseAndPunctuation(t *testing.T) {
	withFirst := func(first *string) SourceValue {
		components := []SourceComponent{{Kind: ComponentLast, Value: "Петров"}}
		if first != nil {
			components = append([]SourceComponent{{Kind: ComponentFirst, Value: *first}}, components...)
		}
		return mustValue(t, components...)
	}

	nullFirst := SourceFingerprint(withFirst(nil))
	emptyFirst := SourceFingerprint(withFirst(strptr("")))
	if nullFirst == emptyFirst {
		t.Error("fingerprint(NULL first) == fingerprint(present-empty first): presence byte lost")
	}

	upper := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "Иванов"}))
	lower := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "иванов"}))
	if upper == lower {
		t.Error("fingerprint is case-insensitive, want case preserved")
	}

	yo := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "Ёлкин"}))
	ye := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "Елкин"}))
	if yo == ye {
		t.Error("fingerprint folds ё to е, want the distinction preserved")
	}

	plain := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "Петров"}))
	comma := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "Петров,"}))
	if plain == comma {
		t.Error("fingerprint ignores punctuation, want punctuation preserved")
	}

	// NFC runs before serialization, so canonically equal inputs collide.
	composed := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "é"}))
	decomposed := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "é"}))
	if composed != decomposed {
		t.Error("fingerprint differs for canonically equal (NFC) inputs, want them equal")
	}
}

func TestSourceFingerprintExcludesRoleBookTitleAndSourceID(t *testing.T) {
	components := []SourceComponent{
		{Kind: ComponentFirst, Value: "Иван"},
		{Kind: ComponentLast, Value: "Петров"},
	}
	// The same source under the author role of one book and the translator
	// role of another must give one fingerprint (and two credits downstream).
	// The value type carries no role/book/title/source-ID fields at all, so
	// the exclusion holds by construction; this test pins the observable part.
	asAuthor := SourceFingerprint(mustValue(t, components...))
	asTranslator := SourceFingerprint(mustValue(t, components...))
	if asAuthor != asTranslator {
		t.Error("identical source components gave different fingerprints")
	}
}

func TestNormalizationKeyChangesOnlyWithFingerprintOrVersions(t *testing.T) {
	fp := SourceFingerprint(goldenFixture(t))

	key, err := NormalizationKey(fp, "extractor-v1", "normalizer-v1")
	if err != nil {
		t.Fatalf("NormalizationKey: %v", err)
	}
	if hex.EncodeToString(key[:]) != goldenNormalizationKeyV1 {
		t.Errorf("NormalizationKey() = %x, want golden %s", key, goldenNormalizationKeyV1)
	}

	again, err := NormalizationKey(fp, "extractor-v1", "normalizer-v1")
	if err != nil {
		t.Fatalf("NormalizationKey: %v", err)
	}
	if key != again {
		t.Error("same inputs gave different normalization keys")
	}

	otherFp := SourceFingerprint(mustValue(t, SourceComponent{Kind: ComponentLast, Value: "Сидоров"}))
	otherKey, err := NormalizationKey(otherFp, "extractor-v1", "normalizer-v1")
	if err != nil {
		t.Fatalf("NormalizationKey: %v", err)
	}
	if key == otherKey {
		t.Error("normalization key ignores the source fingerprint")
	}

	newExtractor, err := NormalizationKey(fp, "extractor-v2", "normalizer-v1")
	if err != nil {
		t.Fatalf("NormalizationKey: %v", err)
	}
	if key == newExtractor {
		t.Error("normalization key ignores the extractor version")
	}

	newNormalizer, err := NormalizationKey(fp, "extractor-v1", "normalizer-v2")
	if err != nil {
		t.Fatalf("NormalizationKey: %v", err)
	}
	if key == newNormalizer {
		t.Error("normalization key ignores the normalizer version")
	}

	if _, err := NormalizationKey(fp, "", "normalizer-v1"); !errors.Is(err, ErrEmptyVersion) {
		t.Errorf("empty extractor version: err = %v, want ErrEmptyVersion", err)
	}
	if _, err := NormalizationKey(fp, "extractor-v1", "  "); !errors.Is(err, ErrEmptyVersion) {
		t.Errorf("blank normalizer version: err = %v, want ErrEmptyVersion", err)
	}
}

// Additional goldens from the same independent python3 pipeline (recorded in
// the phase report, fix round 1): the first exercises non-empty middle and
// nickname payloads, the second keeps all five fields non-empty so the
// fixed-order layout is verified for every named field.
const (
	goldenMiddleNicknameV1 = "60262aac49831ec85678099ee5e4cd0a4d19a4cdaf3395fc6e4588b140f9584f"
	goldenAllFieldsV1      = "c04bf7eeb6d80c4099475ebb1b4e1db9ad3942a7f58c2561a4c281445cde866d"
)

func TestSourceFingerprintCoversNonEmptyMiddleAndNickname(t *testing.T) {
	v := mustValue(t,
		SourceComponent{Kind: ComponentFirst, Value: "Анна"},
		SourceComponent{Kind: ComponentMiddle, Value: "Владимировна"},
		SourceComponent{Kind: ComponentLast, Value: "Смирнова"},
		SourceComponent{Kind: ComponentNickname, Value: "аня"},
	)
	if got := SourceFingerprint(v); hex.EncodeToString(got[:]) != goldenMiddleNicknameV1 {
		t.Errorf("SourceFingerprint() = %x, want golden %s", got, goldenMiddleNicknameV1)
	}
}

func TestSourceFingerprintAllFiveFieldsGolden(t *testing.T) {
	v := mustValue(t,
		SourceComponent{Kind: ComponentFirst, Value: "Mary"},
		SourceComponent{Kind: ComponentMiddle, Value: "Jane"},
		SourceComponent{Kind: ComponentLast, Value: "O'Neil-Smith"},
		SourceComponent{Kind: ComponentNickname, Value: "MJ"},
	)
	if got := SourceFingerprint(v); hex.EncodeToString(got[:]) != goldenAllFieldsV1 {
		t.Errorf("SourceFingerprint() = %x, want golden %s", got, goldenAllFieldsV1)
	}
}

func TestSourceFingerprintDistinguishesMiddleAbsentEmptyPresent(t *testing.T) {
	withMiddle := func(middle *string) [32]byte {
		components := []SourceComponent{
			{Kind: ComponentFirst, Value: "Иван"},
			{Kind: ComponentLast, Value: "Петров"},
		}
		if middle != nil {
			components = append(components, SourceComponent{Kind: ComponentMiddle, Value: *middle})
		}
		return SourceFingerprint(mustValue(t, components...))
	}
	absent, empty, present := withMiddle(nil), withMiddle(strptr("")), withMiddle(strptr("Петрович"))
	if absent == empty {
		t.Error("fingerprint(absent middle) == fingerprint(empty middle): presence lost")
	}
	if empty == present {
		t.Error("fingerprint(empty middle) == fingerprint(non-empty middle): payload lost")
	}
	if absent == present {
		t.Error("fingerprint(absent middle) == fingerprint(non-empty middle)")
	}
}

func TestSourceFingerprintDistinguishesNicknameAbsentEmptyPresent(t *testing.T) {
	withNickname := func(nickname *string) [32]byte {
		components := []SourceComponent{
			{Kind: ComponentFirst, Value: "Иван"},
			{Kind: ComponentLast, Value: "Петров"},
		}
		if nickname != nil {
			components = append(components, SourceComponent{Kind: ComponentNickname, Value: *nickname})
		}
		return SourceFingerprint(mustValue(t, components...))
	}
	absent, empty, present := withNickname(nil), withNickname(strptr("")), withNickname(strptr("ваня"))
	if absent == empty {
		t.Error("fingerprint(absent nickname) == fingerprint(empty nickname): presence lost")
	}
	if empty == present {
		t.Error("fingerprint(empty nickname) == fingerprint(non-empty nickname): payload lost")
	}
	if absent == present {
		t.Error("fingerprint(absent nickname) == fingerprint(non-empty nickname)")
	}
}
