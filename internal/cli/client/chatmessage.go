package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// CreateMessageRequest is the body of POST /chat/{id}/chat-message.
//
// It is declared here rather than reused from internal/handlers/chat because
// the server's own createChatMessageRequest is unexported — this is the one
// place the CLI has to restate a request shape instead of sharing the type.
// Origin is not a field: the server accepts an origin but the only one a
// client may legitimately send is "User", so CreateChatMessage fills it in
// and leaves callers no way to forge an assistant turn by accident.
type CreateMessageRequest struct {
	Message string `json:"message"`
	// ClientTimezone (an IANA name such as "Europe/Berlin") is what lets the
	// assistant answer "what day is it" correctly. The browser sends its own;
	// a terminal has to say so explicitly or the turn is rendered against the
	// server's clock.
	ClientTimezone string `json:"client_timezone,omitempty"`
}

// CreateChatMessage posts a user turn and returns the accepted job. The
// server answers 202 with the persisted message ID and the job ID that will
// carry the reply — polling that job is the whole of the streaming protocol.
func (c *Client) CreateChatMessage(ctx context.Context, chatID uuid.UUID, req CreateMessageRequest) (models.ChatMessageResponse, error) {
	body := struct {
		CreateMessageRequest
		Origin models.MessageOrigin `json:"origin"`
	}{CreateMessageRequest: req, Origin: models.MessageOriginUser}

	var resp models.ChatMessageResponse
	if err := c.do(ctx, http.MethodPost, "/chat/"+chatID.String()+"/chat-message", body, &resp); err != nil {
		return models.ChatMessageResponse{}, err
	}
	return resp, nil
}

// GetChatMessage fetches one persisted message, fully hydrated — attachments,
// tool calls, the model and personality actually used, and the context
// breakdown. This is how a finished turn is reconciled: the job's ResultID
// names the assistant message the turn produced.
func (c *Client) GetChatMessage(ctx context.Context, messageID uuid.UUID) (models.ChatMessage, error) {
	var msg models.ChatMessage
	if err := c.do(ctx, http.MethodGet, "/chat/chat-message/"+messageID.String(), nil, &msg); err != nil {
		return models.ChatMessage{}, err
	}
	return msg, nil
}

// GetActiveChatMessageJob returns the non-terminal job still generating a
// reply for messageID, or nil when there is none.
//
// The nil return is the server's 204: it says "that user turn has no job in
// flight", which is a normal answer (the turn finished, or never had one),
// not an error. A restarted client uses this to re-attach to a turn it was
// watching rather than orphaning it.
func (c *Client) GetActiveChatMessageJob(ctx context.Context, chatID, messageID uuid.UUID) (*models.ActiveChatMessageJobResponse, error) {
	var resp models.ActiveChatMessageJobResponse
	path := "/chat/" + chatID.String() + "/chat-message/" + messageID.String() + "/active-job"
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	// doJSON leaves resp untouched on a 204 (see its body-decoding branch), so
	// a zero JobID is how "no active job" arrives here. A real job always has
	// a non-nil UUID, so the two cannot be confused.
	if resp.JobID == uuid.Nil {
		return nil, nil
	}
	return &resp, nil
}

// ListChatMessagesOptions filters a message listing. The zero value asks the
// server for its own default page size.
type ListChatMessagesOptions struct {
	Limit int
}

// ListChatMessages returns one newest-first page of a chat's messages.
func (c *Client) ListChatMessages(ctx context.Context, chatID uuid.UUID, opts ListChatMessagesOptions) (ChatMessagePage, error) {
	q := url.Values{}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	path := "/chat/" + chatID.String() + "/chat-message"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var page ChatMessagePage
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return ChatMessagePage{}, err
	}
	return page, nil
}
