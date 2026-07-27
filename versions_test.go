package assets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These tests run from the repository root, so every path below is repo-relative.
const (
	goModPath      = "go.mod"
	dockerfilePath = "Dockerfile"
	makefilePath   = "Makefile"
	workflowsDir   = ".github/workflows"
	readmePath     = "README.md"
	frontendDir    = "booksdump-frontend"
	golangciPath   = ".golangci.yml"
)

// unpinnedTags are tags that move under you, so an image built today and an
// image built tomorrow are not the same image.
var unpinnedTags = map[string]bool{
	"latest":  true,
	"main":    true,
	"master":  true,
	"nightly": true,
	"edge":    true,
	"stable":  true,
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// goModVersions returns the "go" directive (1.24.0) and the "toolchain"
// directive without its "go" prefix (1.24.11).
func goModVersions(t *testing.T) (goDirective, toolchain string) {
	t.Helper()
	body := readRepoFile(t, goModPath)

	if m := regexp.MustCompile(`(?m)^go\s+(\S+)`).FindStringSubmatch(body); m != nil {
		goDirective = m[1]
	}
	if m := regexp.MustCompile(`(?m)^toolchain\s+go(\S+)`).FindStringSubmatch(body); m != nil {
		toolchain = m[1]
	}

	if goDirective == "" {
		t.Fatal("go.mod has no go directive")
	}
	if toolchain == "" {
		t.Fatal("go.mod has no toolchain directive")
	}
	return goDirective, toolchain
}

// majorMinor reduces 1.24.11 to 1.24.
func majorMinor(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

// dockerfileImages returns every image reference used in a FROM instruction.
func dockerfileImages(t *testing.T) []string {
	t.Helper()

	var images []string
	re := regexp.MustCompile(`(?mi)^FROM\s+(\S+)`)
	for _, m := range re.FindAllStringSubmatch(readRepoFile(t, dockerfilePath), -1) {
		images = append(images, m[1])
	}
	if len(images) == 0 {
		t.Fatal("Dockerfile has no FROM instructions")
	}
	return images
}

func workflowFiles(t *testing.T) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		t.Fatalf("reading %s: %v", workflowsDir, err)
	}

	files := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		path := filepath.Join(workflowsDir, entry.Name())
		files[path] = readRepoFile(t, path)
	}
	if len(files) == 0 {
		t.Fatalf("no workflow files found in %s", workflowsDir)
	}
	return files
}

