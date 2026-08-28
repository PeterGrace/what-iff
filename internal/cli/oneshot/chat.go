// Package oneshot runs a single turn without a terminal UI: `wi -p "..."`,
// and the piped form `git diff | wi -p "review this"`.
//
// It is the first consumer of the engine seam, and it is deliberately thin —
// resolve which chat to talk to, fold stdin into the prompt, render the
// event stream, pick an exit code. Everything about how a turn actually
// works lives in internal/cli/engine.
package oneshot

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// chatSearchLimit bounds the name lookup. A name is either a match or it is
// not; this only has to be large enough that an exact match is not paged out
// from underneath the filter below.
const chatSearchLimit = 100

// Chats is the slice of the REST client needed to decide which chat a turn
// belongs to.
type Chats interface {
	ListChats(ctx context.Context, opts client.ListChatsOptions) (client.ChatPage, error)
	CreateChat(ctx context.Context, name string) (models.Chat, error)
}

// AmbiguousChatError is returned when a --chat name matches more than one
// chat. It lists the candidates rather than picking one: silently choosing
// between two chats called "deploy plan" would send a message somewhere the
// user did not look, and they would have no way to tell.
type AmbiguousChatError struct {
	Name       string
	Candidates []models.Chat
}

func (e *AmbiguousChatError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d chats are named %q; pass --chat with one of these ids instead:", len(e.Candidates), e.Name)
	for _, candidate := range e.Candidates {
		fmt.Fprintf(&b, "\n  %s", candidate.ID)
	}
	return b.String()
}

// resolveChat turns the user's --chat value into a chat to talk to.
//
// An empty ref creates a new chat, which is what makes `wi -p "..."` work
// with no setup at all. A ref that parses as a UUID is used as-is and is not
// looked up: an id the user pasted is unambiguous by construction, and
// spending a round trip to confirm it would only turn a clear server-side
// 404 into a different, earlier one.
func resolveChat(ctx context.Context, chats Chats, ref, newChatName string) (chatID uuid.UUID, created bool, err error) {
	if ref == "" {
		chat, err := chats.CreateChat(ctx, newChatName)
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("creating a chat for this turn: %w", err)
		}
		return chat.ID, true, nil
	}
	if id, parseErr := uuid.Parse(ref); parseErr == nil {
		return id, false, nil
	}

	page, err := chats.ListChats(ctx, client.ListChatsOptions{Search: ref, Limit: chatSearchLimit})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("looking up chat %q: %w", ref, err)
	}

	// The server's search also matches a chat's checkpoint summary, not just
	// its name, so the results are a superset of what --chat meant. Narrowing
	// to an exact, case-insensitive name match here is what keeps "deploy"
	// from resolving to a chat that merely mentions a deploy.
	matches := make([]models.Chat, 0, len(page.Results))
	for _, chat := range page.Results {
		if strings.EqualFold(strings.TrimSpace(chat.Name), strings.TrimSpace(ref)) {
			matches = append(matches, chat)
		}
	}

	switch len(matches) {
	case 0:
		return uuid.Nil, false, fmt.Errorf("no active chat named %q (archived chats are not searched; pass a chat id to reach one)", ref)
	case 1:
		return matches[0].ID, false, nil
	default:
		return uuid.Nil, false, &AmbiguousChatError{Name: ref, Candidates: matches}
	}
}
