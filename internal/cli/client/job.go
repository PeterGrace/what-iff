package client

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// GetJob fetches one job by ID. The turn engine polls this endpoint while a
// reply generates; the server marks the response no-store precisely so a
// proxy cannot hand back a stale phase (internal/handlers/job/get.go).
func (c *Client) GetJob(ctx context.Context, jobID uuid.UUID) (models.Job, error) {
	var job models.Job
	if err := c.do(ctx, http.MethodGet, "/job/"+jobID.String(), nil, &job); err != nil {
		return models.Job{}, err
	}
	return job, nil
}

// CancelJob requests cancellation and returns the job as it stands afterwards.
//
// Cancellation is non-destructive: the server atomically promotes a non-empty
// draft to a real assistant message before marking the job cancelled, so the
// returned job's ResultID names a partial reply that was saved, not discarded
// (internal/datastore/job.go's FinalizeCancelledChatJobWithPartial). Cancelling
// an already-terminal job is not an error — the handler returns the job
// unchanged — so a racing cancel and completion is safe.
func (c *Client) CancelJob(ctx context.Context, jobID uuid.UUID) (models.Job, error) {
	var job models.Job
	if err := c.do(ctx, http.MethodPost, "/job/"+jobID.String()+"/cancel", nil, &job); err != nil {
		return models.Job{}, err
	}
	return job, nil
}
