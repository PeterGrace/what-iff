// Package client is the only place the WhatIff CLI speaks HTTP.
//
// Responses decode into internal/models types so the CLI and the server cannot
// disagree about a payload shape — the same single-chokepoint discipline the
// Playwright e2e SDK uses (web/app/e2e/sdk/client.ts).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// APIError is a non-2xx response. Message is the server's own wording, which
// the CLI surfaces verbatim rather than inventing its own.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("server returned %d", e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// Tokens is a token pair held in memory by a Client.
type Tokens struct {
	Access  string
	Refresh string
}

// Client talks to one WhatIff server.
type Client struct {
	BaseURL string
	HTTP    *http.Client

	// OnRefresh persists a newly refreshed token pair. Optional.
	OnRefresh func(Tokens) error

	mu       sync.Mutex
	tokens   Tokens
	inflight chan struct{}
}

// New builds a client for baseURL (for example http://localhost:8080/api).
func New(baseURL string, t Tokens) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		tokens:  t,
	}
}

// Tokens returns the client's current token pair.
func (c *Client) Tokens() Tokens {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens
}

func (c *Client) accessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens.Access
}

// doJSON performs one request with no refresh handling. Callers that need
// transparent re-auth use do (added in the next task).
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	// A caller that passes "chat" instead of "/chat" would otherwise silently
	// build c.BaseURL + path into a wrong URL (e.g. ".../apichat"); normalize
	// so a missing leading slash can't produce a wrong-but-valid request.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.accessToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s %s: %w", method, path, decodeAPIError(resp))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s %s response: %w", method, path, err)
	}
	return nil
}

// decodeAPIError turns a non-2xx response into an *APIError. A body that is not
// the server's JSON error envelope (a proxy's HTML 502, say) still yields a
// useful error carrying the status.
func decodeAPIError(resp *http.Response) error {
	apiErr := &APIError{Status: resp.StatusCode}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	// The body may exceed the 64KB cap; drain the remainder so the
	// connection's transport can be reused instead of forcing a close.
	_, _ = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return apiErr
	}
	var envelope models.ErrorResponse
	if json.Unmarshal(raw, &envelope) == nil {
		apiErr.Message = envelope.Message
		apiErr.Code = envelope.Code
		if apiErr.Message == "" {
			apiErr.Message = envelope.Error
		}
	}
	return apiErr
}
