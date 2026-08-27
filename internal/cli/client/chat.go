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

// chatPage is the server's pagination envelope specialized to chats. The shared
// models.PaginatedResponse uses []any, which would force a second decode pass.
type chatPage struct {
	Results    []models.Chat `json:"results"`
	TotalCount int           `json:"total_count"`
	Page       int           `json:"page"`
}

// ListChats returns the user's chats. It calls do, not doJSON, so an expired
// access token refreshes transparently — the same guarantee every other
// resource call in this package gets.
func (c *Client) ListChats(ctx context.Context, opts ListChatsOptions) ([]models.Chat, error) {
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

	var page chatPage
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return nil, err
	}
	return page.Results, nil
}
