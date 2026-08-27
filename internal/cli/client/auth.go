package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// Login exchanges credentials for a token pair and stores it on the client.
// usernameOrEmail matches the server, which accepts either.
//
// Login calls doJSON directly rather than do: there is no token to refresh
// yet, so the 401-retry wrapper has nothing to add here and would only add a
// pointless extra accessToken() read.
func (c *Client) Login(ctx context.Context, usernameOrEmail, password string) (models.UserResponse, error) {
	req := models.UserLoginRequest{Username: usernameOrEmail, Password: password}
	var resp models.LoginResponse
	if err := c.doJSON(ctx, http.MethodPost, "/user/login", req, &resp); err != nil {
		return models.UserResponse{}, err
	}
	c.setTokens(Tokens{Access: resp.AccessToken, Refresh: resp.RefreshToken})
	return resp.User, nil
}

// setTokens replaces the client's token pair under the mutex.
func (c *Client) setTokens(t Tokens) {
	c.mu.Lock()
	c.tokens = t
	c.mu.Unlock()
}

// refreshTokens exchanges the refresh token for a new pair. It reports
// whether the exchange itself succeeded, separately from any error.
//
// The distinction matters: a failed *persist* (read-only credentials file,
// full disk) still leaves the client holding usable tokens in memory, so the
// caller (do) should retry the request rather than fail it. Only a failed
// *exchange* means the request cannot succeed. So:
//
//   - exchange fails: (false, err).
//   - exchange succeeds, no OnRefresh, or OnRefresh returns nil: (true, nil).
//   - exchange succeeds but OnRefresh errors: (true, wrappedErr) — the new
//     tokens are adopted in memory first, then OnRefresh is called, so the
//     error reflects only the persist step.
//   - another goroutine already refreshed (see staleAccess below): (true, nil).
//
// staleAccess is the access token the caller saw fail. If the client's token
// has already moved on, another goroutine refreshed first and this returns
// immediately — so a burst of concurrent 401s produces exactly one refresh
// request rather than a stampede against /user/refresh.
//
// refreshTokens calls doJSON, never do: do calls refreshTokens on a 401, so
// going through do here would recurse the moment a refresh token itself came
// back expired.
func (c *Client) refreshTokens(ctx context.Context, staleAccess string) (bool, error) {
	c.mu.Lock()
	if c.tokens.Access != staleAccess {
		c.mu.Unlock()
		return true, nil
	}
	if existing := c.inflight; existing != nil {
		c.mu.Unlock()
		select {
		case <-existing:
			// The leader's outcome isn't stored anywhere — inferring it from
			// whether the token moved avoids a second piece of shared state
			// (an error channel/field) that would itself need synchronizing
			// and could race with the leader clearing c.inflight. A changed
			// access token is proof the leader's exchange succeeded; if it
			// didn't succeed, the token is still staleAccess and the waiter
			// correctly reports failure rather than retrying with a token
			// that's known to be dead.
			c.mu.Lock()
			changed := c.tokens.Access != staleAccess
			c.mu.Unlock()
			return changed, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	done := make(chan struct{})
	c.inflight = done
	refresh := c.tokens.Refresh
	c.mu.Unlock()

	var resp models.LoginResponse
	err := c.doJSON(ctx, http.MethodPost, "/user/refresh", models.RefreshTokenRequest{RefreshToken: refresh}, &resp)

	c.mu.Lock()
	c.inflight = nil
	var updated Tokens
	if err == nil {
		updated = Tokens{Access: resp.AccessToken, Refresh: resp.RefreshToken}
		c.tokens = updated
	}
	c.mu.Unlock()
	close(done)

	if err != nil {
		return false, err
	}

	// The exchange succeeded and the new tokens are already live in memory
	// (set above, before OnRefresh runs) — so a persist failure below is
	// reported through err but must not flip the reported bool to false.
	if c.OnRefresh != nil {
		if persistErr := c.OnRefresh(updated); persistErr != nil {
			return true, fmt.Errorf("persisting refreshed tokens: %w", persistErr)
		}
	}
	return true, nil
}
