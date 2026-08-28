package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// TestCreateChatMessageSendsUserOrigin pins the origin the CLI is allowed to
// send. The server takes origin from the request body, so a client that
// forgot it — or let a caller choose — could post a turn the transcript
// attributes to the assistant.
func TestCreateChatMessageSendsUserOrigin(t *testing.T) {
	chatID := uuid.New()
	messageID := uuid.New()
	jobID := uuid.New()

	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if want := "/chat/" + chatID.String() + "/chat-message"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("request body was not JSON: %v (%s)", err, raw)
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":"` + messageID.String() + `","job_id":"` + jobID.String() + `","type":"chat_message"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	resp, err := c.CreateChatMessage(context.Background(), chatID, CreateMessageRequest{
		Message:        "review this",
		ClientTimezone: "Europe/Berlin",
	})
	if err != nil {
		t.Fatalf("CreateChatMessage returned %v", err)
	}

	if body["origin"] != string(models.MessageOriginUser) {
		t.Errorf("body origin = %v, want %q", body["origin"], models.MessageOriginUser)
	}
	if body["message"] != "review this" {
		t.Errorf("body message = %v, want %q", body["message"], "review this")
	}
	if body["client_timezone"] != "Europe/Berlin" {
		t.Errorf("body client_timezone = %v, want %q", body["client_timezone"], "Europe/Berlin")
	}
	if resp.ID != messageID {
		t.Errorf("resp.ID = %s, want %s", resp.ID, messageID)
	}
	if resp.JobID != jobID.String() {
		t.Errorf("resp.JobID = %q, want %q", resp.JobID, jobID)
	}
}

// TestCreateChatMessageOmitsEmptyTimezone keeps the CLI from asserting a
// timezone it does not know: an empty client_timezone must not reach the
// server as "", which would be neither the browser's behavior nor a valid
// IANA name.
func TestCreateChatMessageOmitsEmptyTimezone(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":"` + uuid.New().String() + `","job_id":"` + uuid.New().String() + `"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	if _, err := c.CreateChatMessage(context.Background(), uuid.New(), CreateMessageRequest{Message: "hi"}); err != nil {
		t.Fatalf("CreateChatMessage returned %v", err)
	}
	if _, present := body["client_timezone"]; present {
		t.Errorf("client_timezone present = %v, want it omitted when unset", body["client_timezone"])
	}
}

func TestGetChatMessageDecodesHydratedMessage(t *testing.T) {
	messageID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/chat/chat-message/" + messageID.String(); r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Write([]byte(`{
			"id": "` + messageID.String() + `",
			"message": "the deploy is fine",
			"origin": "Assistant",
			"generation_model": "gpt-test",
			"context_breakdown": {"version":1,"total_tokens":1234,"budget_tokens":8000}
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	msg, err := c.GetChatMessage(context.Background(), messageID)
	if err != nil {
		t.Fatalf("GetChatMessage returned %v", err)
	}
	if msg.Message != "the deploy is fine" {
		t.Errorf("msg.Message = %q, want %q", msg.Message, "the deploy is fine")
	}
	if msg.GenerationModel != "gpt-test" {
		t.Errorf("msg.GenerationModel = %q, want %q", msg.GenerationModel, "gpt-test")
	}
	if msg.ContextBreakdown == nil {
		t.Fatal("msg.ContextBreakdown = nil, want the per-turn token snapshot")
	}
	if msg.ContextBreakdown.TotalTokens != 1234 {
		t.Errorf("msg.ContextBreakdown.TotalTokens = %d, want 1234", msg.ContextBreakdown.TotalTokens)
	}
}

func TestGetActiveChatMessageJobReturnsJob(t *testing.T) {
	chatID, messageID, jobID := uuid.New(), uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/chat/" + chatID.String() + "/chat-message/" + messageID.String() + "/active-job"
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Write([]byte(`{"job_id":"` + jobID.String() + `","status":"processing"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	active, err := c.GetActiveChatMessageJob(context.Background(), chatID, messageID)
	if err != nil {
		t.Fatalf("GetActiveChatMessageJob returned %v", err)
	}
	if active == nil {
		t.Fatal("active = nil, want the in-flight job")
	}
	if active.JobID != jobID {
		t.Errorf("active.JobID = %s, want %s", active.JobID, jobID)
	}
	if active.Status != models.JobStatusProcessing {
		t.Errorf("active.Status = %q, want %q", active.Status, models.JobStatusProcessing)
	}
}

// TestGetActiveChatMessageJobNoContent pins the 204 path: "no job is running
// for that turn" is a normal answer, not an error, and must not surface as
// the "response was not JSON: empty response body" decode failure a 204 with
// a non-nil out would otherwise produce.
func TestGetActiveChatMessageJobNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	active, err := c.GetActiveChatMessageJob(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("GetActiveChatMessageJob returned %v, want nil for a 204", err)
	}
	if active != nil {
		t.Errorf("active = %+v, want nil for a 204", active)
	}
}

func TestListChatMessagesPassesLimit(t *testing.T) {
	chatID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/chat/" + chatID.String() + "/chat-message"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit = %q, want 5", got)
		}
		w.Write([]byte(`{"results":[{"message":"newest","origin":"Assistant"}],"total_count":9,"page":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	page, err := c.ListChatMessages(context.Background(), chatID, ListChatMessagesOptions{Limit: 5})
	if err != nil {
		t.Fatalf("ListChatMessages returned %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].Message != "newest" {
		t.Fatalf("page.Results = %+v, want one message", page.Results)
	}
	if page.TotalCount != 9 {
		t.Errorf("page.TotalCount = %d, want 9", page.TotalCount)
	}
}

// TestCreateChatSendsOnlyName pins the deliberate minimal body: marshaling a
// models.Chat instead would put explicit all-zero model_id/personality_id on
// the wire, defeating the server's fall back to the user's preferences.
func TestCreateChatSendsOnlyName(t *testing.T) {
	chatID := uuid.New()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat" {
			t.Errorf("%s %s, want POST /chat", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"` + chatID.String() + `","name":"wi 2026-08-28"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	chat, err := c.CreateChat(context.Background(), "wi 2026-08-28")
	if err != nil {
		t.Fatalf("CreateChat returned %v", err)
	}
	if chat.ID != chatID {
		t.Errorf("chat.ID = %s, want %s", chat.ID, chatID)
	}
	if len(body) != 1 || body["name"] != "wi 2026-08-28" {
		t.Errorf("request body = %#v, want only {\"name\": ...}", body)
	}
}
