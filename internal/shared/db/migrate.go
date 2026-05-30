package db

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sync"
)

// atlasInstallHint is the canonical install URL surfaced when the atlas CLI is
// missing from PATH. Kept as a constant so both the error message and the test
// can reference the same literal.
const atlasInstallHint = "https://atlasgo.io/getting-started/"

// ApplyMigrations shells out to the atlas CLI to apply pending migrations from
// migrationsDir against databaseURL. stdout is streamed to logger at info,
// stderr at error — line by line as atlas emits them, so a long-running run
// produces incremental log lines rather than one wall of text at the end.
//
// The atlas binary is located via exec.LookPath. If atlas is not on PATH the
// returned error mentions both `atlas CLI not found on PATH` and the install
// hint URL so callers can self-correct without reading source. A non-zero
// atlas exit produces a wrapped error.
//
// This function deliberately does not embed an atlas Go SDK — atlas's stable
// surface is its CLI, and shelling out keeps the migration runner identical
// to what `task migrate:apply` invokes.
func ApplyMigrations(ctx context.Context, databaseURL string, migrationsDir string, logger *slog.Logger) error {
	if _, err := exec.LookPath("atlas"); err != nil {
		return fmt.Errorf("atlas CLI not found on PATH (install: %s): %w", atlasInstallHint, err)
	}

	cmd := exec.CommandContext(ctx, "atlas",
		"migrate", "apply",
		"--url", databaseURL,
		"--dir", fileURL(migrationsDir),
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("attach atlas stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("attach atlas stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start atlas migrate apply: %w", err)
	}

	var streams sync.WaitGroup
	streams.Add(2)
	go streamLines(&streams, stdout, logger, slog.LevelInfo)
	go streamLines(&streams, stderr, logger, slog.LevelError)
	streams.Wait()

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("atlas migrate apply exited %d: %w", exitErr.ExitCode(), err)
		}
		return fmt.Errorf("atlas migrate apply failed: %w", err)
	}
	return nil
}

// fileURL turns a filesystem path into an atlas-compatible `file://` URL.
// Backslashes from a Windows path become forward slashes; atlas's parser
// then accepts `file://D:/foo` (Windows absolute) and `file:///foo/bar`
// (Linux absolute, where ToSlash leaves the leading `/`) and `file://foo`
// (relative) uniformly. Without this conversion the parser mistakes
// `D:` for a URL port and the run fails.
func fileURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

// streamLines copies pipe line-by-line to logger at the given level until EOF.
// It always calls Done so a partial scan failure does not deadlock the caller.
func streamLines(wg *sync.WaitGroup, pipe io.Reader, logger *slog.Logger, level slog.Level) {
	defer wg.Done()
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		logger.Log(context.Background(), level, "atlas", slog.String("line", scanner.Text()))
	}
}
