package authornorm

import (
	"errors"
	"slices"
	"strings"
)

// Versions of the local normalization contract. Any change to the rules in
// normalize.go that can change a Result for the same input is a new
// NormalizerVersion; any change to the Result shape is a new
// ResultSchemaVersion.
const (
	NormalizerVersion   = "authornorm-local-v1"
	ResultSchemaVersion = 1
)

var (
	// ErrInvalidSchemaVersion marks a result whose schema version is zero or
	// negative.
	ErrInvalidSchemaVersion = errors.New("authornorm: result schema version must be positive")
	// ErrUnknownKind marks a kind outside person/collective/unknown/malformed.
	ErrUnknownKind = errors.New("authornorm: unknown kind")
	// ErrUnknownStatus marks a status outside normalized/unresolved/invalid.
	ErrUnknownStatus = errors.New("authornorm: unknown status")
	// ErrUnknownMethod marks a method outside structured/rules/llm/manual.
	ErrUnknownMethod = errors.New("authornorm: unknown method")
	// ErrUnknownScript marks a script that is neither a known ISO 15924 code
	// nor "mixed".
	ErrUnknownScript = errors.New("authornorm: unknown script")
	// ErrUnknownDecisionClass marks a decision class outside the closed set.
	ErrUnknownDecisionClass = errors.New("authornorm: unknown decision class")
	// ErrUnknownQualityFlag marks a quality flag outside the closed set.
	ErrUnknownQualityFlag = errors.New("authornorm: unknown quality flag")
	// ErrMissingFingerprint marks a result without a source fingerprint or
	// normalization key.
	ErrMissingFingerprint = errors.New("authornorm: result has no source fingerprint or normalization key")
	// ErrInconsistentResult marks a local result whose kind or status
	// contradicts the routing of its decision class.
	ErrInconsistentResult = errors.New("authornorm: result kind or status contradicts its decision class")
)

// Kind is what a credit lexically names.
type Kind string

// Closed set of kinds.
const (
	KindPerson     Kind = "person"
	KindCollective Kind = "collective"
	KindUnknown    Kind = "unknown"
	KindMalformed  Kind = "malformed"
)

// Validate reports ErrUnknownKind for a value outside the closed set.
func (k Kind) Validate() error {
	switch k {
	case KindPerson, KindCollective, KindUnknown, KindMalformed:
		return nil
	}
	return ErrUnknownKind
}

// Status is the normalizer's own verdict on its candidate. It is not a
// selection: whether a result is selected is decided by AcceptancePolicy.
type Status string

// Closed set of statuses.
const (
	StatusNormalized Status = "normalized"
	StatusUnresolved Status = "unresolved"
	StatusInvalid    Status = "invalid"
)

// Validate reports ErrUnknownStatus for a value outside the closed set.
func (s Status) Validate() error {
	switch s {
	case StatusNormalized, StatusUnresolved, StatusInvalid:
		return nil
	}
	return ErrUnknownStatus
}

// Method records how a result was produced. MethodLLM stays in the enum so a
// later LLM stage needs no schema change; nothing in this package produces it.
type Method string

// Closed set of methods.
const (
	MethodStructured Method = "structured"
	MethodRules      Method = "rules"
	MethodLLM        Method = "llm"
	MethodManual     Method = "manual"
)

// Validate reports ErrUnknownMethod for a value outside the closed set.
func (m Method) Validate() error {
	switch m {
	case MethodStructured, MethodRules, MethodLLM, MethodManual:
		return nil
	}
	return ErrUnknownMethod
}

// local reports whether the method is one this package produces; only local
// results are bound to the class routing table.
func (m Method) local() bool { return m == MethodStructured || m == MethodRules }

// Script is the ISO 15924 code of the single letter script of a name,
// ScriptMixed when letters come from several scripts, or ScriptUndetermined
// when the name has no letters at all. Common and Inherited code points never
// add a script.
type Script string

// Named scripts used by the contract; every other ISO 15924 code from
// iso15924ByUnicodeScript is equally valid.
const (
	ScriptCyrillic     Script = "Cyrl"
	ScriptLatin        Script = "Latn"
	ScriptUndetermined Script = "Zyyy"
	ScriptMixed        Script = "mixed"
)

