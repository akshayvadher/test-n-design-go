package chatgateway

import (
	"context"
	"strings"
	"time"
)

// InMemoryChatGateway is the deterministic test substrate. It splits the
// last message's Content on whitespace via strings.Fields and emits one
// ChatDelta per token. Concatenating the deltas' Content fields
// reconstructs the original text (each delta carries the token plus a
// trailing space, except the joined output mirrors the input with a
// trailing space appended). The trailing-space convention is intentional:
// it keeps the streaming output streamable into a text buffer without
// post-processing.
//
// TokenInterval is exported so tests can twiddle it to verify cancellation
// behaviour. The zero value (synchronous emit) is the test-friendly
// default and is what NewInMemoryChatGateway returns.
type InMemoryChatGateway struct {
	TokenInterval time.Duration
}

// NewInMemoryChatGateway returns an InMemoryChatGateway with TokenInterval
// = 0 (synchronous emit).
func NewInMemoryChatGateway() *InMemoryChatGateway {
	return &InMemoryChatGateway{TokenInterval: 0}
}

var _ ChatGateway = (*InMemoryChatGateway)(nil)

// Stream splits the last message's content into whitespace tokens and
// emits one ChatDelta per token. Setup-time validation (empty messages
// slice) surfaces through the error return; per-token errors do not
// occur for this implementation, so the channel always ends by closing
// after the last token (no error delta).
func (g *InMemoryChatGateway) Stream(ctx context.Context, messages []ChatMessage) (<-chan ChatDelta, error) {
	if len(messages) == 0 {
		return nil, &EmptyMessagesError{}
	}
	tokens := strings.Fields(messages[len(messages)-1].Content)
	ch := make(chan ChatDelta, len(tokens)+1)
	go g.emitTokens(ctx, tokens, ch)
	return ch, nil
}

// emitTokens writes one ChatDelta per token to ch, sleeping
// TokenInterval between writes and aborting early on ctx cancellation.
// The channel is always closed before the goroutine returns.
func (g *InMemoryChatGateway) emitTokens(ctx context.Context, tokens []string, ch chan<- ChatDelta) {
	defer close(ch)
	for _, token := range tokens {
		if g.TokenInterval > 0 {
			if !sleepOrCancel(ctx, g.TokenInterval) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case ch <- ChatDelta{Content: token + " "}:
		}
	}
}

// sleepOrCancel returns false if ctx is cancelled before d elapses.
func sleepOrCancel(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
