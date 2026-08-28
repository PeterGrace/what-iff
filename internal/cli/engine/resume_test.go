package engine

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// TestResumeReattachesAndReplaysTheWholeDraft pins the crash-recovery path.
//
// Because draft_deltas is cumulative and is not consumed by being read, a
// client that restarts mid-reply re-reads the entire draft from its first
// poll — so a resumed turn shows the whole answer, not the tail that happened
// to arrive after the restart.
func TestResumeReattachesAndReplaysTheWholeDraft(t *testing.T) {
	chatID, messageID, jobID := uuid.New(), uuid.New(), uuid.New()
	resultID := uuid.New()

	api := &fakeAPI{
		activeFn: func(gotChat, gotMessage uuid.UUID) (*models.ActiveChatMessageJobResponse, error) {
			if gotChat != chatID {
				t.Errorf("active-job chat = %s, want %s", gotChat, chatID)
			}
			if gotMessage != messageID {
				t.Errorf("active-job message = %s, want %s", gotMessage, messageID)
			}
			return &models.ActiveChatMessageJobResponse{JobID: jobID, Status: models.JobStatusProcessing}, nil
		},
		jobFn: script(
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"already ", "streamed "}},
			models.Job{Status: models.JobStatusProcessing, DraftDeltas: []string{"already ", "streamed ", "plus new"}},
			models.Job{Status: models.JobStatusInferenceComplete, ResultID: &resultID},
		),
	}

	turn, err := testEngine(api).Resume(context.Background(), chatID, messageID)
	if err != nil {
		t.Fatalf("Resume returned %v", err)
	}
	if turn.JobID != jobID {
		t.Errorf("turn.JobID = %s, want %s", turn.JobID, jobID)
	}
	if turn.UserMessageID != messageID {
		t.Errorf("turn.UserMessageID = %s, want %s", turn.UserMessageID, messageID)
	}

	events := collect(t, turn)
	if got := deltaText(events); got != "already streamed plus new" {
		t.Errorf("streamed text = %q, want the whole draft replayed then continued", got)
	}
	if _, ok := last(t, events).(Done); !ok {
		t.Fatalf("last event = %T, want Done", last(t, events))
	}
}

// TestResumeWithNothingInFlight pins the 204 answer as a normal outcome: a
// restarted client asks this about every unanswered turn it finds, and "that
// one already finished" must be distinguishable from a real failure.
func TestResumeWithNothingInFlight(t *testing.T) {
	api := &fakeAPI{
		activeFn: func(uuid.UUID, uuid.UUID) (*models.ActiveChatMessageJobResponse, error) {
			return nil, nil
		},
		jobFn: func(int) (models.Job, error) {
			t.Error("polling started even though no job was in flight")
			return models.Job{}, nil
		},
	}

	_, err := testEngine(api).Resume(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("Resume error = %v, want ErrNoActiveTurn", err)
	}
}

// TestResumeSurfacesLookupFailure keeps a genuine error from being flattened
// into "nothing to resume", which would silently drop a turn the server is
// still working on.
func TestResumeSurfacesLookupFailure(t *testing.T) {
	api := &fakeAPI{
		activeFn: func(uuid.UUID, uuid.UUID) (*models.ActiveChatMessageJobResponse, error) {
			return nil, &client.APIError{Status: http.StatusInternalServerError, Message: "Failed to look up job"}
		},
	}

	_, err := testEngine(api).Resume(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("Resume returned nil error for a failed lookup")
	}
	if errors.Is(err, ErrNoActiveTurn) {
		t.Errorf("Resume error = %v, want the lookup failure, not ErrNoActiveTurn", err)
	}
}
