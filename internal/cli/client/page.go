package client

import "github.com/theimaginaryfoundation/what-iff/internal/models"

// Page is one page of a listing.
//
// It is this package's one deviation from decoding straight into
// internal/models (see client.go's package comment). The server's actual
// envelope is internal/models.PaginatedResponse, whose Results field is []any
// and therefore useless to decode into directly; Page mirrors that envelope's
// wire shape by hand with Results typed as the element a caller actually
// wants.
//
// TotalCount is carried alongside the results because a listing is capped by
// the caller's limit: without it, a command showing 100 of 347 chats has no
// way to say so, and a truncated list is indistinguishable from missing data.
//
// The server's envelope also carries a page number, but nothing here consumes
// it yet, so it is deliberately left undecoded rather than kept as dead
// weight — it can come back once pagination actually lands and something
// reads it.
//
// Because Page is a hand-maintained mirror rather than the real server type,
// a json-tag change on PaginatedResponse would not be caught by the type
// system. TestPageMatchesPaginatedResponseEnvelope (page_test.go) marshals a
// real models.PaginatedResponse and unmarshals it into a Page specifically to
// guard against that drift, for every element type aliased below.
type Page[T any] struct {
	Results    []T `json:"results"`
	TotalCount int `json:"total_count"`
}

// ChatPage is one page of a chat listing (GET /chat).
type ChatPage = Page[models.Chat]

// ChatMessagePage is one page of a chat's messages (GET /chat/{id}/chat-message).
//
// The server orders this listing newest-first (see
// internal/datastore/chatmessage.go's ListChatMessages), which is what makes
// a small limit useful for reconciling the turn that just finished rather
// than a way to read a conversation from the start.
type ChatMessagePage = Page[models.ChatMessage]
