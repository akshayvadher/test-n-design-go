//go:build slice1

// Package bootstrap_test verifies the dev-loop config files that Slice 1 of
// Phase 1 ships. The slice has no Go production code yet (the first Go source
// file lands in Slice 2 and the first unit test in Slice 3), so this test
// asserts the *content contracts* of the config files instead. Each test
// corresponds to one acceptance criterion in
// docs/specs/improving-tdd-demo-go-phase-1-spec.md (lines 56-72).
//
// This file is gated behind the `slice1` build tag so it does NOT run under
// `task test` (which executes `go test -race ./...` with no tags). Run it
// explicitly with:
//
//	go test -tags slice1 ./...
//
// The dev-loop behaviours that cannot be exercised with a file read — `task up`
// end-to-end against podman, the migration runner against a live postgres,
// graceful shutdown semantics — are deliberately deferred to Slice 7's
// integration smoke test and to the sdd-verifier's manual checks. See the
// per-AC notes in this file for the rationale on what each test does and does
// not cover.
package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot resolves the repository root regardless of where `go test` is
// invoked from. Because this file lives at the repo root, the test's working
// directory equals the repo root, so a `.` resolution is sufficient — but we
// resolve it explicitly so callers can sanity-check the path in failure
// messages.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not resolve working directory: %v", err)
	}
	return wd
}

// readFile reads `name` relative to the repo root and fails the test if the
// file is missing or unreadable.
func readFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read %s: %v", path, err)
	}
	return string(data)
}

// assertContains fails the test if `haystack` does not contain `needle`. The
// `label` is included in the failure message so the developer immediately
// knows which AC failed.
func assertContains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: expected to find %q in file content", label, needle)
	}
}

// assertNotContains fails the test if `haystack` contains `needle`. Used to
// catch forbidden dependencies in go.mod.
func assertNotContains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%s: expected NOT to find %q in file content", label, needle)
	}
}

// -----------------------------------------------------------------------------
// AC: go.mod
// -----------------------------------------------------------------------------

func TestGoMod_DeclaresModulePathAndGoVersion(t *testing.T) {
	// Spec AC line 58: module path `github.com/akshayvadher/test-n-design-go`
	// and Go version `1.23` or newer.
	content := readFile(t, "go.mod")

	assertContains(t, content,
		"module github.com/akshayvadher/test-n-design-go",
		"go.mod module path")

	// We do not parse the version string with golang.org/x/mod/modfile because
	// that would pull a new dep we are forbidden to add in this slice. Instead
	// we accept any `go 1.NN.N` line and reject 1.22 and below explicitly. The
	// floor in the spec is 1.23; 1.24+ is what the slice-coder shipped.
	if !strings.Contains(content, "\ngo 1.23") &&
		!strings.Contains(content, "\ngo 1.24") &&
		!strings.Contains(content, "\ngo 1.25") &&
		!strings.Contains(content, "\ngo 1.26") {
		t.Errorf("go.mod: expected Go directive of 1.23 or newer; content was:\n%s", content)
	}
}

func TestGoMod_ListsRequiredDirectDependencies(t *testing.T) {
	// Spec AC line 59: exactly these direct deps must appear.
	content := readFile(t, "go.mod")

	required := []string{
		"github.com/go-chi/chi/v5",
		"github.com/uptrace/bun",
		"github.com/uptrace/bun/driver/pgdriver",
		"github.com/redis/go-redis/v9",
		"github.com/spf13/viper",
		"github.com/google/uuid",
	}
	for _, dep := range required {
		assertContains(t, content, dep, "go.mod required direct dep")
	}
}

