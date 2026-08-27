package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrNoCredentials means the profile has never been logged in to.
var ErrNoCredentials = errors.New("no stored credentials for profile")

// errCorruptStore marks a readAll failure as unparsable JSON, as opposed to
// an I/O error, so Save can tell the two apart: a corrupt file is something
// Save can quarantine and recover from, an I/O error is not.
var errCorruptStore = errors.New("credentials file is corrupt")

// staleTempAge is how old a leftover temp file must be before Save removes it.
// A temp file belonging to an in-flight Save is milliseconds old, so the
// generous threshold is what keeps cleanup from unlinking a concurrent
// writer's file out from under it — the rename would then fail with ENOENT
// after its write, sync, and close had all succeeded.
const staleTempAge = time.Hour

// Credentials is one profile's stored token pair.
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Username     string `json:"username"`
}

// CredentialStore persists Credentials per profile in a 0600 JSON file.
//
// This is deliberately a file rather than the OS keyring: headless servers and
// containers are a primary use case for a CLI, and keyring backends are absent
// or fail there. A keyring backend can be added later behind the same type.
type CredentialStore struct {
	Path string
}

// DefaultCredentialStore locates the store alongside config.toml.
func DefaultCredentialStore() (CredentialStore, error) {
	dir, err := Dir()
	if err != nil {
		return CredentialStore{}, err
	}
	return CredentialStore{Path: filepath.Join(dir, "credentials.json")}, nil
}

// readAll returns every profile's credentials from disk, or an empty
// (never nil) map if the file does not exist. A parse failure is wrapped in
// errCorruptStore, naming the remedy, so callers can tell "file is malformed"
// apart from other I/O errors and react differently — Save quarantines and
// recovers, Load reports the failure as-is.
func (s CredentialStore) readAll() (map[string]Credentials, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Credentials{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	all := map[string]Credentials{}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parsing %s: %w: %w (delete this file or run `wi login` to reset stored logins)", s.Path, errCorruptStore, err)
	}
	// A file containing literal `null` unmarshals to a nil map with no error,
	// and writing to a nil map panics. Normalize so every non-error return is
	// a usable map — json.MarshalIndent of a nil map emits exactly "null", so
	// this store can otherwise generate the input that crashes it.
	if all == nil {
		all = map[string]Credentials{}
	}
	return all, nil
}

// Load returns the profile's credentials, or ErrNoCredentials.
//
// A corrupt file is reported as-is rather than folded into ErrNoCredentials:
// masking corruption as "never logged in" would send the user chasing the
// wrong problem, and errors.Is(err, ErrNoCredentials) is false for it.
func (s CredentialStore) Load(profile string) (Credentials, error) {
	all, err := s.readAll()
	if err != nil {
		return Credentials{}, err
	}
	c, ok := all[profile]
	if !ok {
		return Credentials{}, ErrNoCredentials
	}
	return c, nil
}

// sweepStaleTemp best-effort removes credentials-*.tmp files older than
// staleTempAge from a prior Save that was killed (e.g. SIGKILL) before its
// deferred os.Remove could run. Each holds a refresh token valid for 14 days;
// they're 0600 so not a disclosure risk, but they'd otherwise accumulate
// invisibly. The age check is what keeps this from unlinking a concurrent
// Save's in-flight temp file out from under it. Errors — from the glob, from
// stat, from the remove itself — are ignored: a failed sweep must never fail
// the save it's cleaning up after.
func (s CredentialStore) sweepStaleTemp() {
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(s.Path), "credentials-*.tmp"))
	if err != nil {
		return
	}
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || time.Since(fi.ModTime()) < staleTempAge {
			continue
		}
		_ = os.Remove(m)
	}
}

// Save writes the profile's credentials, preserving other profiles.
//
// The write is synced to disk and made atomic (temp file plus rename) so an
// interrupted save cannot leave a truncated or zero-length file that locks
// the user out of every profile at once. This does not make the save fully
// durable: after a power loss the rename itself may not have landed, in
// which case the file's previous contents come back — costing a re-login,
// which is proportionate for a file that is entirely re-derivable that way.
//
// Save is read-modify-write with no locking: two `wi` processes refreshing
// tokens in different terminals at the same moment can clobber each other's
// profile entry. That is tolerated rather than fixed here, since the file is
// re-derivable at any time by logging in again — last-writer-wins costs at
// most a re-login, not data loss.
//
// A file that fails to parse is quarantined (renamed to Path+".corrupt",
// keeping the most recent such file around for forensics — a second
// corruption overwrites the first) rather than left blocking every future
// save: the natural recovery path, `wi login`, goes through Save, so Save
// has to be able to recover from corruption Load can only report.
//
// Save replaces whatever is at Path via atomic rename; if Path is a symlink,
// the rename replaces the symlink itself with a regular file rather than
// writing through it, so symlinking this file into a dotfiles repo will stop
// tracking new tokens after the first save.
func (s CredentialStore) Save(profile string, c Credentials) error {
	s.sweepStaleTemp()

	all, err := s.readAll()
	if errors.Is(err, errCorruptStore) {
		if rerr := os.Rename(s.Path, s.Path+".corrupt"); rerr != nil {
			return fmt.Errorf("quarantining corrupt credentials file: %w", rerr)
		}
		all = map[string]Credentials{}
	} else if err != nil {
		return err
	}
	all[profile] = c

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}

	dir := filepath.Dir(s.Path)
	// 0700 applies only when this call actually creates the directory; a
	// pre-existing directory keeps whatever mode it already had.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp credentials file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// os.CreateTemp's requested 0600 is masked by the process umask like any
	// other open(2): under a restrictive umask such as 0200 the file would
	// come out 0400 without this Chmod, silently failing
	// TestCredentialFileIsNotWorldReadable's exact-0600 assertion on some
	// machines and not others. This call is load-bearing, not belt-and-braces.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("securing temp credentials file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing credentials: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing credentials: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return fmt.Errorf("replacing %s: %w", s.Path, err)
	}
	return nil
}