// composeAndServiceImages returns every "image:" reference from docker-compose
// and from the service containers CI starts, keyed by where it was found.
func composeAndServiceImages(t *testing.T) map[string][]string {
	t.Helper()

	sources := []string{"docker-compose.yml"}
	for path := range workflowFiles(t) {
		sources = append(sources, path)
	}

	re := regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`)
	found := make(map[string][]string)
	for _, path := range sources {
		for _, m := range re.FindAllStringSubmatch(readRepoFile(t, path), -1) {
			found[path] = append(found[path], m[1])
		}
	}
	return found
}

// releaseDepth is how many numeric components a tag needs before it stops moving,
// which differs per vendor because that is where each one cuts releases:
// alpine:3.20 is a branch but postgres:15.18 is a release. Add an entry when
// introducing an image whose scheme is shallower than the default.
var releaseDepth = map[string]int{
	"postgres": 2,
}

const defaultReleaseDepth = 3

// leadingVersion extracts the numeric prefix of a tag: 1.24.11-alpine3.21 -> 1.24.11,
// 7.4.10-alpine -> 7.4.10, 15.18-alpine -> 15.18.
var leadingVersion = regexp.MustCompile(`^(\d+(?:\.\d+)*)`)

// assertPinnedImage reports an image reference that a rebuild could resolve to
// different bits.
func assertPinnedImage(t *testing.T, where, image string) {
	t.Helper()

	name, tag, found := strings.Cut(image, ":")
	if !found {
		t.Errorf("%s: %s has no tag at all", where, image)
		return
	}
	if unpinnedTags[tag] {
		t.Errorf("%s: %s uses the moving tag %q", where, image, tag)
		return
	}

	// The name may carry a registry or a FROM prefix; only the last path element
	// selects the versioning scheme.
	shortName := name[strings.LastIndex(name, "/")+1:]
	shortName = shortName[strings.LastIndex(shortName, " ")+1:]

	want := defaultReleaseDepth
	if depth, ok := releaseDepth[shortName]; ok {
		want = depth
	}

	version := leadingVersion.FindString(tag)
	if version == "" {
		t.Errorf("%s: %s has tag %q, which does not start with a version", where, image, tag)
		return
	}
	if got := len(strings.Split(version, ".")); got < want {
		t.Errorf("%s: %s has tag %q with a %d-component version, but %s releases are pinned at %d components",
			where, image, tag, got, shortName, want)
	}
}

// TestDockerImagesArePinned guards that a rebuild produces the same base images.
func TestDockerImagesArePinned(t *testing.T) {
	for _, image := range dockerfileImages(t) {
		assertPinnedImage(t, dockerfilePath, "FROM "+image)
	}
}

// TestComposeAndCIServiceImagesArePinned covers the images this repository runs
// rather than builds: local docker-compose services and CI service containers.
func TestComposeAndCIServiceImagesArePinned(t *testing.T) {
	for path, images := range composeAndServiceImages(t) {
		for _, image := range images {
			assertPinnedImage(t, path, image)
		}
	}
}

// TestDockerfileGoImageMatchesGoMod guards that the container builds with the
// toolchain go.mod asks for, instead of silently downloading another one.
func TestDockerfileGoImageMatchesGoMod(t *testing.T) {
	_, toolchain := goModVersions(t)

	var found bool
	for _, image := range dockerfileImages(t) {
		if !strings.HasPrefix(image, "golang:") {
			continue
		}
		found = true

		tag := strings.TrimPrefix(image, "golang:")
		if !strings.HasPrefix(tag, toolchain) {
			t.Errorf("Dockerfile builds with golang:%s, but go.mod pins toolchain go%s", tag, toolchain)
		}
	}
	if !found {
		t.Fatal("Dockerfile has no golang base image")
	}
}

// TestCIGoVersionMatchesGoMod guards that CI does not run on an older Go than
// the module requires.
func TestCIGoVersionMatchesGoMod(t *testing.T) {
	goDirective, _ := goModVersions(t)
	want := majorMinor(goDirective)

	re := regexp.MustCompile(`go-version:\s*'?"?([0-9.]+)'?"?`)
	for path, body := range workflowFiles(t) {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			if got := m[1]; got != want {
				t.Errorf("%s sets go-version %q, but go.mod requires %q", path, got, want)
			}
		}
	}
}