// Validate reports ErrUnknownScript for anything that is neither "mixed" nor
// an ISO 15924 code of a Unicode script.
func (s Script) Validate() error {
	if s == ScriptMixed {
		return nil
	}
	if _, ok := knownScriptCodes[s]; ok {
		return nil
	}
	return ErrUnknownScript
}

// iso15924ByUnicodeScript maps every Unicode script name known to the Go
// unicode tables to its ISO 15924 code (Unicode PropertyValueAliases, sc).
// It is part of the normalizer contract: a test fails when the toolchain
// adds a script that is missing here, instead of silently changing output.
var iso15924ByUnicodeScript = parseScriptTable(`
Adlam=Adlm Ahom=Ahom Anatolian_Hieroglyphs=Hluw Arabic=Arab Armenian=Armn Avestan=Avst
Balinese=Bali Bamum=Bamu Bassa_Vah=Bass Batak=Batk Bengali=Beng Bhaiksuki=Bhks
Bopomofo=Bopo Brahmi=Brah Braille=Brai Buginese=Bugi Buhid=Buhd Canadian_Aboriginal=Cans
Carian=Cari Caucasian_Albanian=Aghb Chakma=Cakm Cham=Cham Cherokee=Cher Chorasmian=Chrs
Common=Zyyy Coptic=Copt Cuneiform=Xsux Cypriot=Cprt Cypro_Minoan=Cpmn Cyrillic=Cyrl
Deseret=Dsrt Devanagari=Deva Dives_Akuru=Diak Dogra=Dogr Duployan=Dupl
Egyptian_Hieroglyphs=Egyp Elbasan=Elba Elymaic=Elym Ethiopic=Ethi Georgian=Geor
Glagolitic=Glag Gothic=Goth Grantha=Gran Greek=Grek Gujarati=Gujr Gunjala_Gondi=Gong
Gurmukhi=Guru Han=Hani Hangul=Hang Hanifi_Rohingya=Rohg Hanunoo=Hano Hatran=Hatr
Hebrew=Hebr Hiragana=Hira Imperial_Aramaic=Armi Inherited=Zinh Inscriptional_Pahlavi=Phli
Inscriptional_Parthian=Prti Javanese=Java Kaithi=Kthi Kannada=Knda Katakana=Kana Kawi=Kawi
Kayah_Li=Kali Kharoshthi=Khar Khitan_Small_Script=Kits Khmer=Khmr Khojki=Khoj
Khudawadi=Sind Lao=Laoo Latin=Latn Lepcha=Lepc Limbu=Limb Linear_A=Lina Linear_B=Linb
Lisu=Lisu Lycian=Lyci Lydian=Lydi Mahajani=Mahj Makasar=Maka Malayalam=Mlym Mandaic=Mand
Manichaean=Mani Marchen=Marc Masaram_Gondi=Gonm Medefaidrin=Medf Meetei_Mayek=Mtei
Mende_Kikakui=Mend Meroitic_Cursive=Merc Meroitic_Hieroglyphs=Mero Miao=Plrd Modi=Modi
Mongolian=Mong Mro=Mroo Multani=Mult Myanmar=Mymr Nabataean=Nbat Nag_Mundari=Nagm
Nandinagari=Nand New_Tai_Lue=Talu Newa=Newa Nko=Nkoo Nushu=Nshu
Nyiakeng_Puachue_Hmong=Hmnp Ogham=Ogam Ol_Chiki=Olck Old_Hungarian=Hung Old_Italic=Ital
Old_North_Arabian=Narb Old_Permic=Perm Old_Persian=Xpeo Old_Sogdian=Sogo
Old_South_Arabian=Sarb Old_Turkic=Orkh Old_Uyghur=Ougr Oriya=Orya Osage=Osge Osmanya=Osma
Pahawh_Hmong=Hmng Palmyrene=Palm Pau_Cin_Hau=Pauc Phags_Pa=Phag Phoenician=Phnx
Psalter_Pahlavi=Phlp Rejang=Rjng Runic=Runr Samaritan=Samr Saurashtra=Saur Sharada=Shrd
Shavian=Shaw Siddham=Sidd SignWriting=Sgnw Sinhala=Sinh Sogdian=Sogd Sora_Sompeng=Sora
Soyombo=Soyo Sundanese=Sund Syloti_Nagri=Sylo Syriac=Syrc Tagalog=Tglg Tagbanwa=Tagb
Tai_Le=Tale Tai_Tham=Lana Tai_Viet=Tavt Takri=Takr Tamil=Taml Tangsa=Tnsa Tangut=Tang
Telugu=Telu Thaana=Thaa Thai=Thai Tibetan=Tibt Tifinagh=Tfng Tirhuta=Tirh Toto=Toto
Ugaritic=Ugar Vai=Vaii Vithkuqi=Vith Wancho=Wcho Warang_Citi=Wara Yezidi=Yezi Yi=Yiii
Zanabazar_Square=Zanb
`)

