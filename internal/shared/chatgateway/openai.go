package chatgateway

import (
	"context"
	"errors"
	"io"
	"log/slog"

	openai "github.com/sashabaranov/go-openai"
)

const defaultOpenAIModel = "gpt-4o-mini"

// OpenAIChatGateway wraps the official-community OpenAI SDK
// (github.com/sashabaranov/go-openai). It maps a slice of ChatMessage
// onto openai.ChatCompletionRequest, opens a streaming completion, and
// forwards each non-empty delta into the outbound channel.
//
// Per-token errors close the channel after emitting one ChatDelta whose
// Err field carries the underlying error. Setup errors (auth failure,
// network unreachable at request time) return through the error return
// of Stream — no channel is opened.
type OpenAIChatGateway struct {
	client *openai.Client
	model  string
	logger *slog.Logger
}

// NewOpenAIChatGateway constructs the OpenAI-backed gateway. An empty
// model defaults to gpt-4o-mini; a nil logger defaults to a discard
// handler so the gateway is safe to use without explicit logger wiring
// in tests.
func NewOpenAIChatGateway(apiKey string, model string, logger *slog.Logger) *OpenAIChatGateway {
	if model == "" {
		model = defaultOpenAIModel
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &OpenAIChatGateway{
		client: openai.NewClient(apiKey),
		model:  model,
		logger: logger,
	}
}

var _ ChatGateway = (*OpenAIChatGateway)(nil)

// Stream issues a streaming chat-completion request and forwards each
// non-empty token onto the returned channel. The goroutine closes the
// channel when stream.Recv returns io.EOF or any other error.
func (g *OpenAIChatGateway) Stream(ctx context.Context, messages []ChatMessage) (<-chan ChatDelta, error) {
	if len(messages) == 0 {
		return nil, &EmptyMessagesError{}
	}
	stream, err := g.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    g.model,
		Messages: toOpenAIMessages(messages),
		Stream:   true,
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan ChatDelta)
	go g.forward(stream, ch)
	return ch, nil
}

// forward pumps chunks from the OpenAI stream into ch. io.EOF closes the
// channel cleanly; any other error is delivered as one final ChatDelta
// carrying Err before the channel closes.
func (g *OpenAIChatGateway) forward(stream *openai.ChatCompletionStream, ch chan<- ChatDelta) {
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
			ch <- ChatDelta{Err: err}
			return
		}
		if len(resp.Choices) == 0 {
			continue
		}
		content := resp.Choices[0].Delta.Content
		if content == "" {
			continue
		}
		ch <- ChatDelta{Content: content}
	}
}

// toOpenAIMessages maps the gateway's ChatMessage slice onto the SDK's
// ChatCompletionMessage slice. Role and Content are the only fields the
// stream needs.
func toOpenAIMessages(messages []ChatMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		out[i] = openai.ChatCompletionMessage{Role: m.Role, Content: m.Content}
	}
	return out
}
