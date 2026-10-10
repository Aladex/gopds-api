package llmres

import (
	"strings"
	"testing"

	"gopds-api/internal/authornorm"
)

// Tokenizer examples follow the request contract of design doc section 2:
// whitespace separates words, punctuation at word edges becomes its own
// token, brackets are separate tokens, and an initial's dot stays attached.

func tok(i int, text string, field Field) Token { return Token{I: i, Text: text, Field: field} }

func TestTokenize(t *testing.T) {
	tests := []struct {
		name   string
		fields []FieldValue
		want   []Token
	}{
		{
			name:   "comma at word edge splits off",
			fields: []FieldValue{{FieldFirst, "Thilliez,"}, {FieldLast, "Franck"}},
			want:   []Token{tok(0, "Thilliez", FieldFirst), tok(1, ",", FieldFirst), tok(2, "Franck", FieldLast)},
		},
		{
			name:   "brackets are separate tokens",
			fields: []FieldValue{{FieldFirst, "(Сан-Антонио)"}},
			want:   []Token{tok(0, "(", FieldFirst), tok(1, "Сан-Антонио", FieldFirst), tok(2, ")", FieldFirst)},
		},
		{
			name:   "initial dots stay attached",
			fields: []FieldValue{{FieldFirst, "Л."}, {FieldMiddle, "А."}, {FieldLast, "Калугина"}},
			want:   []Token{tok(0, "Л.", FieldFirst), tok(1, "А.", FieldMiddle), tok(2, "Калугина", FieldLast)},
		},
		{
			name:   "compound initial stays whole",
			fields: []FieldValue{{FieldFirst, "Дж.Р.Р. Толкин"}},
			want:   []Token{tok(0, "Дж.Р.Р.", FieldFirst), tok(1, "Толкин", FieldFirst)},
		},
		{
			name:   "hyphenated initial stays whole",
			fields: []FieldValue{{FieldFirst, "Ж.-П."}},
			want:   []Token{tok(0, "Ж.-П.", FieldFirst)},
		},
		{
			name:   "stray dot after a full word splits off",
			fields: []FieldValue{{FieldLast, "Иванов."}},
			want:   []Token{tok(0, "Иванов", FieldLast), tok(1, ".", FieldLast)},
		},
		{
			name:   "punctuation-only word is one token",
			fields: []FieldValue{{FieldFirst, "..."}},
			want:   []Token{tok(0, "...", FieldFirst)},
		},
		{
			name:   "guillemets split as brackets",
			fields: []FieldValue{{FieldFirst, "«Анна»"}},
			want:   []Token{tok(0, "«", FieldFirst), tok(1, "Анна", FieldFirst), tok(2, "»", FieldFirst)},
		},
		{
			name:   "leading punctuation run groups",
			fields: []FieldValue{{FieldFirst, "...Иван"}},
			want:   []Token{tok(0, "...", FieldFirst), tok(1, "Иван", FieldFirst)},
		},
		{
			name:   "several words in one field",
			fields: []FieldValue{{FieldFirst, "Chuck Hogan, Guillermo del Toro"}},
			want: []Token{
				tok(0, "Chuck", FieldFirst), tok(1, "Hogan", FieldFirst), tok(2, ",", FieldFirst),
				tok(3, "Guillermo", FieldFirst), tok(4, "del", FieldFirst), tok(5, "Toro", FieldFirst),
			},
		},
		{
			name:   "empty fields contribute nothing",
			fields: []FieldValue{{FieldFirst, ""}, {FieldLast, "Иванов"}},
			want:   []Token{tok(0, "Иванов", FieldLast)},
		},
		{
			name:   "collapsed whitespace inside a field",
			fields: []FieldValue{{FieldFirst, "Лу  Синь"}},
			want:   []Token{tok(0, "Лу", FieldFirst), tok(1, "Синь", FieldFirst)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.fields)
			if len(got) != len(tt.want) {
				t.Fatalf("Tokenize(%v) = %v, want %v", tt.fields, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("token %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFieldValuesOfRecoversDocumentOrder(t *testing.T) {
	// The XML order of name-bearing children is not stored on SourceValue, but
	// the display name is their join in that order, so the order is
	// recoverable.
	v, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
		{Kind: authornorm.ComponentLast, Value: "Иванов"},
		{Kind: authornorm.ComponentFirst, Value: "Иван"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := FieldValuesOf(v)
	want := []FieldValue{{FieldLast, "Иванов"}, {FieldFirst, "Иван"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("FieldValuesOf = %v, want %v", got, want)
	}

	// An absent field contributes nothing.
	v2, err := authornorm.NewSourceValue([]authornorm.SourceComponent{
		{Kind: authornorm.ComponentFirst, Value: "Иван"},
		{Kind: authornorm.ComponentLast, Value: "Иванов"},
		{Kind: authornorm.ComponentNickname, Value: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	got2 := FieldValuesOf(v2)
	if len(got2) != 2 || got2[0].Field != FieldFirst || got2[1].Field != FieldLast {
		t.Fatalf("FieldValuesOf = %v", got2)
	}
}

func TestCheckLimits(t *testing.T) {
	fit := Tokenize([]FieldValue{{FieldFirst, "Л. А. Калугина"}})
	if err := CheckLimits(fit); err != nil {
		t.Fatalf("CheckLimits(%v) = %v", fit, err)
	}

	// Seventeen tokens exceed the sixteen-token cap.
	var many []FieldValue
	many = append(many, FieldValue{FieldFirst, strings.Repeat("а ", 17)})
	if err := CheckLimits(Tokenize(many)); err != ErrInputTooLong {
		t.Fatalf("17 tokens: CheckLimits = %v, want ErrInputTooLong", err)
	}

	// A single 201-code-point token exceeds the code-point cap.
	long := Tokenize([]FieldValue{{FieldFirst, strings.Repeat("а", 201)}})
	if err := CheckLimits(long); err != ErrInputTooLong {
		t.Fatalf("201 code points: CheckLimits = %v, want ErrInputTooLong", err)
	}
}
