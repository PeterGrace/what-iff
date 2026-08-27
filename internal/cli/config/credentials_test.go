package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	want := Credentials{AccessToken: "acc-1", RefreshToken: "ref-1", Username: "pete"}
	if err := store.Save("local", want); err != nil {
		t.Fatalf("Save returned %v", err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestCredentialProfilesAreIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "local-acc"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("hosted", Credentials{AccessToken: "hosted-acc"}); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "local-acc" {
		t.Errorf("saving hosted clobbered local: got %q", got.AccessToken)
	}
}

func TestCredentialFileIsNotWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}
	if err := store.Save("local", Credentials{AccessToken: "secret"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials file mode = %o, want 600", perm)
	}
}

func TestLoadMissingProfileReturnsErrNoCredentials(t *testing.T) {
	store := CredentialStore{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if _, err := store.Load("local"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("Load = %v, want ErrNoCredentials", err)
	}
}
