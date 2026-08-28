package oneshot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// TestResolveChatUsesAUUIDDirectly pins that a pasted id costs no round trip:
// it is unambiguous by construction, and confirming it would only turn a
// clear server-side 404 into an earlier, less obvious one.
func TestResolveChatUsesAUUIDDirectly(t *testing.T) {
	want := uuid.New()
	chats := &fakeChats{
		listed: []models.Chat{{ID: uuid.New(), Name: "something else"}},
	}

	got, created, err := resolveChat(context.Background(), chats, want.String(), "unused")
	if err != nil {
		t.Fatalf("resolveChat returned %v", err)
	}
	if got != want {
		t.Errorf("chatID = %s, want %s", got, want)
	}
	if created {
		t.Error("created = true, want false for an existing chat id")
	}
	if chats.lastSearch != "" {
		t.Errorf("searched for %q, want no lookup at all", chats.lastSearch)
	}
	if chats.creates != 0 {
		t.Errorf("CreateChat called %d times, want 0", chats.creates)
	}
}

func TestResolveChatMatchesAnExactName(t *testing.T) {
	want := uuid.New()
	chats := &fakeChats{
		listed: []models.Chat{
			// The server's search also matches checkpoint summaries, so the
			// results are a superset of what --chat meant. Only the exact
			// name may win.
			{ID: uuid.New(), Name: "notes that mention the deploy plan"},
			{ID: want, Name: "deploy plan"},
		},
	}

	got, _, err := resolveChat(context.Background(), chats, "deploy plan", "unused")
	if err != nil {
		t.Fatalf("resolveChat returned %v", err)
	}
	if got != want {
		t.Errorf("chatID = %s, want the exact name match %s", got, want)
	}
	if chats.lastSearch != "deploy plan" {
		t.Errorf("search = %q, want %q", chats.lastSearch, "deploy plan")
	}
}

func TestResolveChatNameIsCaseInsensitive(t *testing.T) {
	want := uuid.New()
	chats := &fakeChats{listed: []models.Chat{{ID: want, Name: "Deploy Plan"}}}

	got, _, err := resolveChat(context.Background(), chats, "deploy plan", "unused")
	if err != nil {
		t.Fatalf("resolveChat returned %v", err)
	}
	if got != want {
		t.Errorf("chatID = %s, want %s", got, want)
	}
}

// TestResolveChatAmbiguousNameLists is the behavior the design calls out by
// name: two chats sharing a name is an error naming both, never a silent
// pick, because a silently-wrong target sends a message somewhere the user
// will not think to look.
func TestResolveChatAmbiguousNameLists(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	chats := &fakeChats{
		listed: []models.Chat{
			{ID: first, Name: "deploy plan"},
			{ID: second, Name: "deploy plan"},
		},
	}

	_, _, err := resolveChat(context.Background(), chats, "deploy plan", "unused")

	var ambiguous *AmbiguousChatError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("resolveChat error = %v, want an *AmbiguousChatError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(ambiguous.Candidates))
	}
	message := ambiguous.Error()
	for _, id := range []uuid.UUID{first, second} {
		if !strings.Contains(message, id.String()) {
			t.Errorf("error %q does not name candidate %s", message, id)
		}
	}
}

func TestResolveChatUnknownName(t *testing.T) {
	chats := &fakeChats{listed: []models.Chat{{ID: uuid.New(), Name: "something else"}}}

	_, _, err := resolveChat(context.Background(), chats, "deploy plan", "unused")
	if err == nil {
		t.Fatal("resolveChat returned nil error for a name that matches nothing")
	}
	if !strings.Contains(err.Error(), "deploy plan") {
		t.Errorf("error = %q, want it to name the chat that was not found", err)
	}
}

// TestResolveChatCreatesWhenUnset pins the zero-setup path: `wi -p "..."`
// with no --chat starts a chat rather than refusing.
func TestResolveChatCreatesWhenUnset(t *testing.T) {
	chats := &fakeChats{}

	got, created, err := resolveChat(context.Background(), chats, "", "review this migration")
	if err != nil {
		t.Fatalf("resolveChat returned %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if got != chats.createdID {
		t.Errorf("chatID = %s, want the created chat %s", got, chats.createdID)
	}
	if chats.createdName != "review this migration" {
		t.Errorf("created name = %q, want %q", chats.createdName, "review this migration")
	}
}

func TestResolveChatSurfacesCreateFailure(t *testing.T) {
	chats := &fakeChats{createErr: errors.New("quota exceeded")}

	if _, _, err := resolveChat(context.Background(), chats, "", "x"); err == nil {
		t.Fatal("resolveChat returned nil error when chat creation failed")
	}
}
