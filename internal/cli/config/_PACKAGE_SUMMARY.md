# Package: `internal/cli/config`

## Role

Configuration and credential storage for the `wi` CLI.

## Responsibilities

- Parse `config.toml` (profiles, default profile, download directory) and
  reject unknown keys rather than silently discarding them.
- Resolve a profile by name, falling back to a builtin `local` profile so the
  CLI works with no config file at all against `make run`/`make run-mock`.
- Locate the CLI's config directory and files (`paths.go`), following the OS
  user config directory (`XDG_CONFIG_HOME` on Linux).
- Store and load per-profile token pairs in a single `0600` JSON file,
  separate from `config.toml`.

## Key types and entry points

- `Config`, `Profile`, `Load`, `Config.Resolve(name) (string, Profile, error)`,
  `DefaultProfileName`, `DefaultAPIURL`.
- `Dir()`, `DefaultPath()`.
- `Credentials`, `CredentialStore`, `CredentialStore.Load`,
  `CredentialStore.Save`, `DefaultCredentialStore()`, `ErrNoCredentials`.

## Dependencies

- **Inbound:** `cmd/whatiff-cli` (the only caller today).
- **Outbound:** `github.com/BurntSushi/toml`; otherwise stdlib only
  (`encoding/json`, `os`, `path/filepath`, `net/url`).

## Non-obvious decisions

- **Credentials are a separate file from `config.toml`** so the config file
  stays safe to commit to a dotfiles repo or share — it never carries a
  secret.
- **A file store, not the OS keyring.** Headless servers and containers are a
  primary CLI use case, and keyring backends are absent or fail there. A
  keyring backend can be layered in later behind the same `CredentialStore`
  type without changing callers.
- **Atomic save.** `Save` writes to a temp file in the same directory, calls
  `Sync()`, then renames over the real path (`credentials.go:Save`) — an
  interrupted write can never leave a truncated file that locks the user out
  of every profile at once. This is not full durability (a rename that
  doesn't land after a power loss just reverts to the old contents), which is
  an acceptable trade-off for a file that's entirely re-derivable via
  `wi login`.
- **The `Chmod(0600)` on the temp file is load-bearing, not redundant.**
  `os.CreateTemp`'s requested 0600 is masked by the process umask like any
  other `open(2)`; under `umask 0200` it comes out 0400 without this call.
  Removing it breaks the exact-0600 guarantee on some machines and not
  others.
- **`readAll` normalizes a nil map.** `json.Unmarshal` of the literal `null`
  yields a nil map with no error; writing to that nil map in `Save` would
  panic. Every non-error return from `readAll` is a usable, non-nil map.
- **Corrupt files are quarantined on write, reported on read.** A file that
  fails to parse is renamed to `Path+".corrupt"` inside `Save` (the recovery
  path — `wi login` — goes through `Save`, so `Save` must be able to recover
  from what `Load` can only report as `errCorruptStore`).
- **The stale-temp sweep checks mtime.** `sweepStaleTemp` only removes
  `credentials-*.tmp` files older than `staleTempAge` (one hour). An earlier,
  unconditional sweep deleted a concurrent `Save`'s in-flight temp file out
  from under it, so the sync/close/rename that followed failed with `ENOENT`.
- **`Resolve` returns the resolved profile *name*,** not just the `Profile`,
  because that name is what keys the credential store — recomputing the
  fallback chain (asked name → `default_profile` → `local`) in `cmd/` would
  silently drift the moment `Resolve`'s chain changes.
- **A non-`local` profile with a missing `api_url` errors** rather than
  falling back to `DefaultAPIURL` (localhost). Falling back there would mean
  a password meant for a hosted profile gets sent to whatever happens to be
  listening on localhost.
- **`Load` rejects unknown TOML keys** (`md.Undecoded()`) precisely because a
  silently-ignored typo (e.g. `apiurl` instead of `api_url`) is what makes
  the localhost-fallback scenario above reachable in the first place.

## Testing

- `config_test.go` — profile resolution (including the builtin-local and
  no-`api_url` error paths), TOML loading, unknown-key rejection,
  `validateAPIURL` trimming/validation.
- `credentials_test.go` — round trip, per-profile independence, exact `0600`
  mode, `ErrNoCredentials`, corrupt-file quarantine and recovery, stale-temp
  sweep behavior (including that it does not touch a fresh in-flight temp
  file).
- `paths_test.go` — `Dir`/`DefaultPath` resolution.
- These behaviors were mutation-tested during milestone 1 review; the tests
  above are written to kill mutants in the atomic-save, corrupt-file, and
  profile-resolution logic, not just to hit each line once.

## Related

- [Architecture summary](../../../docs/ARCHITECTURE_SUMMARY.md) — CLI as a
  second client (Frontend section).
