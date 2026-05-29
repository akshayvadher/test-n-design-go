// Package main hosts the composition root for the library service binary.
//
// config.go owns Phase-1 environment configuration. Every value the binary
// needs at boot is loaded here, validated up front, and returned as an
// immutable *Config. Callers receive a fully-typed, fully-validated value or
// a precise error — never silent fallbacks.
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the runtime settings for the library binary. All fields are
// populated by LoadConfig from environment variables (with defaults documented
// in .env.example). The struct is immutable after construction.
type Config struct {
	HTTPPort    int
	DatabaseURL string
	RedisURL    string
	LogLevel    string
	LogFormat   string
}

// envPrefix is the viper prefix applied to every key in this binary. E.g. the
// Config.HTTPPort field is read from LIBRARY_HTTP_PORT.
const envPrefix = "LIBRARY"

// Keys read by LoadConfig. Each maps to LIBRARY_<KEY> via viper's env prefix.
const (
	keyHTTPPort    = "http_port"
	keyDatabaseURL = "database_url"
	keyRedisURL    = "redis_url"
	keyLogLevel    = "log_level"
	keyLogFormat   = "log_format"
)

// validLogLevels enumerates the accepted LIBRARY_LOG_LEVEL values.
var validLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"warn":  {},
	"error": {},
}

// validLogFormats enumerates the accepted LIBRARY_LOG_FORMAT values.
var validLogFormats = map[string]struct{}{
	"json": {},
	"text": {},
}

// LoadConfig reads configuration from the environment via viper, applying the
// defaults documented in .env.example. A .env file at the working directory is
// merged in when present; explicit environment variables override .env values.
//
// LoadConfig returns an error whose message names the offending field when a
// required value is missing or unparseable: a non-numeric port, a log level
// outside debug|info|warn|error, or a log format outside json|text.
func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	applyConfigDefaults(v)
	if err := mergeDotEnv(v); err != nil {
		return nil, err
	}

	cfg := &Config{
		DatabaseURL: v.GetString(keyDatabaseURL),
		RedisURL:    v.GetString(keyRedisURL),
		LogLevel:    strings.ToLower(strings.TrimSpace(v.GetString(keyLogLevel))),
		LogFormat:   strings.ToLower(strings.TrimSpace(v.GetString(keyLogFormat))),
	}

	port, err := parsePort(v)
	if err != nil {
		return nil, err
	}
	cfg.HTTPPort = port

	if err := validateLogLevel(cfg.LogLevel); err != nil {
		return nil, err
	}
	if err := validateLogFormat(cfg.LogFormat); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyConfigDefaults seeds viper with the defaults documented in .env.example.
// These are the values returned when neither the environment nor a .env file
// supplies the key.
func applyConfigDefaults(v *viper.Viper) {
	v.SetDefault(keyHTTPPort, 3000)
	v.SetDefault(keyDatabaseURL, "postgres://library:library@localhost:5432/library?sslmode=disable")
	v.SetDefault(keyRedisURL, "redis://localhost:6379/0")
	v.SetDefault(keyLogLevel, "info")
	v.SetDefault(keyLogFormat, "json")
}

// mergeDotEnv loads `.env` from the working directory if it exists. Missing
// file is not an error — the .env is purely a developer convenience.
func mergeDotEnv(v *viper.Viper) error {
	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")

	err := v.MergeInConfig()
	if err == nil {
		return nil
	}
	var notFound viper.ConfigFileNotFoundError
	if errors.As(err, &notFound) {
		return nil
	}
	return fmt.Errorf("loading .env: %w", err)
}

// parsePort extracts LIBRARY_HTTP_PORT and rejects non-numeric input. Viper's
// GetInt silently returns 0 for unparseable values, so we re-validate using the
// raw string to give the developer a precise error.
func parsePort(v *viper.Viper) (int, error) {
	raw := strings.TrimSpace(v.GetString(keyHTTPPort))
	if raw == "" {
		return 0, fmt.Errorf("config field %s_HTTP_PORT is required", envPrefix)
	}
	port := v.GetInt(keyHTTPPort)
	if port == 0 && raw != "0" {
		return 0, fmt.Errorf("config field %s_HTTP_PORT must be numeric, got %q", envPrefix, raw)
	}
	return port, nil
}

// validateLogLevel rejects any LIBRARY_LOG_LEVEL outside debug|info|warn|error.
func validateLogLevel(level string) error {
	if _, ok := validLogLevels[level]; !ok {
		return fmt.Errorf("config field %s_LOG_LEVEL must be one of debug|info|warn|error, got %q", envPrefix, level)
	}
	return nil
}

// validateLogFormat rejects any LIBRARY_LOG_FORMAT outside json|text.
func validateLogFormat(format string) error {
	if _, ok := validLogFormats[format]; !ok {
		return fmt.Errorf("config field %s_LOG_FORMAT must be one of json|text, got %q", envPrefix, format)
	}
	return nil
}