// TestCIActionsAndToolsArePinned guards that workflow steps cannot change under
// the repository. Publishing a moving tag (type=raw,value=latest) is a different
// thing and is deliberately not matched here.
func TestCIActionsAndToolsArePinned(t *testing.T) {
	checks := []struct {
		what string
		re   *regexp.Regexp
	}{
		{"action pinned to a moving branch", regexp.MustCompile(`uses:\s*\S+@(master|main)\b`)},
		{"tool installed at @latest", regexp.MustCompile(`@latest\b`)},
		{"action input version: latest", regexp.MustCompile(`version:\s*'?"?latest'?"?`)},
	}

	for path, body := range workflowFiles(t) {
		for lineNum, line := range strings.Split(body, "\n") {
			for _, check := range checks {
				if check.re.MatchString(line) {
					t.Errorf("%s:%d %s: %s", path, lineNum+1, check.what, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestNodeVersionMatchesBetweenDockerfileAndCI guards that the frontend is built
// and tested on the same Node major everywhere.
func TestNodeVersionMatchesBetweenDockerfileAndCI(t *testing.T) {
	var want string
	for _, image := range dockerfileImages(t) {
		if !strings.HasPrefix(image, "node:") {
			continue
		}
		want = strings.Split(strings.TrimPrefix(image, "node:"), ".")[0]
	}
	if want == "" {
		t.Fatal("Dockerfile has no node base image")
	}

	re := regexp.MustCompile(`node-version:\s*'?"?(\d+)`)
	var found bool
	for path, body := range workflowFiles(t) {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			found = true
			if got := m[1]; got != want {
				t.Errorf("%s builds on Node %s, but the Dockerfile uses Node %s", path, got, want)
			}
		}
	}
	if !found {
		t.Fatal("no workflow sets node-version")
	}
}

// TestGoimportsLocalPrefixMatchesModulePath guards that the import grouping rule
// actually matches this module's imports instead of silently applying to nothing.
func TestGoimportsLocalPrefixMatchesModulePath(t *testing.T) {
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(readRepoFile(t, goModPath))
	if m == nil {
		t.Fatal("go.mod has no module directive")
	}
	want := m[1]

	p := regexp.MustCompile(`local-prefixes:\s*\n\s*-\s*(\S+)`).
		FindStringSubmatch(readRepoFile(t, golangciPath))
	if p == nil {
		t.Fatal(".golangci.yml does not configure goimports local-prefixes")
	}

	if got := p[1]; got != want {
		t.Errorf("goimports local-prefixes is %q, but the module is %q", got, want)
	}
}

// TestReadmeGoVersionMatchesGoMod guards that the README does not send people
// after a Go version the module cannot build with.
func TestReadmeGoVersionMatchesGoMod(t *testing.T) {
	goDirective, _ := goModVersions(t)
	want := majorMinor(goDirective)

	re := regexp.MustCompile(`Go\s+(\d+\.\d+)`)
	matches := re.FindAllStringSubmatch(readRepoFile(t, readmePath), -1)
	if len(matches) == 0 {
		t.Fatal("README mentions no Go version at all")
	}

	for _, m := range matches {
		if got := m[1]; got != want {
			t.Errorf("README mentions Go %s, but go.mod requires %s", got, want)
		}
	}
}

// TestReadmeReactVersionMatchesPackageJSON guards the same for the frontend.
func TestReadmeReactVersionMatchesPackageJSON(t *testing.T) {
	pkg := readRepoFile(t, filepath.Join(frontendDir, "package.json"))

	m := regexp.MustCompile(`"react":\s*"\D*(\d+)`).FindStringSubmatch(pkg)
	if m == nil {
		t.Fatal("package.json does not depend on react")
	}
	want := m[1]

	re := regexp.MustCompile(`React\s+(\d+)`)
	matches := re.FindAllStringSubmatch(readRepoFile(t, readmePath), -1)
	if len(matches) == 0 {
		t.Fatal("README mentions no React version at all")
	}

	for _, match := range matches {
		if got := match[1]; got != want {
			t.Errorf("README mentions React %s, but package.json depends on React %s", got, want)
		}
	}
}

// TestReadmeUsesTheProjectPackageManager guards against instructions that would
// desynchronise yarn.lock, which is the lockfile this repository actually has.
func TestReadmeUsesTheProjectPackageManager(t *testing.T) {
	if _, err := os.Stat(filepath.Join(frontendDir, "yarn.lock")); err != nil {
		t.Fatalf("expected a yarn lockfile: %v", err)
	}

	readme := readRepoFile(t, readmePath)
	for lineNum, line := range strings.Split(readme, "\n") {
		if regexp.MustCompile(`\bnpm\s+(install|run|start|ci)\b`).MatchString(line) {
			t.Errorf("README:%d tells the reader to use npm, but the project uses yarn: %s",
				lineNum+1, strings.TrimSpace(line))
		}
	}
}

// TestSwagVersionMatchesBetweenMakefileAndDockerfile guards that a local
// `make swagger` and the container build generate docs with the same generator.
func TestSwagVersionMatchesBetweenMakefileAndDockerfile(t *testing.T) {
	makeRe := regexp.MustCompile(`SWAG_VERSION\s*:?=\s*(\S+)`)
	m := makeRe.FindStringSubmatch(readRepoFile(t, makefilePath))
	if m == nil {
		t.Fatal("Makefile does not define SWAG_VERSION")
	}
	want := m[1]

	dockerRe := regexp.MustCompile(`swaggo/swag/cmd/swag@(\S+)`)
	d := dockerRe.FindStringSubmatch(readRepoFile(t, dockerfilePath))
	if d == nil {
		t.Fatal("Dockerfile does not install swaggo/swag")
	}

	if got := d[1]; got != want {
		t.Errorf("Dockerfile installs swag@%s, Makefile pins SWAG_VERSION=%s", got, want)
	}
}
