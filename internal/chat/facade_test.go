// facade_test.go is the facade-level spec for the chat module. It uses
// the real InMemoryChatGateway as the substrate and a spec-local
// throwingChatGateway decorator for fault injection.
//
// Stdlib testing only — no testify, no mock library.
package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway"
)

// -----------------------------------------------------------------------------
// Happy path
// -----------------------------------------------------------------------------

func TestFacade_Stream_EmitsDeltaFramesThenDone(t *testing.T) {
	facade := NewFacadeWithOverrides(Overrides{})

	frames := collectFrames(t, facade, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hello world"}},
	})

	want := []ChatFrame{
		{Type: FrameTypeDelta, Content: "hello "},
		{Type: FrameTypeDelta, Content: "world "},
		{Type: FrameTypeDone},
	}
	if !equalFrames(frames, want) {
		t.Fatalf("frames: got %+v, want %+v", frames, want)
	}
}

func TestFacade_Stream_WhitespaceOnlyContentRejectedBySchema(t *testing.T) {
	facade := NewFacadeWithOverrides(Overrides{})
	_, err := facade.Stream(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "   "}},
	})
	var invalid *InvalidChatRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected *InvalidChatRequestError, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Validation
// -----------------------------------------------------------------------------

func TestFacade_Stream_EmptyMessagesRejected(t *testing.T) {
	facade := NewFacadeWithOverrides(Overrides{})
	_, err := facade.Stream(context.Background(), ChatRequest{Messages: nil})
	var invalid *InvalidChatRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected *InvalidChatRequestError, got %v", err)
	}
}

func TestFacade_Stream_InvalidRoleRejected(t *testing.T) {
	facade := NewFacadeWithOverrides(Overrides{})
	_, err := facade.Stream(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "robot", Content: "hi"}},
	})
	var invalid *InvalidChatRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected *InvalidChatRequestError, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Gateway failures
// -----------------------------------------------------------------------------

func TestFacade_Stream_GatewaySetupErrorWrappedAsChatGatewayError(t *testing.T) {
	sentinel := errors.New("simulated setup failure")
	facade := NewFacadeWithOverrides(Overrides{Gateway: &throwingChatGateway{setupErr: sentinel}})

	_, err := facade.Stream(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})

	var gwErr *ChatGatewayError
	if !errors.As(err, &gwErr) {
		t.Fatalf("expected *ChatGatewayError, got %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected errors.Is to unwrap to sentinel, got %v", err)
	}
}

func TestFacade_Stream_PerTokenGatewayErrorEmittedAsErrorFrame(t *testing.T) {
	sentinel := errors.New("midway")
	facade := NewFacadeWithOverrides(Overrides{Gateway: &throwingChatGateway{streamErr: sentinel}})

	frames := collectFrames(t, facade, ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})

	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d (%+v)", len(frames), frames)
	}
	if frames[0].Type != FrameTypeError {
		t.Fatalf("frame type: got %q, want %q", frames[0].Type, FrameTypeError)
	}
	if !errors.Is(frames[0].Err, sentinel) {
		t.Fatalf("frame err: got %v, want %v", frames[0].Err, sentinel)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func collectFrames(t *testing.T, facade *Facade, req ChatRequest) []ChatFrame {
	t.Helper()
	ch, err := facade.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	var out []ChatFrame
	for f := range ch {
		out = append(out, f)
	}
	return out
}

func equalFrames(got, want []ChatFrame) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Type != want[i].Type || got[i].Content != want[i].Content {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// throwingChatGateway is a spec-local decorator that fires a single
// configured failure either at setup time (setupErr) or as the first
// streamed delta (streamErr). Mirrors the Throwing-Once pattern from
// the catalog facade spec — intentionally NOT exported.
// -----------------------------------------------------------------------------

type throwingChatGateway struct {
	setupErr  error
	streamErr error
}

func (g *throwingChatGateway) Stream(_ context.Context, _ []chatgateway.ChatMessage) (<-chan chatgateway.ChatDelta, error) {
	if g.setupErr != nil {
		return nil, g.setupErr
	}
	ch := make(chan chatgateway.ChatDelta, 1)
	if g.streamErr != nil {
		ch <- chatgateway.ChatDelta{Err: g.streamErr}
	}
	close(ch)
	return ch, nil
}
