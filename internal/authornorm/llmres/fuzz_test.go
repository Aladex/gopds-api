package llmres

import (
	"strings"
	"testing"
	"unicode/utf8"

	"gopds-api/internal/authornorm"
)

// sourceOf builds the canonical source value of a field list, mapping the
// request fields back to FB2 component kinds.
func sourceOf(fields []FieldValue) (authornorm.SourceValue, error) {
	kinds := map[Field]authornorm.ComponentKind{
		FieldFirst:    authornorm.ComponentFirst,
		FieldMiddle:   authornorm.ComponentMiddle,
		FieldLast:     authornorm.ComponentLast,
		FieldNickname: authornorm.ComponentNickname,
	}
	var comps []authornorm.SourceComponent
	for _, f := range fields {
		kind, ok := kinds[f.Field]
		if !ok {
			continue
		}
		comps = append(comps, authornorm.SourceComponent{Kind: kind, Value: f.Text})
	}
	return authornorm.NewSourceValue(comps)
}

// FuzzV2RoleCoverage fuzzes the coverage validator against an independent
// reference: roles cover every input index exactly once.
func FuzzV2RoleCoverage(f *testing.F) {
	f.Add(uint8(3), []byte{0, 1, 2})
	f.Add(uint8(1), []byte{0, 0})
	f.Add(uint8(4), []byte{3, 2, 1, 0})
	f.Add(uint8(2), []byte{5, 0})
	f.Fuzz(func(t *testing.T, n uint8, roleIdx []byte) {
		n = n%16 + 1
		in := inputOf(t, FieldValue{FieldFirst, strings.Repeat("слово ", int(n))})
		var roles []RoleAssignment
		for _, b := range roleIdx {
			roles = append(roles, role(int(b)%20, RoleGiven))
		}
		rep := personReply(roles...)
		err := rep.checkV2(&in)

		// Reference: every index 0..n-1 covered exactly once.
		seen := make(map[int]int)
		for _, r := range roles {
			seen[r.I]++
		}
		wantOK := true
		for i := 0; i < int(n); i++ {
			if seen[i] != 1 {
				wantOK = false
			}
		}
		for i := range seen {
			if i < 0 || i >= int(n) {
				wantOK = false
			}
		}
		if (err == nil) != wantOK {
			t.Fatalf("checkV2(n=%d, roles=%v) = %v, reference says ok=%v", n, roleIdx, err, wantOK)
		}
	})
}

// FuzzV7TokenInvariant fuzzes the script invariant: reflexive on any token,
// and a letter from another script smuggled in must always be caught.
func FuzzV7TokenInvariant(f *testing.F) {
	f.Add("Иван")
	f.Add("Hиколай")
	f.Add("Franck")
	f.Add("")
	f.Add("東京")
	f.Fuzz(func(t *testing.T, src string) {
		if err := CheckTokenInvariant(src, src); err != nil {
			t.Fatalf("identity must pass: CheckTokenInvariant(%q, %q) = %v", src, src, err)
		}
		// Appending a letter changes the rune count and must always be caught.
		if err := CheckTokenInvariant(src, src+"а"); err == nil {
			t.Fatalf("appended letter must fail: %q -> %q", src, src+"а")
		}
		// A cross-script substitution mid-token must be caught unless it is a
		// declared homoglyph pair applied under the repair rule.
		for i, r := range src {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				mut := src[:i] + "Ж" + src[i+utf8.RuneLen(r):]
				if CheckTokenInvariant(src, mut) == nil {
					// Admissible only when Ж is the homoglyph pair of r — it never is.
					t.Fatalf("cross-script substitution %q -> %q passed", src, mut)
				}
				return
			}
		}
	})
}

// FuzzBuildContext feeds arbitrary bytes to the excerpt reader: it must never
// panic, successful reads must be deterministic and within the caps.
func FuzzBuildContext(f *testing.F) {
	f.Add([]byte(testFB2))
	f.Add([]byte(creditHeavyFB2())) // credits overflow the body budget
	f.Add([]byte(`<FictionBook><body><p>x</p></body></FictionBook>`))
	f.Add([]byte{0xFF, 0xFE, 0x00, 0x00})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		ctx, err := BuildContext(data, "")
		if err != nil {
			return
		}
		ctx2, err := BuildContext(data, "")
		if err != nil {
			t.Fatalf("second read of the same bytes failed: %v", err)
		}
		if ctx.SHA256() != ctx2.SHA256() {
			t.Fatal("SHA-256 must be deterministic for the same bytes")
		}
		if runeLen(ctx.Title) > TitleMaxRunes ||
			runeLen(ctx.Annotation) > AnnotationMaxRunes ||
			runeLen(ctx.PublishInfo) > PublishInfoMaxRunes ||
			runeLen(ctx.BodyStart) > BodyStartMaxRunes ||
			ctx.TotalLen() > TotalMaxRunes {
			t.Fatalf("caps exceeded: %+v", ctx)
		}
		// The excerpt must be valid UTF-8: it is serialized into a JSON request.
		for _, s := range []string{ctx.Title, ctx.Annotation, ctx.PublishInfo, ctx.BodyStart} {
			if !utf8.ValidString(s) {
				t.Fatal("excerpt field is not valid UTF-8")
			}
		}
	})
}
