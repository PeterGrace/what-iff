package oneshot

import (
	"context"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/engine"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// fakeEngine replays a fixed event sequence. The poll loop has its own tests
// (internal/cli/engine); what these tests are about is what the renderer does
// with the events, so the events are given rather than generated.
type fakeEngine struct {
	events  []engine.Event
	sendErr error

	chatID uuid.UUID
	jobID  uuid.UUID

	sentTo  uuid.UUID
	sentReq engine.TurnRequest
	sends   int
}

func (f *fakeEngine) Send(_ context.Context, chatID uuid.UUID, req engine.TurnRequest) (*engine.Turn, error) {
	f.sends++
	f.sentTo = chatID
	f.sentReq = req
	if f.sendErr != nil {
		return nil, f.sendErr
	}

	events := make(chan engine.Event, len(f.events))
	for _, ev := range f.events {
		events <- ev
	}
	close(events)

	if f.chatID == uuid.Nil {
		f.chatID = chatID
	}
	if f.jobID == uuid.Nil {
		f.jobID = uuid.New()
	}
	return &engine.Turn{
		ChatID: f.chatID,
		JobID:  f.jobID,
		Events: events,
		Cancel: func() error { return nil },
	}, nil
}

func (f *fakeEngine) Resume(context.Context, uuid.UUID, uuid.UUID) (*engine.Turn, error) {
	return nil, engine.ErrNoActiveTurn
}

// fakeChats is a scripted chat directory.
type fakeChats struct {
	listed    []models.Chat
	listErr   error
	createErr error

	createdName string
	createdID   uuid.UUID
	lastSearch  string
	creates     int
}

func (f *fakeChats) ListChats(_ context.Context, opts client.ListChatsOptions) (client.ChatPage, error) {
	f.lastSearch = opts.Search
	if f.listErr != nil {
		return client.ChatPage{}, f.listErr
	}
	return client.ChatPage{Results: f.listed, TotalCount: len(f.listed)}, nil
}

func (f *fakeChats) CreateChat(_ context.Context, name string) (models.Chat, error) {
	f.creates++
	f.createdName = name
	if f.createErr != nil {
		return models.Chat{}, f.createErr
	}
	if f.createdID == uuid.Nil {
		f.createdID = uuid.New()
	}
	return models.Chat{ID: f.createdID, Name: name}, nil
}

// doneWith is the event tail a normal turn ends with.
func doneWith(message *models.ChatMessage) engine.Event {
	return engine.Done{Status: models.JobStatusComplete, Message: message}
}
