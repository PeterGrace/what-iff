package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoCredentials means the profile has never been logged in to.
var ErrNoCredentials = errors.New("no stored credentials for profile")

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
		return nil, fmt.Errorf("parsing %s: %w", s.Path, err)
	}
	return all, nil
}

// Load returns the profile's credentials, or ErrNoCredentials.
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

// Save writes the profile's credentials, preserving other profiles.
//
// The write is synced to disk and made atomic (temp file plus rename) so an
// interrupted save cannot leave a truncated or zero-length file that locks
// the user out of every profile at once.
//
// Save is read-modify-write with no locking: two `wi` processes refreshing
// tokens in different terminals at the same moment can clobber each other's
// profile entry. That is tolerated rather than fixed here, since the file is
// re-derivable at any time by logging in again — last-writer-wins costs at
// most a re-login, not data loss.
func (s CredentialStore) Save(profile string, c Credentials) error {
	all, err := s.readAll()
	if err != nil {
		return err
	}
	all[profile] = c

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}

	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp credentials file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

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
