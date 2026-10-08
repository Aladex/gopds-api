package authornorm

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode"
)

const testExtractorVersion = "fb2-metadata-test-v1"

type localCaseComponent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type localCaseWant struct {
	GivenName       string   `json:"given_name"`
	AdditionalNames string   `json:"additional_names"`
	FamilyName      string   `json:"family_name"`
	Nickname        string   `json:"nickname"`
	Prefix          string   `json:"prefix"`
	Suffix          string   `json:"suffix"`
	DisplayName     string   `json:"display_name"`
	SortName        string   `json:"sort_name"`
	SearchKey       string   `json:"search_key"`
	Script          string   `json:"script"`
	Kind            string   `json:"kind"`
	Status          string   `json:"status"`
	Method          string   `json:"method"`
	DecisionClass   string   `json:"decision_class"`
	QualityFlags    []string `json:"quality_flags"`
}

type localCase struct {
	Name       string               `json:"name"`
	Components []localCaseComponent `json:"components"`
	Want       localCaseWant        `json:"want"`
}

var componentKindsByName = map[string]ComponentKind{
	"first":    ComponentFirst,
	"middle":   ComponentMiddle,
	"last":     ComponentLast,
	"nickname": ComponentNickname,
}

func loadLocalCases(t *testing.T) []localCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "local_cases.json"))
	if err != nil {
		t.Fatalf("read local cases: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file struct {
		Description string      `json:"description"`
		Cases       []localCase `json:"cases"`
	}
	if err := decoder.Decode(&file); err != nil {
		t.Fatalf("decode local cases: %v", err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("local cases fixture is empty")
	}
	return file.Cases
}

func (c *localCase) source(t *testing.T) SourceValue {
	t.Helper()
	components := make([]SourceComponent, 0, len(c.Components))
	for _, component := range c.Components {
		kind, ok := componentKindsByName[component.Kind]
		if !ok {
			t.Fatalf("case %q: unknown component kind %q", c.Name, component.Kind)
		}
		components = append(components, SourceComponent{Kind: kind, Value: component.Value})
	}
	v, err := NewSourceValue(components)
	if err != nil {
		t.Fatalf("case %q: NewSourceValue: %v", c.Name, err)
	}
	return v
}

func mustNormalize(t *testing.T, v SourceValue) Result {
	t.Helper()
	r, err := Normalize(v, testExtractorVersion)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return r
}

func mustSource(t *testing.T, components ...SourceComponent) SourceValue {
	t.Helper()
	v, err := NewSourceValue(components)
	if err != nil {
		t.Fatalf("NewSourceValue: %v", err)
	}
	return v
}

func flagNames(flags []QualityFlag) []string {
	names := make([]string, 0, len(flags))
	for _, f := range flags {
		names = append(names, string(f))
	}
	return names
}

// TestNormalizeLocalCases pins the whole candidate of every golden case:
// presentation fields, search key, script, kind, status, method, decision
// class and the ordered quality flags.
func TestNormalizeLocalCases(t *testing.T) {
	for _, tc := range loadLocalCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			got := mustNormalize(t, tc.source(t))
			want := tc.Want
			checks := []struct {
				field     string
				got, want string
			}{
				{"given_name", got.GivenName, want.GivenName},
				{"additional_names", got.AdditionalNames, want.AdditionalNames},
				{"family_name", got.FamilyName, want.FamilyName},
				{"nickname", got.Nickname, want.Nickname},
				{"prefix", got.Prefix, want.Prefix},
				{"suffix", got.Suffix, want.Suffix},
				{"display_name", got.DisplayName, want.DisplayName},
				{"sort_name", got.SortName, want.SortName},
				{"search_key", got.SearchKey, want.SearchKey},
				{"script", string(got.Script), want.Script},
				{"kind", string(got.Kind), want.Kind},
				{"status", string(got.Status), want.Status},
				{"method", string(got.Method), want.Method},
				{"decision_class", string(got.DecisionClass), want.DecisionClass},
			}
			for _, c := range checks {
				if c.got != c.want {
					t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
				}
			}
			wantFlags := want.QualityFlags
			if wantFlags == nil {
				wantFlags = []string{}
			}
			if gotFlags := flagNames(got.QualityFlags); !slices.Equal(gotFlags, wantFlags) {
				t.Errorf("quality_flags = %q, want %q", gotFlags, wantFlags)
			}
			if err := got.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil for a normalizer result", err)
			}
		})
	}
}

