package chatgateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemoryChatGateway_StreamsTokensInOrder(t *testing.T) {
	gateway := NewInMemoryChatGateway()
	ch, err := gateway.Stream(context.Background(), []ChatMessage{
		{Role: RoleUser, Content: "hello world go"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	got := drain(ch)
	want := []string{"hello ", "world ", "go "}
	if !equalContents(got, want) {
		t.Fatalf("tokens: got %q, want %q", got, want)
	}
}

func TestInMemoryChatGateway_SingleTokenMessage(t *testing.T) {
	gateway := NewInMemoryChatGateway()
	ch, err := gateway.Stream(context.Background(), []ChatMessage{
		{Role: RoleUser, Content: "hi"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if got, want := drain(ch), []string{"hi "}; !equalContents(got, want) {
		t.Fatalf("tokens: got %q, want %q", got, want)
	}
}

func TestInMemoryChatGateway_EmptyMessagesReturnsError(t *testing.T) {
	gateway := NewInMemoryChatGateway()
	ch, err := gateway.Stream(context.Background(), nil)
	if ch != nil {
		t.Fatalf("expected nil channel, got %v", ch)
	}
	var empty *EmptyMessagesError
	if !errors.As(err, &empty) {
		t.Fatalf("expected *EmptyMessagesError, got %v", err)
	}
}

func TestInMemoryChatGateway_WhitespaceOnlyContentEmitsZeroDeltas(t *testing.T) {
	gateway := NewInMemoryChatGateway()
	ch, err := gateway.Stream(context.Background(), []ChatMessage{
		{Role: RoleUser, Content: "   "},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("expected zero deltas, got %q", got)
	}
}

func TestInMemoryChatGateway_ContextCancellationAbortsStream(t *testing.T) {
	gateway := &InMemoryChatGateway{TokenInterval: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := gateway.Stream(ctx, []ChatMessage{
		{Role: RoleUser, Content: "a b c d e"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	// Read first delta, then cancel.
	first, ok := <-ch
	if !ok {
		t.Fatalf("expected first delta")
	}
	if first.Content != "a " {
		t.Fatalf("first delta: got %q, want %q", first.Content, "a ")
	}
	cancel()

	collected := []ChatDelta{first}
	done := make(chan struct{})
	go func() {
		for d := range ch {
			collected = append(collected, d)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("channel did not close within 500ms after cancellation")
	}

	if len(collected) >= 5 {
		t.Fatalf("expected fewer than 5 deltas after cancellation, got %d", len(collected))
	}
}

func TestInMemoryChatGateway_UsesLastMessageOnly(t *testing.T) {
	gateway := NewInMemoryChatGateway()
	ch, err := gateway.Stream(context.Background(), []ChatMessage{
		{Role: RoleUser, Content: "ignored"},
		{Role: RoleAssistant, Content: "also ignored"},
		{Role: RoleUser, Content: "echo me"},
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if got, want := drain(ch), []string{"echo ", "me "}; !equalContents(got, want) {
		t.Fatalf("tokens: got %q, want %q", got, want)
	}
}

// drain reads every delta from ch until close and returns the slice of
// Content strings. Non-nil Err on any delta is recorded by appending the
// error message; tests inspecting per-token errors do so explicitly via
// the returned slice's contents.
func drain(ch <-chan ChatDelta) []string {
	var out []string
	for d := range ch {
		if d.Err != nil {
			out = append(out, d.Err.Error())
			continue
		}
		out = append(out, d.Content)
	}
	return out
}

func equalContents(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
