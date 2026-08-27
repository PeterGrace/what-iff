package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestLoadProfileMissingFromPopulatedFileReturnsErrNoCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}
	if err := store.Save("hosted", Credentials{AccessToken: "hosted-acc"}); err != nil {
		t.Fatal(err)
	}

	// The file exists and parses fine; it just has no "local" entry. This is
	// a different code path than the missing-file case above (the !ok branch
	// after a successful readAll, not the os.ErrNotExist branch).
	if _, err := store.Load("local"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("Load = %v, want ErrNoCredentials", err)
	}
}

func TestSaveOverwritesSameProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "old-acc", RefreshToken: "old-ref"}); err != nil {
		t.Fatal(err)
	}
	// This is the path exercised on every token refresh, not just login.
	want := Credentials{AccessToken: "new-acc", RefreshToken: "new-ref"}
	if err := store.Save("local", want); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestSaveOnNullFileDoesNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	// json.MarshalIndent of a nil map produces exactly this, and this store's
	// own Save can therefore write the input that used to crash its own
	// readAll/Save round trip via a nil-map assignment panic.
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := CredentialStore{Path: path}

	want := Credentials{AccessToken: "acc-1"}
	if err := store.Save("local", want); err != nil {
		t.Fatalf("Save returned %v, want nil (and no panic)", err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestSaveRecoversFromCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"local": {"access_tok`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := CredentialStore{Path: path}

	want := Credentials{AccessToken: "fresh-acc"}
	if err := store.Save("local", want); err != nil {
		t.Fatalf("Save returned %v, want nil", err)
	}

	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Errorf("quarantined file %s.corrupt not found: %v", path, err)
	}

	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadOnCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"local": {"access_tok`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := CredentialStore{Path: path}

	_, err := store.Load("local")
	if err == nil {
		t.Fatal("Load on a corrupt file returned nil error, want error")
	}
	if errors.Is(err, ErrNoCredentials) {
		t.Errorf("Load on a corrupt file = %v, want it NOT to be ErrNoCredentials", err)
	}
	if !strings.Contains(err.Error(), "wi login") {
		t.Errorf("error = %q, want it to name the remedy (`wi login`)", err.Error())
	}
}

func TestFailedSaveLeavesExistingFileIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits, so this write-protection test cannot fail the way it needs to")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "original"}); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	if err := store.Save("local", Credentials{AccessToken: "should-not-land"}); err == nil {
		t.Fatal("Save into a read-only directory returned nil error, want error")
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load("local")
	if err != nil {
		t.Fatalf("Load returned %v", err)
	}
	if got.AccessToken != "original" {
		t.Errorf("AccessToken = %q, want unchanged %q", got.AccessToken, "original")
	}
}

func TestSaveSweepsOnlyStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := CredentialStore{Path: path}

	if err := store.Save("local", Credentials{AccessToken: "acc-1"}); err != nil {
		t.Fatal(err)
	}

	// A fresh temp file looks like an in-flight Save's own file and must
	// survive the sweep; a stale one looks like leftovers from a killed
	// Save and must be removed.
	freshTmp := filepath.Join(dir, "credentials-fresh.tmp")
	if err := os.WriteFile(freshTmp, []byte("in flight"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleTmp := filepath.Join(dir, "credentials-stale.tmp")
	if err := os.WriteFile(staleTmp, []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleTempAge)
	if err := os.Chtimes(staleTmp, old, old); err != nil {
		t.Fatal(err)
	}

	if err := store.Save("local", Credentials{AccessToken: "acc-2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(freshTmp); err != nil {
		t.Errorf("fresh temp file was removed by the sweep: %v", err)
	}
	if _, err := os.Stat(staleTmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale temp file still exists (stat err = %v), want it swept", err)
	}
}

func TestSaveConcurrentSavesAllSucceed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := CredentialStore{Path: path}

	// Regression test for a sweep that unlinked a concurrent Save's own
	// in-flight temp file out from under it: the reviewer measured 7/50
	// concurrent Saves failing before the staleness check was added.
	const n = 30
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = store.Save("local", Credentials{AccessToken: fmt.Sprintf("acc-%d", i)})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Save #%d returned %v, want nil", i, err)
		}
	}
}
