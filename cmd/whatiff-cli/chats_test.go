package main

import (
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
