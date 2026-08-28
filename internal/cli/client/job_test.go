package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestGetJobDecodesDraftDeltas(t *testing.T) {
	jobID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/job/" + jobID.String(); r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Write([]byte(`{
			"id": "` + jobID.String() + `",
			"status": "processing",
			"draft_deltas": ["Hello", " world"]
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	job, err := c.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob returned %v", err)
	}
	if job.Status != models.JobStatusProcessing {
		t.Errorf("job.Status = %q, want %q", job.Status, models.JobStatusProcessing)
	}
	if len(job.DraftDeltas) != 2 || job.DraftDeltas[1] != " world" {
		t.Errorf("job.DraftDeltas = %#v, want [Hello  world]", job.DraftDeltas)
	}
}

// TestGetJobDecodesResultID pins the field the turn engine reconciles
// against: result_id is set atomically with inference_complete
// (internal/agent/job_phase.go) and names the assistant message the turn
// produced. Losing it would silently strip the reply from every finished
// turn.
func TestGetJobDecodesResultID(t *testing.T) {
	resultID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"inference_complete","result_id":"` + resultID.String() + `"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	job, err := c.GetJob(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetJob returned %v", err)
	}
	if job.ResultID == nil {
		t.Fatal("job.ResultID = nil, want the assistant message id")
	}
	if *job.ResultID != resultID {
		t.Errorf("job.ResultID = %s, want %s", *job.ResultID, resultID)
	}
}

func TestGetJobSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Job not found"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	_, err := c.GetJob(context.Background(), uuid.New())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("GetJob error = %v, want an *APIError", err)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("apiErr.Status = %d, want 404", apiErr.Status)
	}
	if apiErr.Message != "Job not found" {
		t.Errorf("apiErr.Message = %q, want %q", apiErr.Message, "Job not found")
	}
}

func TestCancelJobPostsAndReturnsLatest(t *testing.T) {
	jobID := uuid.New()
	resultID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if want := "/job/" + jobID.String() + "/cancel"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Write([]byte(`{"status":"cancelled","result_id":"` + resultID.String() + `"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, Tokens{Access: "acc-1"})
	job, err := c.CancelJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("CancelJob returned %v", err)
	}
	if job.Status != models.JobStatusCancelled {
		t.Errorf("job.Status = %q, want %q", job.Status, models.JobStatusCancelled)
	}
	// The non-nil result_id is the whole point of the non-destructive cancel:
	// the partial draft was promoted to a real assistant message rather than
	// thrown away.
	if job.ResultID == nil || *job.ResultID != resultID {
		t.Errorf("job.ResultID = %v, want %s", job.ResultID, resultID)
	}
}