// parseScriptTable reads white-space separated "UnicodeName=Code" pairs.
func parseScriptTable(table string) map[string]string {
	fields := strings.Fields(table)
	scripts := make(map[string]string, len(fields))
	for _, pair := range fields {
		name, code, _ := strings.Cut(pair, "=")
		scripts[name] = code
	}
	return scripts
}

var knownScriptCodes = func() map[Script]struct{} {
	codes := make(map[Script]struct{}, len(iso15924ByUnicodeScript))
	for _, code := range iso15924ByUnicodeScript {
		codes[Script(code)] = struct{}{}
	}
	return codes
}()

// DecisionClass is the versioned family a local result belongs to. It is the
// only key the acceptance policy registers, and it carries its routing as a
// closed property (Ambiguous, Kind, Status) so callers never match strings.
type DecisionClass string

// Closed set of decision classes of NormalizerVersion v1, in precedence
// order: when several properties hold, the earliest class names the result
// and the rest stay visible as quality flags.
const (
	ClassMalformed           DecisionClass = "malformed"
	ClassPlaceholder         DecisionClass = "placeholder"
	ClassCollectiveCandidate DecisionClass = "collective_candidate"
	ClassPlaceholderFragment DecisionClass = "placeholder_fragment"
	ClassDuplicateComponent  DecisionClass = "duplicate_component"
	ClassMixedScript         DecisionClass = "mixed_script"
	ClassUnusualCharacters   DecisionClass = "unusual_characters"
	ClassInitials            DecisionClass = "initials"
	ClassParticle            DecisionClass = "particle"
	ClassAffix               DecisionClass = "affix"
	ClassSingleField         DecisionClass = "single_field"
	ClassMononym             DecisionClass = "mononym"
	ClassPartialStructure    DecisionClass = "partial_structure"
	ClassNicknameWithName    DecisionClass = "nickname_with_name"
	ClassCaseDefect          DecisionClass = "case_defect"
	ClassStructuredPerson    DecisionClass = "structured_person"
)

type classRouting struct {
	ambiguous bool
	kind      Kind
}

// decisionClassOrder is the closed list in precedence order.
var decisionClassOrder = []DecisionClass{
	ClassMalformed, ClassPlaceholder, ClassCollectiveCandidate, ClassPlaceholderFragment,
	ClassDuplicateComponent, ClassMixedScript, ClassUnusualCharacters, ClassInitials,
	ClassParticle, ClassAffix, ClassSingleField, ClassMononym, ClassPartialStructure,
	ClassNicknameWithName, ClassCaseDefect, ClassStructuredPerson,
}

