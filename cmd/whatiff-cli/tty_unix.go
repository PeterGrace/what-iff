//go:build unix

package main

import "golang.org/x/sys/unix"

// flushPendingInput discards any input already queued by the terminal
// driver for fd, and reports whether it actually discarded something.
//
// This is the same trick sudo uses immediately before its own password
// prompt. The problem it defends against: pasting "alice\nhunter2\n" into a
// terminal arrives at the tty driver as one write, and the driver echoes
// every byte of it - including the password half - to the screen as it
// lands, because local echo is still on at that point; it is only turned off
// once promptPassword calls term.ReadPassword, by which time the password
// has already been echoed and is sitting in scrollback. In canonical mode
// the kernel delivers one line per read(), so the prompt sequence's username
// read consumes only "alice\n" and leaves "hunter2\n" queued in the driver's
// own buffer, invisible to anything done in Go-level buffering (bufio.Reader
// included) - only the kernel can discard it. TCIFLUSH does that, so the
// stale line can never be silently read back as the password once echo is
// off. It cannot undo the echo that already happened - the caller is
// responsible for warning about that separately, using this function's
// reported bool to know whether there was anything to warn about.
//
// Whether anything was pending is checked with a zero-timeout unix.Poll
// (POLLIN) immediately before the flush, not by reading: a read would
// consume bytes itself rather than leaving the flush to do it, and this
// package has no portable FIONREAD-equivalent available in
// golang.org/x/sys/unix to ask the kernel "how many bytes" directly across
// every GOOS this build tag covers. Poll only answers yes/no, which is all
// the caller needs to decide whether to print a warning.
func flushPendingInput(fd int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 0)
	if err != nil {
		return false, err
	}
	pending := n > 0 && fds[0].Revents&unix.POLLIN != 0

	if err := unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH); err != nil {
		return false, err
	}
	return pending, nil
}
