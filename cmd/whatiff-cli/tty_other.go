//go:build !unix

package main

// flushPendingInput is a no-op on non-unix platforms (Windows): there is no
// TCFLSH-equivalent wired up here, so a paste landing on the terminal
// immediately before the password prompt is not defended against on those
// platforms. Rather than pretending otherwise, this always reports that
// nothing was discarded - which is honest, since flushPendingInput's only
// caller (promptPassword) uses the returned bool solely to decide whether to
// print a warning about discarded input, and printing that warning here
// would be a lie.
func flushPendingInput(fd int) (bool, error) {
	return false, nil
}
