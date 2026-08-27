package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/theimaginaryfoundation/what-iff/internal/cli/client"
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
	if err := fs.Parse(args); err != nil {
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
		return enc.Encode(page)
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
		model := c.ModelName
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.Name, model, last, unread)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if notice := truncationNotice(page); notice != "" {
		fmt.Println(notice)
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
