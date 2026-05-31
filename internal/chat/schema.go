package chat

import (
	"fmt"
	"strings"
)

// validRoles enumerates the role values ParseChatRequest accepts. The
// vocabulary matches OpenAI's chat-completion roles 1:1.
var validRoles = map[string]struct{}{
	"user":      {},
	"assistant": {},
	"system":    {},
}

// ParseChatRequest validates a slice of ChatMessage and returns a
// fully-typed ChatRequest. On any failure it returns
// *InvalidChatRequestError whose Reason names the first offending
// constraint. The handler layer is responsible for JSON decoding;
// schema validation operates on already-decoded Go values so the rule
// set lives in one place.
func ParseChatRequest(messages []ChatMessage) (ChatRequest, error) {
	if len(messages) == 0 {
		return ChatRequest{}, &InvalidChatRequestError{Reason: "messages must be non-empty"}
	}
	for _, m := range messages {
		if _, ok := validRoles[m.Role]; !ok {
			return ChatRequest{}, &InvalidChatRequestError{Reason: fmt.Sprintf("invalid role: %s", m.Role)}
		}
		if strings.TrimSpace(m.Content) == "" {
			return ChatRequest{}, &InvalidChatRequestError{Reason: "message content must be non-blank"}
		}
	}
	return ChatRequest{Messages: messages}, nil
}
