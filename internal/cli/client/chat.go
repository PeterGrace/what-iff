package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// ListChatsOptions filters a chat listing. The zero value lists active
// (non-archived) chats, matching the server's default.
type ListChatsOptions struct {
	Archived bool
	Search   string
	Limit    int
}

// ListChats returns one page of the user's chats. It calls do, not doJSON, so
// an expired access token refreshes transparently — the same guarantee every
// other resource call in this package gets.
func (c *Client) ListChats(ctx context.Context, opts ListChatsOptions) (ChatPage, error) {
	q := url.Values{}
	if opts.Archived {
		q.Set("archived", "true")
	}
	if opts.Search != "" {
		q.Set("search", opts.Search)
	}
	// The server defaults to a page size of 10 when limit is omitted
	// (internal/handlers/chat/chat.go). Leaving Limit <= 0 unset here is a
	// deliberate choice, not an oversight: this package stays a thin
	// transport, and it is the caller's job to decide what "list chats"
	// should default to (the wi chats command passes an explicit limit).
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}

	path := "/chat"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var page ChatPage
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return ChatPage{}, err
	}
	return page, nil
}

// CreateChat creates a chat with the given name and returns it.
//
// The body is deliberately just the name rather than a models.Chat: the
// server falls back to the user's preferred model and personality when those
// fields are absent (internal/datastore/chat.go's CreateChat), and
// models.Chat's model_id/personality_id tags carry no omitempty, so marshaling
// one would put explicit all-zero UUIDs on the wire where "unset" was meant.
func (c *Client) CreateChat(ctx context.Context, name string) (models.Chat, error) {
	body := struct {
		Name string `json:"name"`
	}{Name: name}

	var chat models.Chat
	if err := c.do(ctx, http.MethodPost, "/chat", body, &chat); err != nil {
		return models.Chat{}, err
	}
	return chat, nil
}