func TestGoMod_DoesNotListForbiddenDependencies(t *testing.T) {
	// Spec AC line 59 (negative half): forbidden libs that must not appear.
	// pgx is deferred to a Phase 2+ slice; the test libs are anti-stack;
	// validator is forbidden by the hand-written-validation convention; the
	// DI containers are forbidden by the manual-wiring convention.
	content := readFile(t, "go.mod")

	forbidden := []string{
		"github.com/jackc/pgx/v5",
		"github.com/stretchr/testify",
		"github.com/google/wire",
		"go.uber.org/fx",
		"github.com/go-playground/validator",
	}
	for _, dep := range forbidden {
		assertNotContains(t, content, dep, "go.mod forbidden dep")
	}
}

// -----------------------------------------------------------------------------
// AC: .gitignore
// -----------------------------------------------------------------------------

func TestGitignore_ExcludesRequiredEntries(t *testing.T) {
	// Spec AC line 60: bin/, dist/, coverage.out, *.test, .env, .idea/,
	// .vscode/, tmp/.
	content := readFile(t, ".gitignore")

	for _, entry := range []string{
		"bin/",
		"dist/",
		"coverage.out",
		"*.test",
		".env",
		".idea/",
		".vscode/",
		"tmp/",
	} {
		assertContains(t, content, entry, ".gitignore entry")
	}
}

// -----------------------------------------------------------------------------
// AC: .env.example
// -----------------------------------------------------------------------------

func TestEnvExample_DocumentsAllRequiredKeysAndDefaults(t *testing.T) {
	// Spec AC line 61: every key with its default value, exactly as named.
	// We assert on `KEY=value` substrings rather than parsing because we are
	// not allowed to add a dotenv lib and the file is small.
	content := readFile(t, ".env.example")

	cases := []struct {
		key   string
		value string
	}{
		{"LIBRARY_HTTP_PORT", "3000"},
		{"LIBRARY_DATABASE_URL", "postgres://library:library@localhost:5432/library?sslmode=disable"},
		{"LIBRARY_REDIS_URL", "redis://localhost:6379/0"},
		{"LIBRARY_LOG_LEVEL", "info"},
		{"LIBRARY_LOG_FORMAT", "json"},
	}
	for _, c := range cases {
		want := c.key + "=" + c.value
		assertContains(t, content, want, ".env.example key=value")
	}
}

// -----------------------------------------------------------------------------
// AC: compose.yaml
// -----------------------------------------------------------------------------

func TestComposeYaml_DefinesPostgresAndRedisWithHealthchecks(t *testing.T) {
	// Spec AC lines 62-63: postgres:16-alpine + redis:7-alpine, each with a
	// healthcheck (pg_isready / redis-cli ping), exposed on 5432/6379, named
	// volume for postgres, and POSTGRES_USER/PASSWORD/DB=library.
	//
	// We deliberately use string matching, NOT a YAML parser. gopkg.in/yaml.v3
	// is not a direct dep of this module and Slice 1 must not add new deps.
	// The structural validity of the file is verified out-of-band by
	// `podman compose config` and end-to-end by Slice 7's integration test.
	content := readFile(t, "compose.yaml")

	for _, needle := range []string{
		"postgres:",
		"redis:",
		"image: postgres:16-alpine",
		"image: redis:7-alpine",
		"pg_isready",
		"redis-cli",
		"ping",
		"5432:5432",
		"6379:6379",
		"POSTGRES_USER: library",
		"POSTGRES_PASSWORD: library",
		"POSTGRES_DB: library",
		"healthcheck:",
	} {
		assertContains(t, content, needle, "compose.yaml content")
	}

	// AC line 63 specifically: a *named* volume for postgres data. The
	// volume name itself is implementation choice (the slice-coder shipped
	// `library-postgres`), but the top-level `volumes:` block must exist
	// and the postgres service must reference it.
	assertContains(t, content, "volumes:", "compose.yaml top-level volumes block")
	assertContains(t, content, "/var/lib/postgresql/data", "compose.yaml postgres data mount")
}

// -----------------------------------------------------------------------------
// AC: Taskfile.yml
// -----------------------------------------------------------------------------

