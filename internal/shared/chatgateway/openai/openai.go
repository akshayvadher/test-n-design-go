package openai

import (
	"context"
	"errors"
	"io"
	"log/slog"

	upstreamopenai "github.com/sashabaranov/go-openai"

	"github.com/akshayvadher/test-n-design-go/internal/shared/chatgateway"
)

const defaultModel = "gpt-4o-mini"

// Gateway wraps the official-community OpenAI SDK
// (github.com/sashabaranov/go-openai). It maps a slice of ChatMessage
// onto openai.ChatCompletionRequest, opens a streaming completion, and
// forwards each non-empty delta into the outbound channel.
//
// Per-token errors close the channel after emitting one ChatDelta whose
// Err field carries the underlying error. Setup errors (auth failure,
// network unreachable at request time) return through the error return
// of Stream — no channel is opened.
type Gateway struct {
	client *upstreamopenai.Client
	model  string
	logger *slog.Logger
}

// Compile-time assertion that *Gateway satisfies the
// chatgateway.ChatGateway port.
var _ chatgateway.ChatGateway = (*Gateway)(nil)

// NewGateway constructs the OpenAI-backed gateway. An empty model
// defaults to gpt-4o-mini; a nil logger defaults to a discard handler
// so the gateway is safe to use without explicit logger wiring in
// tests.
func NewGateway(apiKey string, model string, logger *slog.Logger) *Gateway {
	if model == "" {
		model = defaultModel
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Gateway{
		client: upstreamopenai.NewClient(apiKey),
		model:  model,
		logger: logger,
	}
}

// Stream issues a streaming chat-completion request and forwards each
// non-empty token onto the returned channel. The goroutine closes the
// channel when stream.Recv returns io.EOF or any other error.
func (g *Gateway) Stream(ctx context.Context, messages []chatgateway.ChatMessage) (<-chan chatgateway.ChatDelta, error) {
	if len(messages) == 0 {
		return nil, &chatgateway.EmptyMessagesError{}
	}
	stream, err := g.client.CreateChatCompletionStream(ctx, upstreamopenai.ChatCompletionRequest{
		Model:    g.model,
		Messages: toOpenAIMessages(messages),
		Stream:   true,
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan chatgateway.ChatDelta)
	go g.forward(stream, ch)
	return ch, nil
}

// forward pumps chunks from the OpenAI stream into ch. io.EOF closes
// the channel cleanly; any other error is delivered as one final
// ChatDelta carrying Err before the channel closes.
func (g *Gateway) forward(stream *upstreamopenai.ChatCompletionStream, ch chan<- chatgateway.ChatDelta) {
	defer close(ch)
	defer stream.Close()
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			g.logger.Error("openai stream recv",
				slog.String("error", err.Error()),
				slog.String("model", g.model),
			)
			ch <- chatgateway.ChatDelta{Err: err}
			return
		}
		if len(resp.Choices) == 0 {
			continue
		}
		content := resp.Choices[0].Delta.Content
		if content == "" {
			continue
		}
		ch <- chatgateway.ChatDelta{Content: content}
	}
}

// toOpenAIMessages maps the gateway's ChatMessage slice onto the SDK's
// ChatCompletionMessage slice. Role and Content are the only fields
// the stream needs.
func toOpenAIMessages(messages []chatgateway.ChatMessage) []upstreamopenai.ChatCompletionMessage {
	out := make([]upstreamopenai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		out[i] = upstreamopenai.ChatCompletionMessage{Role: m.Role, Content: m.Content}
	}
	return out
}
