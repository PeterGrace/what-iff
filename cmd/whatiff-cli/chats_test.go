package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestTruncationNotice(t *testing.T) {
	tests := []struct {
		name       string
		results    int
		totalCount int
		want       string
	}{
		{"nothing truncated, counts equal", 3, 3, ""},
		{"total unset (zero value), fewer than results is impossible so no notice", 3, 0, ""},
		{"truncated", 100, 347, "Showing 100 of 347 chats. Use --limit to see more."},
		{"truncated by one", 9, 10, "Showing 9 of 10 chats. Use --limit to see more."},
		{"empty page, nothing to truncate", 0, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := client.ChatPage{
				Results:    make([]models.Chat, tt.results),
				TotalCount: tt.totalCount,
			}
			got := truncationNotice(page)
			if got != tt.want {
				t.Errorf("truncationNotice() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHumanizeSince(t *testing.T) {
	tests := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"just now, no elapsed time", 0, "just now"},
		{"just now, well under a minute", 10 * time.Second, "just now"},
		{"a minute and a half rounds down to 1m", 90 * time.Second, "1m ago"},
		{"comfortably minutes", 45 * time.Minute, "45m ago"},
		{"an hour and a half rounds down to 1h", 90 * time.Minute, "1h ago"},
		{"comfortably hours", 5 * time.Hour, "5h ago"},
		{"a day and an hour rounds down to 1d", 25 * time.Hour, "1d ago"},
		{"comfortably days", 3 * 24 * time.Hour, "3d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := humanizeSince(time.Now().Add(-tt.ago))
			if got != tt.want {
				t.Errorf("humanizeSince(now-%v) = %q, want %q", tt.ago, got, tt.want)
			}
		})
	}
}

// TestNormalizeResultsForJSON_NilBecomesEmptySlice is the regression test
// for a nil page.Results (the server sent "results":null, or omitted the
// field - see internal/cli/client/chat_test.go's
// TestListChatsMissingResultsField/TestListChatsNullResultsField) still
// encoding as [] rather than null.
func TestNormalizeResultsForJSON_NilBecomesEmptySlice(t *testing.T) {
	page := normalizeResultsForJSON(client.ChatPage{TotalCount: 0})
	if page.Results == nil {
		t.Fatal("Results is still nil, want a non-nil empty slice")
	}
	if len(page.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(page.Results))
	}

	data, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(data), `"results":null`) {
		t.Errorf("encoded page still has null results: %s", data)
	}
	if !strings.Contains(string(data), `"results":[]`) {
		t.Errorf("encoded page does not have an empty-array results: %s", data)
	}
}

// TestNormalizeResultsForJSON_LeavesNonNilResultsAlone guards against an
// overzealous fix that replaces every Results slice rather than only a nil
// one - a real chat list must survive unchanged.
func TestNormalizeResultsForJSON_LeavesNonNilResultsAlone(t *testing.T) {
	want := []models.Chat{{Name: "deploy plan"}}
	page := normalizeResultsForJSON(client.ChatPage{Results: want, TotalCount: 1})
	if len(page.Results) != 1 || page.Results[0].Name != "deploy plan" {
		t.Errorf("Results = %+v, want unchanged %+v", page.Results, want)
	}
}

func TestSanitizeCell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text unchanged", "deploy plan", "deploy plan"},
		{"embedded tab collapsed to a single space", "deploy\tplan", "deploy plan"},
		{"embedded newline collapsed to a single space", "deploy\nplan", "deploy plan"},
		{"ANSI escape byte stripped, literal text survives", "\x1b[31mdanger\x1b[0m", "[31mdanger[0m"},
		{"leading and trailing whitespace trimmed", "  spaced  ", "spaced"},
		{"empty string stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeCell(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeCell(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