// decisionClassRouting is the single place that says which classes are
// ambiguous (scope A3: proposal + review item) and which kind a class names.
// Non-ambiguous classes are either malformed (credit invalid) or eligible for
// policy registration.
var decisionClassRouting = map[DecisionClass]classRouting{
	ClassMalformed:           {ambiguous: false, kind: KindMalformed},
	ClassPlaceholder:         {ambiguous: false, kind: KindUnknown},
	ClassStructuredPerson:    {ambiguous: false, kind: KindPerson},
	ClassCollectiveCandidate: {ambiguous: true, kind: KindCollective},
	ClassPlaceholderFragment: {ambiguous: true, kind: KindUnknown},
	ClassDuplicateComponent:  {ambiguous: true, kind: KindPerson},
	ClassMixedScript:         {ambiguous: true, kind: KindPerson},
	ClassUnusualCharacters:   {ambiguous: true, kind: KindPerson},
	ClassInitials:            {ambiguous: true, kind: KindPerson},
	ClassParticle:            {ambiguous: true, kind: KindPerson},
	ClassAffix:               {ambiguous: true, kind: KindPerson},
	ClassSingleField:         {ambiguous: true, kind: KindPerson},
	ClassMononym:             {ambiguous: true, kind: KindPerson},
	ClassPartialStructure:    {ambiguous: true, kind: KindPerson},
	ClassNicknameWithName:    {ambiguous: true, kind: KindPerson},
	ClassCaseDefect:          {ambiguous: true, kind: KindPerson},
}

// DecisionClasses returns the closed set of decision classes in precedence
// order.
func DecisionClasses() []DecisionClass {
	return append([]DecisionClass(nil), decisionClassOrder...)
}

// Validate reports ErrUnknownDecisionClass for a value outside the closed set.
func (c DecisionClass) Validate() error {
	if _, ok := decisionClassRouting[c]; !ok {
		return ErrUnknownDecisionClass
	}
	return nil
}

// Ambiguous reports whether the class names a form the normalizer refuses to
// guess about; such a result is only ever a proposal for review. An unknown
// class fails closed as ambiguous.
func (c DecisionClass) Ambiguous() bool {
	routing, ok := decisionClassRouting[c]
	return !ok || routing.ambiguous
}

// Kind returns the kind a result of this class names, or "" for an unknown
// class.
func (c DecisionClass) Kind() Kind { return decisionClassRouting[c].kind }

// Status returns the normalizer status a local result of this class carries:
// invalid for malformed input, unresolved for an ambiguous class, normalized
// otherwise. An unknown class returns "".
func (c DecisionClass) Status() Status {
	routing, ok := decisionClassRouting[c]
	switch {
	case !ok:
		return ""
	case routing.kind == KindMalformed:
		return StatusInvalid
	case routing.ambiguous:
		return StatusUnresolved
	default:
		return StatusNormalized
	}
}

// selectable reports whether an acceptance policy may register the class:
// only non-ambiguous classes of well-formed input.
func (c DecisionClass) selectable() bool {
	return c.Validate() == nil && !c.Ambiguous() && c.Kind() != KindMalformed
}

// QualityFlag is one observed property of the source form. Flags never
// change selection by themselves; they make every property visible even when
// a higher-precedence class names the result.
type QualityFlag string

// Closed set of quality flags, in their canonical output order.
const (
	FlagDuplicateComponent   QualityFlag = "duplicate_component"
	FlagMixedScript          QualityFlag = "mixed_script"
	FlagUnusualCharacters    QualityFlag = "unusual_characters"
	FlagInitials             QualityFlag = "initials"
	FlagParticle             QualityFlag = "particle"
	FlagAffix                QualityFlag = "affix"
	FlagUnstructured         QualityFlag = "unstructured"
	FlagSingleToken          QualityFlag = "single_token"
	FlagPartialStructure     QualityFlag = "partial_structure"
	FlagNickname             QualityFlag = "nickname"
	FlagAllCaps              QualityFlag = "all_caps"
	FlagLowercase            QualityFlag = "lowercase"
	FlagCollectiveMarker     QualityFlag = "collective_marker"
	FlagPlaceholder          QualityFlag = "placeholder"
	FlagPlaceholderFragment  QualityFlag = "placeholder_fragment"
	FlagNoLetters            QualityFlag = "no_letters"
	FlagReplacementCharacter QualityFlag = "replacement_character"
	FlagHyphen               QualityFlag = "hyphen"
	FlagApostrophe           QualityFlag = "apostrophe"
	FlagWhitespaceCollapsed  QualityFlag = "whitespace_collapsed"
	FlagEmptyComponent       QualityFlag = "empty_component"
)

