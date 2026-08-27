package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// defaultChatLimit overrides the server's page size, which is 10
// (internal/handlers/chat/chat.go:167). Inheriting that default would silently
// truncate the listing and look like missing data.
const defaultChatLimit = 100

func runChats(ctx context.Context, profileName string, asJSON bool, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	archived := fs.Bool("archived", false, "list archived chats instead of active ones")
	search := fs.String("search", "", "filter by name or checkpoint summary")
	limit := fs.Int("limit", defaultChatLimit, "maximum chats to return")
	if err := parseSubFlags(fs, args); err != nil {
		return err
	}

	sess, err := newSession(profileName)
	if err != nil {
		return err
	}

	page, err := sess.client.ListChats(ctx, client.ListChatsOptions{
		Archived: *archived,
		Search:   *search,
		Limit:    *limit,
	})
	if err != nil {
		return err
	}

	if asJSON {
		// The whole ChatPage, not a bare array of results: TotalCount is the
		// entire reason ChatPage exists over []models.Chat (see its doc
		// comment in internal/cli/client/chat.go) - a script parsing a bare
		// array has no way to tell a capped listing from the complete one,
		// which is exactly the ambiguity TotalCount exists to resolve.
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(normalizeResultsForJSON(page))
	}

	if len(page.Results) == 0 {
		fmt.Println("No chats found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMODEL\tLAST MESSAGE\tUNREAD")
	for _, c := range page.Results {
		last := "-"
		if c.LastMessageTime != nil {
			last = humanizeSince(*c.LastMessageTime)
		}
		unread := ""
		if c.UnreadCount > 0 {
			unread = fmt.Sprintf("%d", c.UnreadCount)
		}
		// Name and ModelName are server-controlled, user-supplied text
		// reaching a terminal - sanitizeCell strips it the same way
		// internal/cli/client's snippet() already does for error bodies
		// (see sanitizeCell's doc comment for why this doesn't just import
		// that function instead).
		name := sanitizeCell(c.Name)
		model := sanitizeCell(c.ModelName)
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, model, last, unread)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// stderr, not stdout: this is an advisory line about the listing, not
	// part of it. `wi chats | grep -c .` (or any other line-counting or
	// line-parsing consumer of the table) must not have to know to skip a
	// trailing sentence mixed into its data. --json mode never reaches this
	// line at all (it returns above) since TotalCount is already in that
	// payload for a script to check itself.
	if notice := truncationNotice(page); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}
	return nil
}

// truncationNotice reports whether page's listing was capped by the caller's
// limit and, if so, the line to print about it - e.g. "Showing 100 of 347
// chats. Use --limit to see more." It returns "" when nothing was cut off.
//
// Pulled out of the print loop so the one thing TotalCount exists for (see
// ChatPage's doc comment in internal/cli/client/chat.go) has a test pinning
// it, rather than being a couple of inline lines a future edit could delete
// without anything noticing.
func truncationNotice(page client.ChatPage) string {
	if page.TotalCount <= len(page.Results) {
		return ""
	}
	return fmt.Sprintf("Showing %d of %d chats. Use --limit to see more.", len(page.Results), page.TotalCount)
}

// normalizeResultsForJSON replaces a nil page.Results with an empty, non-nil
// slice before it is handed to json.Marshal/json.Encoder.
//
// encoding/json encodes a nil slice as the JSON literal null, not []. The
// server can send "results" as null, or omit it entirely - either decodes
// into a nil Results (see internal/cli/client/chat_test.go's
// TestListChatsMissingResultsField/TestListChatsNullResultsField) - and
// without this, `wi chats --json` on an empty account emits
// {"results":null,...}. A script doing `jq '.results[]'` or a naive
// for-range loop over the decoded field breaks on null in a way it would not
// on [], which is a needless trap for a value that means exactly the same
// thing either way: no chats.
func normalizeResultsForJSON(page client.ChatPage) client.ChatPage {
	if page.Results == nil {
		page.Results = []models.Chat{}
	}
	return page
}

// sanitizeCell strips non-printable characters from s and collapses any
// whitespace runs (including a tab or newline embedded in a chat name) to a
// single space, so server-controlled text is safe to print unescaped into a
// tabwriter cell.
//
// This mirrors internal/cli/client's snippet() (client.go), which solves the
// identical problem for a raw HTTP error body reaching a terminal: without
// stripping, a chat name containing an ANSI/OSC escape sequence could forge
// terminal output or rename the tab, and an embedded tab or newline would
// shift every column after it in the table. It is reimplemented here rather
// than imported because the client package is a thin HTTP transport with no
// terminal-rendering concern of its own - that split is deliberate, not an
// oversight, so this comment is what keeps the two implementations in step
// if one of them changes.
func sanitizeCell(s string) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, collapsed)
}

// humanizeSince renders a coarse relative time. Chat listings are scanned, not
// read precisely, so minutes-level granularity is enough and much easier to
// scan than a timestamp.
func humanizeSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