// TestLocalCasesCoverEveryDecisionClass keeps the golden table honest: a
// decision class without a fixture is a class nobody has looked at.
func TestLocalCasesCoverEveryDecisionClass(t *testing.T) {
	seen := map[DecisionClass]bool{}
	for _, tc := range loadLocalCases(t) {
		seen[DecisionClass(tc.Want.DecisionClass)] = true
	}
	for _, class := range DecisionClasses() {
		if !seen[class] {
			t.Errorf("decision class %q has no case in testdata/local_cases.json", class)
		}
	}
}

func TestNormalizeCarriesFingerprintKeyAndVersions(t *testing.T) {
	v := mustSource(t,
		SourceComponent{Kind: ComponentFirst, Value: "Лев"},
		SourceComponent{Kind: ComponentLast, Value: "Толстой"},
	)
	r := mustNormalize(t, v)

	if r.SourceFingerprint != SourceFingerprint(v) {
		t.Error("SourceFingerprint differs from the phase 1 fingerprint of the same source")
	}
	wantKey, err := NormalizationKey(SourceFingerprint(v), testExtractorVersion, NormalizerVersion)
	if err != nil {
		t.Fatalf("NormalizationKey: %v", err)
	}
	if r.NormalizationKey != wantKey {
		t.Error("NormalizationKey is not derived from the fingerprint, extractor version and NormalizerVersion")
	}
	if r.ExtractorVersion != testExtractorVersion || r.NormalizerVersion != NormalizerVersion {
		t.Errorf("versions = (%q, %q), want (%q, %q)", r.ExtractorVersion, r.NormalizerVersion, testExtractorVersion, NormalizerVersion)
	}
	if r.SchemaVersion != ResultSchemaVersion || ResultSchemaVersion <= 0 {
		t.Errorf("SchemaVersion = %d, want positive ResultSchemaVersion %d", r.SchemaVersion, ResultSchemaVersion)
	}
	again := mustNormalize(t, v)
	if !slices.Equal(again.QualityFlags, r.QualityFlags) || again.DisplayName != r.DisplayName ||
		again.NormalizationKey != r.NormalizationKey {
		t.Error("Normalize is not deterministic for the same input")
	}
}

func TestNormalizeRejectsEmptyExtractorVersionAndZeroSource(t *testing.T) {
	v := mustSource(t, SourceComponent{Kind: ComponentLast, Value: "Толстой"})
	if _, err := Normalize(v, " "); !errors.Is(err, ErrEmptyVersion) {
		t.Errorf("blank extractor version: err = %v, want ErrEmptyVersion", err)
	}
	if _, err := Normalize(SourceValue{}, testExtractorVersion); !errors.Is(err, ErrNoNameComponents) {
		t.Errorf("zero SourceValue: err = %v, want ErrNoNameComponents", err)
	}
}

