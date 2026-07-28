package safepath

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolveKeepsOrdinaryPathsInsideTheBase(t *testing.T) {
	base := filepath.FromSlash("/srv/posters")

	cases := map[string]string{
		"a plain name":                     "cover.png",
		"a router wildcard":                "/cover.png",
		"a nested file":                    "fb2-1-2/cover.png",
		"a nested router path":             "/fb2-1-2/cover.png",
		"an inner dot segment":             "fb2-1-2/./cover.png",
		"an inner climb that stays inside": "fb2-1-2/../fb2-3-4/cover.png",
	}

	for name, rel := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Resolve(base, rel)
			if err != nil {
				t.Fatalf("Resolve(%q, %q) refused a path inside the base: %v", base, rel, err)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("returned %q, which is not an absolute path", got)
			}
		})
	}
}

func TestResolveRefusesToLeaveTheBase(t *testing.T) {
	base := filepath.FromSlash("/srv/posters")

	cases := map[string]string{
		"a climb":                  "../secrets/key.pem",
		"a climb behind a name":    "covers/../../secrets/key.pem",
		"many climbs":              "../../../../etc/passwd",
		"an absolute-looking path": "/etc/passwd",
	}

	for name, rel := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Resolve(base, rel)
			// Either it refuses outright, or it anchors the path back inside
			// the base — what it must never do is hand back something outside.
			if err == nil {
				if !isInside(base, got) {
					t.Fatalf("Resolve(%q, %q) returned %q, outside the base", base, rel, got)
				}
				return
			}
			if !errors.Is(err, ErrEscapesBase) {
				t.Errorf("error %v does not wrap ErrEscapesBase", err)
			}
		})
	}
}

// The case the plain prefix check gets wrong. A sibling directory whose name
// starts with the base's name shares every character of that prefix, and is a
// different directory.
func TestResolveRefusesASiblingDirectoryWithASharedPrefix(t *testing.T) {
	base := filepath.FromSlash("/srv/posters")

	got, err := Resolve(base, "../posters-backup/private.png")
	if err == nil && !isInside(base, got) {
		t.Fatalf("Resolve returned %q: a sibling directory passed as if it were inside %q", got, base)
	}
}

func TestResolveTreatsTheBaseItselfAsInside(t *testing.T) {
	base := filepath.FromSlash("/srv/posters")

	for _, rel := range []string{"", "/", ".", "/."} {
		got, err := Resolve(base, rel)
		if err != nil {
			t.Errorf("Resolve(%q, %q) refused the base itself: %v", base, rel, err)
			continue
		}
		if got != base {
			t.Errorf("Resolve(%q, %q) = %q, want the base itself", base, rel, got)
		}
	}
}

func TestResolveCleansTheBase(t *testing.T) {
	got, err := Resolve(filepath.FromSlash("/srv/posters/"), "cover.png")
	if err != nil {
		t.Fatalf("a base with a trailing separator was refused: %v", err)
	}
	want := filepath.Join(filepath.FromSlash("/srv/posters"), "cover.png")
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// A relative base is what the configuration actually carries ("./posters/"),
// so it has to work too.
func TestResolveHandlesARelativeBase(t *testing.T) {
	got, err := Resolve("./posters/", "/fb2-1-2/cover.png")
	if err != nil {
		t.Fatalf("a relative base was refused: %v", err)
	}
	want := filepath.Join("posters", "fb2-1-2", "cover.png")
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func isInside(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !hasDotDotPrefix(rel)
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}
