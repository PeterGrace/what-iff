//go:build linux || aix || solaris

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
// This file is one of three platform-specific implementations, split by
// which ioctl request constant golang.org/x/sys/unix actually exports for
// the target GOOS - verified by cross-compiling for every GOOS Go supports,
// not by trusting a header reference or a filename grep (both misled an
// earlier version of this split - see the note on the file name below and
// the illumos note further down). TCFLSH is exported for linux (every arch
// this repo might target), aix, and solaris. tty_bsd.go covers the
// platforms where x/sys/unix instead exports TIOCFLUSH; tty_other.go is the
// no-op fallback for every remaining GOOS.
//
// This file is deliberately NOT named tty_linux.go. A source file whose name
// ends in "_<goos>.go" gets an implicit build constraint for that GOOS,
// ANDed together with whatever an explicit "//go:build" line says - so
// tty_linux.go with a "//go:build linux || aix || solaris" line would still
// only ever build for linux, silently excluding aix and solaris despite the
// tag naming them (confirmed by cross-compiling: both failed with
// "undefined: flushPendingInput" until this file was renamed). tty_bsd.go
// and tty_other.go are unaffected only because "bsd" and "other" are not
// recognized GOOS names Go's file-name matching looks for.
//
// illumos is covered by this file too, via its "linux || aix || solaris"
// tag, even though no zerrors_illumos_*.go file exists anywhere in this
// module: `go help buildconstraint` documents that GOOS=illumos matches
// every "solaris"-tagged file in addition to its own, and cross-compiling
// confirms unix.TCFLSH really does resolve for GOOS=illumos as a result. A
// filename-only search for "illumos" (which is how the platform set here
// was first drafted) misses this and wrongly concludes illumos has no
// working ioctl constant at all.
//
// Whether anything was pending is checked with a zero-timeout unix.Poll
// (POLLIN) immediately before the flush, not by reading: a read would
// consume bytes itself rather than leaving the flush to do it, and this
// package has no portable FIONREAD-equivalent available in
// golang.org/x/sys/unix to ask the kernel "how many bytes" directly across
// every GOOS this build tag covers. Poll only answers yes/no, which is all
// the caller needs to decide whether to print a warning.
//
// The poll result and the flush are independent facts, and both are
// attempted unconditionally: an earlier version returned early on a Poll
// error (so a stray EINTR would skip the flush entirely, leaving a stale
// password line in place) and discarded a true "pending" the moment the
// ioctl itself failed (so "something was pending but the flush failed" was
// indistinguishable from "nothing was pending" - silently accepting the
// stale pasted line as the password with no warning at all, the exact
// failure this function exists to prevent). pending now reflects only what
// Poll observed, err reports whichever step failed (flush takes priority,
// since a caller that only checks err for logging purposes should see the
// more actionable failure), and the flush always runs regardless of whether
// Poll succeeded.
func flushPendingInput(fd int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, pollErr := unix.Poll(fds, 0)
	pending := pollErr == nil && n > 0 && fds[0].Revents&unix.POLLIN != 0

	flushErr := unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)

	err := flushErr
	if err == nil {
		err = pollErr
	}
	return pending, err
}
