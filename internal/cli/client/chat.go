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

// ChatPage is one page of a chat listing.
//
// TotalCount is returned alongside the results because a listing is capped by
// the caller's limit: without it, a command showing 100 of 347 chats has no way
// to say so, and a truncated list is indistinguishable from missing data.
//
// The server's envelope also carries a page number, but nothing here consumes
// it yet, so it is deliberately left undecoded rather than kept as dead
// weight — it can come back once pagination actually lands and something
// reads it.
//
// ChatPage is this package's one deviation from decoding straight into
// internal/models (see client.go's package comment): the server's actual
// envelope is internal/models.PaginatedResponse, whose Results is []any and
// therefore useless to decode into directly. ChatPage mirrors that
// envelope's wire shape by hand, with Results typed as []models.Chat.
// Because it's a hand-maintained mirror rather than the real type, a tag
// change on PaginatedResponse would not be caught by the type system — see
// TestChatPageMatchesPaginatedResponseEnvelope (chat_test.go), which
// marshals a real models.PaginatedResponse and unmarshals it into ChatPage
// specifically to guard against that drift.
type ChatPage struct {
	Results    []models.Chat `json:"results"`
	TotalCount int           `json:"total_count"`
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
