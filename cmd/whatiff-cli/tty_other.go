//go:build !linux && !aix && !solaris && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package main

// flushPendingInput is a no-op on every GOOS not covered by tty_tcflsh.go or
// tty_bsd.go - Windows, Plan 9, js/wasm, and GNU/Hurd (which, unlike
// illumos, is not documented anywhere as inheriting another GOOS's build
// tags, has no zerrors_hurd_*.go file in golang.org/x/sys/unix defining
// either TCFLSH or TIOCFLUSH, and is not even a target this Go toolchain
// can cross-compile for - `GOOS=hurd go build` reports "unsupported
// GOOS/GOARCH pair" outright). Rather than pretending otherwise, this
// always reports that nothing was discarded - which is honest, since
// flushPendingInput's only caller (promptPassword) uses the returned bool
// solely to decide whether to print a warning about discarded input, and
// printing that warning here would be a lie.
//
// illumos is NOT handled here despite not having its own zerrors file in
// x/sys/unix: `go help buildconstraint` documents that GOOS=illumos matches
// every "solaris"-tagged file too, so tty_tcflsh.go's "linux || aix ||
// solaris" tag already covers it (confirmed by cross-compiling for
// GOOS=illumos) - see that file's doc comment for the full explanation.
//
// The three tty_*.go files' build tags are each other's exact negation, so
// every GOOS matches exactly one of them: a GOOS matching zero would fail to
// build (undefined: flushPendingInput), and one matching two would fail to
// build the other way (duplicate declaration).
func flushPendingInput(fd int) (bool, error) {
	return false, nil
}
