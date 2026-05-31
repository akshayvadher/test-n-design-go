//go:build integration

package bun

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// testEvent is the synthetic DomainEvent used throughout this file's
// tests. Declared locally so the bun tx package owns no business
// imports — events.DomainEvent is satisfied via duck typing.
type testEvent struct {
	name string
}

func (e testEvent) Type() string { return e.name }

// recordingHandler captures every log record into a slice for
// structured-field assertions. Replaces string-match assertions that
// break when slog's rendering changes.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, record.Clone())
	h.mu.Unlock()
	return nil
}
func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *recordingHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// assertLoggedPublishFailure asserts records contains an error-level
// log entry tagged with event_type, event_index, and a matching error
// string.
func assertLoggedPublishFailure(t *testing.T, records []slog.Record, eventType string, eventIndex int, wantErr error) {
	t.Helper()
	for _, record := range records {
		if record.Level != slog.LevelError {
			continue
		}
		attrs := collectAttrs(record)
		if attrs["event_type"] != eventType {
			continue
		}
		if attrs["event_index"] != int64(eventIndex) {
			continue
		}
		if attrs["error"] != wantErr.Error() {
			continue
		}
		return
	}
	t.Fatalf("no error-level log record matched event_type=%q event_index=%d error=%q; records=%v",
		eventType, eventIndex, wantErr.Error(), records)
}

// collectAttrs flattens record's Attrs into a key→value map for
// table-driven assertions.
func collectAttrs(record slog.Record) map[string]any {
	out := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		out[attr.Key] = attr.Value.Any()
		return true
	})
	return out
}