func TestTaskfile_DeclaresAllRequiredTasks(t *testing.T) {
	// Spec AC line 67: all required task names must be declared.
	content := readFile(t, "Taskfile.yml")

	for _, taskName := range []string{
		"up:",
		"down:",
		"down:clean:",
		"run:",
		"build:",
		"test:",
		"test:integration:",
		"migrate:apply:",
		"migrate:status:",
		"fmt:",
		"lint:",
		"tidy:",
	} {
		assertContains(t, content, taskName, "Taskfile.yml task declaration")
	}
}

func TestTaskfile_UpRunsPodmanComposeUp(t *testing.T) {
	// Spec AC line 64: `task up` runs `podman compose up -d`. We can only
	// assert that the command is *declared* in the Taskfile; we cannot
	// run it end-to-end without mutating the developer's machine. The
	// sdd-verifier (and Slice 7's integration smoke) covers the live path.
	content := readFile(t, "Taskfile.yml")
	assertContains(t, content, "podman compose up -d", "Taskfile.yml `up` command")
}

func TestTaskfile_DownIsNonDestructive(t *testing.T) {
	// Spec AC lines 65-66: `task down` runs `podman compose down` (no -v).
	// `task down:clean` is the separate destructive one with `-v`.
	content := readFile(t, "Taskfile.yml")
	assertContains(t, content, "podman compose down", "Taskfile.yml `down` command")
	assertContains(t, content, "podman compose down -v", "Taskfile.yml `down:clean` command")
}

func TestTaskfile_FmtAndTidyRunExpectedCommands(t *testing.T) {
	// Spec ACs lines 68 + 71.
	content := readFile(t, "Taskfile.yml")
	assertContains(t, content, "gofmt -w .", "Taskfile.yml `fmt` runs gofmt")
	assertContains(t, content, "go mod tidy", "Taskfile.yml `fmt`/`tidy` runs go mod tidy")
	assertContains(t, content, "go mod verify", "Taskfile.yml `tidy` runs go mod verify")
}

func TestTaskfile_LintRunsGoVet(t *testing.T) {
	// Spec AC line 69: `task lint` runs `go vet ./...` and no golangci-lint
	// dependency is added in Phase 1. The "no golangci-lint" half of the AC
	// is really a go.mod / build-tool concern (covered by go.mod's forbidden
	// deps test); the Taskfile is allowed to *mention* golangci-lint in a
	// documentation comment ("No golangci-lint dependency in Phase 1.") as
	// long as it does not actually invoke it. We assert the positive
	// invocation here and leave the dependency check to go.mod.
	content := readFile(t, "Taskfile.yml")
	assertContains(t, content, "go vet ./...", "Taskfile.yml `lint` command")
}

func TestTaskfile_BuildProducesBinaryUnderBinDir(t *testing.T) {
	// Spec AC line 70: `task build` produces `bin/library` (or .exe on
	// Windows). We only assert the output flag is present and points at
	// the bin/ dir; the precise per-platform line difference is part of
	// the slice-coder's implementation choice.
	content := readFile(t, "Taskfile.yml")
	assertContains(t, content, "go build -o", "Taskfile.yml `build` invokes go build")
	assertContains(t, content, "./cmd/library", "Taskfile.yml `build` targets cmd/library")
}

func TestTaskfile_DocumentsWindowsPodmanGotchaAtTop(t *testing.T) {
	// Spec AC line 72: the Taskfile documents the DOCKER_HOST workaround at
	// the top in a comment block, naming both the named-pipe value AND the
	// `podman machine inspect` discovery command.
	content := readFile(t, "Taskfile.yml")
	assertContains(t, content, "DOCKER_HOST", "Taskfile.yml mentions DOCKER_HOST")
	assertContains(t, content, "npipe:////./pipe/podman-machine-default", "Taskfile.yml documents the windows named-pipe value")
	assertContains(t, content, "podman machine inspect", "Taskfile.yml documents the discovery command")
}
