package openai

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway"
)

// These tests cover the construction-time behaviour of the OpenAI
// gateway. Streaming behaviour is exercised by the chat facade tests
// against the in-memory gateway — we do not call live OpenAI from the
// test suite.

func TestNewGateway_DefaultsModelWhenEmpty(t *testing.T) {
	gateway := NewGateway("test-key", "", nil)
	if gateway.model != defaultModel {
		t.Fatalf("model: got %q, want %q", gateway.model, defaultModel)
	}
	if gateway.logger == nil {
		t.Fatalf("logger should default to a discard handler, got nil")
	}
}

func TestNewGateway_HonoursExplicitModel(t *testing.T) {
	gateway := NewGateway("test-key", "gpt-4.1-mini", slog.Default())
	if gateway.model != "gpt-4.1-mini" {
		t.Fatalf("model: got %q, want %q", gateway.model, "gpt-4.1-mini")
	}
}

func TestOpenAIChatGateway_EmptyMessagesReturnsSetupError(t *testing.T) {
	gateway := NewGateway("test-key", "", nil)
	ch, err := gateway.Stream(context.Background(), nil)
	if ch != nil {
		t.Fatalf("expected nil channel on setup error, got %v", ch)
	}
	var empty *chatgateway.EmptyMessagesError
	if !errors.As(err, &empty) {
		t.Fatalf("expected *chatgateway.EmptyMessagesError, got %v", err)
	}
}

func TestToOpenAIMessages_PreservesRoleAndContent(t *testing.T) {
	got := toOpenAIMessages([]chatgateway.ChatMessage{
		{Role: chatgateway.RoleSystem, Content: "be terse"},
		{Role: chatgateway.RoleUser, Content: "hi"},
	})
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2", len(got))
	}
	if got[0].Role != chatgateway.RoleSystem || got[0].Content != "be terse" {
		t.Fatalf("first message: got %+v", got[0])
	}
	if got[1].Role != chatgateway.RoleUser || got[1].Content != "hi" {
		t.Fatalf("second message: got %+v", got[1])
	}
}
