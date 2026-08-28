//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package main

import "golang.org/x/sys/unix"

// flushBSDInputQueue selects TIOCFLUSH's input-queue argument: the BSD
// family's TIOCFLUSH ioctl takes a pointer to an int bitmask naming which
// queue(s) to flush (0 means both), built from the historic BSD
// sys/fcntl.h FREAD/FWRITE bits. golang.org/x/sys/unix does not export
// either constant on darwin/dragonfly/freebsd/netbsd/openbsd (checked by
// grepping the whole module, not just this platform's generated file), so
// the literal is spelled out here instead of a symbolic name that does not
// exist. FREAD is 0x1 on every BSD-derived system, a value that predates
// and is far more stable than anything this file needs to track.
const flushBSDInputQueue = 0x1

// flushPendingInput discards any input already queued by the terminal
// driver for fd, and reports whether it actually discarded something.
//
// See tty_tcflsh.go's doc comment for the full rationale (the sudo-style
// paste defense, and why both the poll result and the flush attempt are
// unconditional and independent). This file exists because
// golang.org/x/sys/unix does not export TCFLSH for darwin, dragonfly,
// freebsd, netbsd, or openbsd - confirmed by grepping the module's
// generated zerrors_<goos>_<goarch>.go files for every arch this repo might
// target, and by cross-compiling for each GOOS, not by trusting a header
// reference. These platforms instead export TIOCFLUSH (value 0x80047410 on
// every one of them in this module), which - unlike TCFLSH's IoctlSetInt -
// takes its argument by pointer: IoctlSetPointerInt, not IoctlSetInt.
func flushPendingInput(fd int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, pollErr := unix.Poll(fds, 0)
	pending := pollErr == nil && n > 0 && fds[0].Revents&unix.POLLIN != 0

	flushErr := unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, flushBSDInputQueue)

	err := flushErr
	if err == nil {
		err = pollErr
	}
	return pending, err
}
