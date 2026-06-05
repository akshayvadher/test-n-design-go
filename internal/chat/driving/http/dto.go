// Package http is the chat module's HTTP edge. Every type defined
// here is a wire shape — a JSON request body or response envelope —
// and exists only to be encoded/decoded across the network boundary.
// These types do not leak out of this package: the handler translates
// the inbound DTO into chat.ChatRequest via mapping helpers and writes
// SSE frames directly to the response writer.
//
// JSON tags match the source TypeScript API contract verbatim
// (`messages`, `role`, `content`).
package http

// ChatRequest is the inbound JSON body for POST /chat.
type ChatRequest struct {
	Messages []ChatMessage `json:"messages"`
}

// ChatMessage is one turn of conversation on the wire.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