func TestSearchKeyFoldsCaseYoAndSeparatorsWithoutTransliteration(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"case fold", "ТОЛСТОЙ Лев", "толстой лев"},
		{"capital yo folded to e", "ЁЛКИН", "елкин"},
		{"small yo folded to e", "Семёнов", "семенов"},
		{"decomposed yo composed first", "Семёнов", "семенов"},
		{"short i keeps its breve", "Йорк", "йорк"},
		{"latin diacritics kept", "Ångström Émile", "ångström émile"},
		{"german sharp s full fold", "Strauß", "strauss"},
		{"no transliteration of cyrillic", "Пушкин", "пушкин"},
		{"no transliteration of latin", "Pushkin", "pushkin"},
		{"hyphen becomes separator", "Салтыков-Щедрин", "салтыков щедрин"},
		{"apostrophe becomes separator", "O'Connor", "o connor"},
		{"dots and commas collapse", "Толкин,  Дж.Р.Р.", "толкин дж р р"},
		{"nbsp and em space collapse", "Анна  Мария", "анна мария"},
		{"edges trimmed", " -- Иван -- ", "иван"},
		{"punctuation only becomes empty", "*** --", ""},
		{"symbols and digits kept", "Иван 2 +", "иван 2 +"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SearchKey(tc.in); got != tc.want {
				t.Errorf("SearchKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDetectScript(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Script
	}{
		{"cyrillic", "Лев Толстой", ScriptCyrillic},
		{"latin", "Leo Tolstoy", ScriptLatin},
		{"latin with diacritics", "Søren Ångström", ScriptLatin},
		{"greek", "Νίκος", "Grek"},
		{"armenian", "Ованес Туманян Հովհաննես", ScriptMixed},
		{"han", "村上春樹", "Hani"},
		{"arabic", "نجيب محفوظ", "Arab"},
		{"common digits do not create mixed", "Иван 2", ScriptCyrillic},
		{"common punctuation and symbols do not create mixed", "Иван-Петров (№1) © ’", ScriptCyrillic},
		{"inherited combining mark does not create mixed", "Иван́", ScriptCyrillic},
		{"cyrillic and latin letters are mixed", "Иван Smith", ScriptMixed},
		{"single latin homoglyph is mixed", "Сeмёнова", ScriptMixed},
		{"no letters is undetermined", "*** 2002", ScriptUndetermined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectScript(tc.in); got != tc.want {
				t.Errorf("DetectScript(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestEveryUnicodeScriptHasAnISOCode makes a toolchain upgrade that adds a
// Unicode script fail loudly instead of silently changing normalizer output.
func TestEveryUnicodeScriptHasAnISOCode(t *testing.T) {
	for name := range unicode.Scripts {
		code, ok := iso15924ByUnicodeScript[name]
		if !ok {
			t.Errorf("unicode script %q has no ISO 15924 code", name)
			continue
		}
		if err := Script(code).Validate(); err != nil {
			t.Errorf("ISO 15924 code %q of %q does not validate: %v", code, name, err)
		}
	}
	if len(iso15924ByUnicodeScript) != len(unicode.Scripts) {
		t.Errorf("table has %d scripts, unicode.Scripts has %d", len(iso15924ByUnicodeScript), len(unicode.Scripts))
	}
}

// TestDecisionClassRoutingIsClosed pins the routing property of every class
// so phase 10 routes by A3 through Ambiguous/Kind/Status and never by string
// matching on the class name.
func TestDecisionClassRoutingIsClosed(t *testing.T) {
	type routing struct {
		ambiguous bool
		kind      Kind
		status    Status
	}
	want := map[DecisionClass]routing{
		ClassStructuredPerson:    {false, KindPerson, StatusNormalized},
		ClassPlaceholder:         {false, KindUnknown, StatusNormalized},
		ClassMalformed:           {false, KindMalformed, StatusInvalid},
		ClassCollectiveCandidate: {true, KindCollective, StatusUnresolved},
		ClassPlaceholderFragment: {true, KindUnknown, StatusUnresolved},
		ClassDuplicateComponent:  {true, KindPerson, StatusUnresolved},
		ClassMixedScript:         {true, KindPerson, StatusUnresolved},
		ClassUnusualCharacters:   {true, KindPerson, StatusUnresolved},
		ClassInitials:            {true, KindPerson, StatusUnresolved},
		ClassParticle:            {true, KindPerson, StatusUnresolved},
		ClassAffix:               {true, KindPerson, StatusUnresolved},
		ClassSingleField:         {true, KindPerson, StatusUnresolved},
		ClassMononym:             {true, KindPerson, StatusUnresolved},
		ClassPartialStructure:    {true, KindPerson, StatusUnresolved},
		ClassNicknameWithName:    {true, KindPerson, StatusUnresolved},
		ClassCaseDefect:          {true, KindPerson, StatusUnresolved},
	}
	classes := DecisionClasses()
	if len(classes) != len(want) {
		t.Errorf("DecisionClasses() has %d classes, the routing table pins %d", len(classes), len(want))
	}
	for _, class := range classes {
		w, ok := want[class]
		if !ok {
			t.Errorf("class %q has no pinned routing", class)
			continue
		}
		if err := class.Validate(); err != nil {
			t.Errorf("%q.Validate() = %v", class, err)
		}
		if got := (routing{class.Ambiguous(), class.Kind(), class.Status()}); got != w {
			t.Errorf("%q routing = %+v, want %+v", class, got, w)
		}
	}
	unknown := DecisionClass("guessed_person")
	if err := unknown.Validate(); !errors.Is(err, ErrUnknownDecisionClass) {
		t.Errorf("unknown class Validate() = %v, want ErrUnknownDecisionClass", err)
	}
	if !unknown.Ambiguous() {
		t.Error("an unknown class must fail closed as ambiguous")
	}
}

func TestPlaceholdersStayDistinctSourceValues(t *testing.T) {
	inputs := []string{"Автор неизвестен", "Аноним", "АНОНИМ", "Anonymous", "Unknown"}
	displays := map[string]bool{}
	fingerprints := map[[32]byte]bool{}
	keys := map[[32]byte]bool{}
	for _, in := range inputs {
		r := mustNormalize(t, mustSource(t, SourceComponent{Kind: ComponentLast, Value: in}))
		if r.Kind != KindUnknown || r.DecisionClass != ClassPlaceholder {
			t.Errorf("%q: kind/class = %q/%q, want unknown/placeholder", in, r.Kind, r.DecisionClass)
		}
		if r.DisplayName != in {
			t.Errorf("%q: display = %q, placeholder must keep its own display", in, r.DisplayName)
		}
		displays[r.DisplayName] = true
		fingerprints[r.SourceFingerprint] = true
		keys[r.NormalizationKey] = true
	}
	if len(displays) != len(inputs) || len(fingerprints) != len(inputs) || len(keys) != len(inputs) {
		t.Errorf("placeholders merged: %d displays, %d fingerprints, %d keys for %d inputs",
			len(displays), len(fingerprints), len(keys), len(inputs))
	}
}

func TestResultValidateRejectsBadVersionsAndUnknownEnums(t *testing.T) {
	valid := mustNormalize(t, mustSource(t,
		SourceComponent{Kind: ComponentFirst, Value: "Лев"},
		SourceComponent{Kind: ComponentLast, Value: "Толстой"},
	))
	if err := valid.Validate(); err != nil {
		t.Fatalf("baseline Validate() = %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Result)
		want   error
	}{
		{"zero schema version", func(r *Result) { r.SchemaVersion = 0 }, ErrInvalidSchemaVersion},
		{"negative schema version", func(r *Result) { r.SchemaVersion = -1 }, ErrInvalidSchemaVersion},
		{"empty normalizer version", func(r *Result) { r.NormalizerVersion = "" }, ErrEmptyVersion},
		{"empty extractor version", func(r *Result) { r.ExtractorVersion = "  " }, ErrEmptyVersion},
		{"unknown kind", func(r *Result) { r.Kind = "human" }, ErrUnknownKind},
		{"unknown status", func(r *Result) { r.Status = "accepted" }, ErrUnknownStatus},
		{"unknown method", func(r *Result) { r.Method = "guess" }, ErrUnknownMethod},
		{"unknown script", func(r *Result) { r.Script = "Cyrillic" }, ErrUnknownScript},
		{"lower-case script code", func(r *Result) { r.Script = "cyrl" }, ErrUnknownScript},
		{"unknown decision class", func(r *Result) { r.DecisionClass = "person" }, ErrUnknownDecisionClass},
		{"unknown quality flag", func(r *Result) { r.QualityFlags = []QualityFlag{"confident"} }, ErrUnknownQualityFlag},
		{"zero fingerprint", func(r *Result) { r.SourceFingerprint = [32]byte{} }, ErrMissingFingerprint},
		{"zero normalization key", func(r *Result) { r.NormalizationKey = [32]byte{} }, ErrMissingFingerprint},
		{"kind contradicts class", func(r *Result) { r.Kind = KindCollective }, ErrInconsistentResult},
		{"status contradicts class", func(r *Result) { r.Status = StatusInvalid }, ErrInconsistentResult},
		{"selectable class carries a structural ambiguity flag", func(r *Result) {
			r.QualityFlags = append(r.QualityFlags, FlagDuplicateComponent)
		}, ErrInconsistentResult},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			r.QualityFlags = slices.Clone(valid.QualityFlags)
			tc.mutate(&r)
			if err := r.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResultValidateAcceptsEveryClosedEnumValue(t *testing.T) {
	for _, k := range []Kind{KindPerson, KindCollective, KindUnknown, KindMalformed} {
		if err := k.Validate(); err != nil {
			t.Errorf("kind %q: %v", k, err)
		}
	}
	for _, s := range []Status{StatusNormalized, StatusUnresolved, StatusInvalid} {
		if err := s.Validate(); err != nil {
			t.Errorf("status %q: %v", s, err)
		}
	}
	// llm stays in the enum (scope A1) so a later LLM stage needs no migration.
	for _, m := range []Method{MethodStructured, MethodRules, MethodLLM, MethodManual} {
		if err := m.Validate(); err != nil {
			t.Errorf("method %q: %v", m, err)
		}
	}
	for _, f := range QualityFlags() {
		if err := f.Validate(); err != nil {
			t.Errorf("flag %q: %v", f, err)
		}
	}
	for _, s := range []Script{ScriptCyrillic, ScriptLatin, ScriptMixed, ScriptUndetermined, "Grek", "Hani"} {
		if err := s.Validate(); err != nil {
			t.Errorf("script %q: %v", s, err)
		}
	}
}
