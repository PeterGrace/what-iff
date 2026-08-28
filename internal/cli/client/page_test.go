package client

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// TestPageMatchesPaginatedResponseEnvelope is the regression test for Page's
// one documented deviation from this package's own rule that responses decode
// into internal/models types (see client.go's package comment): Page is a
// hand-maintained mirror of internal/models.PaginatedResponse, not that type
// itself, because PaginatedResponse.Results is []any and cannot decode into
// anything usable on its own.
//
// Every other fixture in this package is a hand-written JSON literal, which
// means a change to PaginatedResponse's json tags (e.g. "total_count"
// renamed) would compile and pass every one of those tests on both sides
// while wi chats silently printed "No chats found." — the two types would
// have quietly stopped agreeing on the wire shape with nothing to catch it.
//
// This test closes that gap by using the real server type on one side: it
// marshals an actual models.PaginatedResponse (so PaginatedResponse's own
// json tags are what produce the bytes) and unmarshals the result into a Page
// (so Page's own tags are what consume them), then asserts the fields
// survived the round trip. A tag drift on either side breaks this test
// without needing a second, independent server-side test to catch it.
//
// It runs once per element type Page is aliased to, because the envelope tags
// are shared but the element decode is not: ChatMessagePage could stop
// decoding a message correctly while ChatPage stayed fine.
func TestPageMatchesPaginatedResponseEnvelope(t *testing.T) {
	t.Run("ChatPage", func(t *testing.T) {
		envelope := envelopeAround(t, models.Chat{Name: "deploy plan", UnreadCount: 3})

		var page ChatPage
		decodeEnvelope(t, envelope, &page)

		if len(page.Results) != 1 {
			t.Fatalf("len(page.Results) = %d, want 1", len(page.Results))
		}
		if page.Results[0].Name != "deploy plan" {
			t.Errorf("page.Results[0].Name = %q, want %q", page.Results[0].Name, "deploy plan")
		}
		if page.Results[0].UnreadCount != 3 {
			t.Errorf("page.Results[0].UnreadCount = %d, want 3", page.Results[0].UnreadCount)
		}
		if page.TotalCount != 42 {
			t.Errorf("page.TotalCount = %d, want 42", page.TotalCount)
		}
	})

	t.Run("ChatMessagePage", func(t *testing.T) {
		id := uuid.New()
		envelope := envelopeAround(t, models.ChatMessage{
			ID:              id,
			Message:         "the deploy is fine",
			Origin:          models.MessageOriginAssistant,
			GenerationModel: "gpt-test",
		})

		var page ChatMessagePage
		decodeEnvelope(t, envelope, &page)

		if len(page.Results) != 1 {
			t.Fatalf("len(page.Results) = %d, want 1", len(page.Results))
		}
		got := page.Results[0]
		if got.ID != id {
			t.Errorf("page.Results[0].ID = %s, want %s", got.ID, id)
		}
		if got.Message != "the deploy is fine" {
			t.Errorf("page.Results[0].Message = %q, want %q", got.Message, "the deploy is fine")
		}
		if got.Origin != models.MessageOriginAssistant {
			t.Errorf("page.Results[0].Origin = %q, want %q", got.Origin, models.MessageOriginAssistant)
		}
		if got.GenerationModel != "gpt-test" {
			t.Errorf("page.Results[0].GenerationModel = %q, want %q", got.GenerationModel, "gpt-test")
		}
		if page.TotalCount != 42 {
			t.Errorf("page.TotalCount = %d, want 42", page.TotalCount)
		}
	})
}

// envelopeAround marshals result into a real models.PaginatedResponse, so the
// bytes under test are produced by the server type's own json tags.
//
// The element is round-tripped through an untyped any first so it sits inside
// PaginatedResponse.Results the way a real server response would — as decoded
// JSON, not a typed Go value.
func envelopeAround(t *testing.T, result any) []byte {
	t.Helper()

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshaling %T: %v", result, err)
	}
	var asAny any
	if err := json.Unmarshal(encoded, &asAny); err != nil {
		t.Fatalf("unmarshaling %T into any: %v", result, err)
	}

	raw, err := json.Marshal(models.PaginatedResponse{
		Results:    []any{asAny},
		TotalCount: 42,
		Page:       1,
	})
	if err != nil {
		t.Fatalf("marshaling models.PaginatedResponse: %v", err)
	}
	return raw
}

func decodeEnvelope(t *testing.T, raw []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("unmarshaling into %T: %v", into, err)
	}
}