var qualityFlagOrder = []QualityFlag{
	FlagDuplicateComponent, FlagMixedScript, FlagUnusualCharacters, FlagInitials,
	FlagParticle, FlagAffix, FlagUnstructured, FlagSingleToken, FlagPartialStructure,
	FlagNickname, FlagAllCaps, FlagLowercase, FlagCollectiveMarker, FlagPlaceholder,
	FlagPlaceholderFragment, FlagNoLetters, FlagReplacementCharacter, FlagHyphen,
	FlagApostrophe, FlagWhitespaceCollapsed, FlagEmptyComponent,
}

var knownQualityFlags = func() map[QualityFlag]struct{} {
	known := make(map[QualityFlag]struct{}, len(qualityFlagOrder))
	for _, f := range qualityFlagOrder {
		known[f] = struct{}{}
	}
	return known
}()

// structuralAmbiguityFlags mark a source whose every reading is a guess: a
// broken or partial structure, mixed scripts, stray characters, initials,
// particles or affixes. A local result carrying any of them never gets a
// selectable class, whatever vocabulary it also matches — an exact
// placeholder display does not outrank a duplicate component. Flags about
// field layout (unstructured, single_token), case and plain punctuation are
// not structural ambiguity: a placeholder names no one in particular, so its
// layout and case carry no identity to guess.
var structuralAmbiguityFlags = []QualityFlag{
	FlagDuplicateComponent, FlagMixedScript, FlagUnusualCharacters, FlagInitials,
	FlagParticle, FlagAffix, FlagPartialStructure,
}

func hasStructuralAmbiguity(has func(QualityFlag) bool) bool {
	for _, f := range structuralAmbiguityFlags {
		if has(f) {
			return true
		}
	}
	return false
}

// QualityFlags returns the closed set of quality flags in canonical order.
func QualityFlags() []QualityFlag {
	return append([]QualityFlag(nil), qualityFlagOrder...)
}

// Validate reports ErrUnknownQualityFlag for a value outside the closed set.
func (f QualityFlag) Validate() error {
	if _, ok := knownQualityFlags[f]; !ok {
		return ErrUnknownQualityFlag
	}
	return nil
}

// Result is one lexical normalization of one normalization key. It is not a
// person and not a canonical author. Empty strings mean "not filled": the
// normalizer never invents a field the source did not carry.
type Result struct {
	GivenName       string
	AdditionalNames string
	FamilyName      string
	Nickname        string
	Prefix          string
	Suffix          string

	DisplayName string
	SortName    string
	SearchKey   string

	Script        Script
	Kind          Kind
	Status        Status
	Method        Method
	DecisionClass DecisionClass
	QualityFlags  []QualityFlag

	SourceFingerprint [32]byte
	NormalizationKey  [32]byte
	ExtractorVersion  string
	NormalizerVersion string
	SchemaVersion     int
}

// Validate checks versions, every closed enum and, for local results, that
// kind and status agree with the decision class routing and that a
// selectable class carries no structural ambiguity flag.
func (r *Result) Validate() error {
	if r.SchemaVersion <= 0 {
		return ErrInvalidSchemaVersion
	}
	if strings.TrimSpace(r.ExtractorVersion) == "" || strings.TrimSpace(r.NormalizerVersion) == "" {
		return ErrEmptyVersion
	}
	if r.SourceFingerprint == ([32]byte{}) || r.NormalizationKey == ([32]byte{}) {
		return ErrMissingFingerprint
	}
	for _, check := range []func() error{
		r.Kind.Validate, r.Status.Validate, r.Method.Validate, r.Script.Validate, r.DecisionClass.Validate,
	} {
		if err := check(); err != nil {
			return err
		}
	}
	for _, f := range r.QualityFlags {
		if err := f.Validate(); err != nil {
			return err
		}
	}
	if !r.Method.local() {
		return nil
	}
	if r.Kind != r.DecisionClass.Kind() || r.Status != r.DecisionClass.Status() {
		return ErrInconsistentResult
	}
	if r.DecisionClass.selectable() && hasStructuralAmbiguity(func(f QualityFlag) bool { return slices.Contains(r.QualityFlags, f) }) {
		return ErrInconsistentResult
	}
	return nil
}
