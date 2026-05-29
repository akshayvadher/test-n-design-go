// config_test.go verifies cmd/library/config.go's LoadConfig contract.
//
// These are unit tests: zero filesystem fixtures, zero network. Every test
// drives LoadConfig purely through environment variables via t.Setenv (which
// auto-restores on cleanup) and a small clearLibraryEnv helper for the
// defaults scenario. They run in milliseconds.
//
// The package is `main` (not `main_test`) so the tests can call the
// unexported LoadConfig directly without an export hatch.
package main

import (
	"os"
	"strings"
	"testing"
)

// libraryEnvKeys enumerates the LIBRARY_* keys that LoadConfig reads. Tests
// that want a pristine environment call clearLibraryEnv(t) to unset each one
// and restore the original value on test cleanup.
var libraryEnvKeys = []string{
	"LIBRARY_HTTP_PORT",
	"LIBRARY_DATABASE_URL",
	"LIBRARY_REDIS_URL",
	"LIBRARY_LOG_LEVEL",
	"LIBRARY_LOG_FORMAT",
}

// clearLibraryEnv unsets every LIBRARY_* key for the duration of the test and
// restores the prior value on cleanup. This is needed because t.Setenv can
// only set values; the "no env vars set" scenario must actively unset
// anything the developer has exported into their shell.
func clearLibraryEnv(t *testing.T) {
	t.Helper()
	for _, key := range libraryEnvKeys {
		prev, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("could not unset %s: %v", key, err)
		}
		if ok {
			key := key
			prev := prev
			t.Cleanup(func() { _ = os.Setenv(key, prev) })
		} else {
			key := key
			t.Cleanup(func() { _ = os.Unsetenv(key) })
		}
	}
}

// -----------------------------------------------------------------------------
// AC: LoadConfig returns the documented defaults when no env vars are set.
// -----------------------------------------------------------------------------

func TestLoadConfig_ReturnsDocumentedDefaultsWhenEnvIsEmpty(t *testing.T) {
	clearLibraryEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig returned nil config without an error")
	}

	if got, want := cfg.HTTPPort, 3000; got != want {
		t.Errorf("HTTPPort: got %d, want %d", got, want)
	}
	if got, want := cfg.DatabaseURL, "postgres://library:library@localhost:5432/library?sslmode=disable"; got != want {
		t.Errorf("DatabaseURL: got %q, want %q", got, want)
	}
	if got, want := cfg.RedisURL, "redis://localhost:6379/0"; got != want {
		t.Errorf("RedisURL: got %q, want %q", got, want)
	}
	if got, want := cfg.LogLevel, "info"; got != want {
		t.Errorf("LogLevel: got %q, want %q", got, want)
	}
	if got, want := cfg.LogFormat, "json"; got != want {
		t.Errorf("LogFormat: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// AC: Explicit env vars override defaults on a per-field basis.
// -----------------------------------------------------------------------------

func TestLoadConfig_HTTPPortEnvOverridesDefault(t *testing.T) {
	clearLibraryEnv(t)
	t.Setenv("LIBRARY_HTTP_PORT", "4000")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if got, want := cfg.HTTPPort, 4000; got != want {
		t.Errorf("HTTPPort: got %d, want %d", got, want)
	}
}

func TestLoadConfig_DatabaseURLEnvOverridesDefault(t *testing.T) {
	clearLibraryEnv(t)
	const custom = "postgres://test:test@db.internal:5432/test?sslmode=require"
	t.Setenv("LIBRARY_DATABASE_URL", custom)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if got, want := cfg.DatabaseURL, custom; got != want {
		t.Errorf("DatabaseURL: got %q, want %q", got, want)
	}
}

func TestLoadConfig_RedisURLEnvOverridesDefault(t *testing.T) {
	clearLibraryEnv(t)
	const custom = "redis://cache.internal:6380/2"
	t.Setenv("LIBRARY_REDIS_URL", custom)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if got, want := cfg.RedisURL, custom; got != want {
		t.Errorf("RedisURL: got %q, want %q", got, want)
	}
}

func TestLoadConfig_LogLevelEnvOverridesDefault(t *testing.T) {
	clearLibraryEnv(t)
	t.Setenv("LIBRARY_LOG_LEVEL", "debug")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if got, want := cfg.LogLevel, "debug"; got != want {
		t.Errorf("LogLevel: got %q, want %q", got, want)
	}
}

func TestLoadConfig_LogFormatEnvOverridesDefault(t *testing.T) {
	clearLibraryEnv(t)
	t.Setenv("LIBRARY_LOG_FORMAT", "text")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if got, want := cfg.LogFormat, "text"; got != want {
		t.Errorf("LogFormat: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// AC: Non-numeric port returns an error mentioning the offending field.
// -----------------------------------------------------------------------------

func TestLoadConfig_NonNumericPortReturnsErrorNamingTheField(t *testing.T) {
	clearLibraryEnv(t)
	t.Setenv("LIBRARY_HTTP_PORT", "not-a-port")

	cfg, err := LoadConfig()
	if err == nil {
		t.Fatalf("LoadConfig returned no error for non-numeric port; cfg=%+v", cfg)
	}
	if !strings.Contains(err.Error(), "LIBRARY_HTTP_PORT") {
		t.Errorf("error message does not name the offending field: %q", err.Error())
	}
}

// -----------------------------------------------------------------------------
// AC: Bad log level returns an error mentioning the field and the allow-list.
// -----------------------------------------------------------------------------

func TestLoadConfig_BadLogLevelReturnsErrorNamingFieldAndAllowedValues(t *testing.T) {
	clearLibraryEnv(t)
	t.Setenv("LIBRARY_LOG_LEVEL", "verbose")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig returned no error for an invalid log level")
	}

	msg := err.Error()
	if !strings.Contains(msg, "LIBRARY_LOG_LEVEL") {
		t.Errorf("error message does not name the offending field: %q", msg)
	}
	for _, want := range []string{"debug", "info", "warn", "error"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not advertise allowed value %q: %q", want, msg)
		}
	}
}

// -----------------------------------------------------------------------------
// AC: Bad log format returns an error mentioning the field and the allow-list.
// -----------------------------------------------------------------------------

func TestLoadConfig_BadLogFormatReturnsErrorNamingFieldAndAllowedValues(t *testing.T) {
	clearLibraryEnv(t)
	t.Setenv("LIBRARY_LOG_FORMAT", "xml")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig returned no error for an invalid log format")
	}

	msg := err.Error()
	if !strings.Contains(msg, "LIBRARY_LOG_FORMAT") {
		t.Errorf("error message does not name the offending field: %q", msg)
	}
	for _, want := range []string{"json", "text"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not advertise allowed value %q: %q", want, msg)
		}
	}
}
