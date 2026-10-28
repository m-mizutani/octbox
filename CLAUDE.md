# CLAUDE.md

octbox is a terminal inbox of the GitHub issues and pull requests that involve
the user. It is a single Go binary; the layout follows m-mizutani/robin.

## Layers

- `pkg/cli/` — flags, environment variables, dependency wiring, the root
  command (the inbox) and `auth login` / `auth logout`. Each flag is defined
  once in a flag group of `pkg/cli/config` (`Logger`, `Storage`, `GitHub`,
  `Sync`). Default values come from the flag definitions.
- `pkg/controller/tui/` — the full-screen inbox (bubbletea). Turns key
  presses into `usecase.Inbox` calls and draws the result. Holds display state
  only (selection, filter input, last sync state); no business decision, no
  repository or GitHub calls.
- `pkg/usecase/` — business operations. `Auth` is the only component that
  reads, writes, or deletes the token. `Syncer` decides what to fetch from
  GitHub and is the only writer of items, category memberships, and the sync
  state (cursor, full sync period, rate limit wait, lease). `Inbox` lists
  items and is the only component that opens items and writes read marks for
  the user.
- `pkg/usecase/usecasetest/` — hand-written test doubles of the domain
  interfaces, shared by the tests of `pkg/usecase`, `pkg/controller` and
  `pkg/cli`. Imported only by tests.
- `pkg/domain/` — models (`model/`), the filter rule, and interfaces and
  errors (`interfaces/`). No I/O.
- `pkg/repository/sqlite/` — persistence in one SQLite file
  (`modernc.org/sqlite`, no cgo). Schema version in `PRAGMA user_version`.
- `pkg/adapter/{github,securestore,browser}/` — thin wrappers over GitHub
  (GraphQL, notifications REST, device flow), the OS secure storage
  (go-keyring on macOS and Linux, chosen by build tags), and the OS command
  that opens a URL. No business decisions.
- `pkg/utils/` — `logging`, `errutil`, `safe`, `async`.

## Sync rules

- Only open items of repositories that are not archived are stored. An item
  seen closed, merged, or in an archived repository, or no longer returned by
  GitHub (deleted repository), is deleted with its read mark.
- A regular sync searches items updated since the previous search start minus
  10 minutes. Every 6 hours a full sync searches all open items and replaces
  the category memberships; items left without a category are deleted. The
  subscribed category is never removed by a full sync. A full sync that fails
  with an API or network error falls back to a regular sync in the same run,
  and the process does not try a full sync again for 15 minutes.
- GraphQL requests answered with 502, 503 or 504 are retried after 2 and 5
  seconds (`Syncer.request`, which renews the lease before every attempt).
- A regular sync searches archived repositories too (pull requests closed
  right before archiving are seen only there); a full sync uses
  `archived:false`.
- CI completion does not change `updatedAt`, so pull requests with pending CI
  are fetched again every sync. Archiving or deleting a repository does not
  change it either, so every stored item is fetched again by ID every 10
  minutes and on every forced sync (`r`).
- The notifications API is called with `If-Modified-Since` and never earlier
  than `NextRunAt`, which already includes `X-Poll-Interval`.
- Processes sharing a database take turns through the lease in `sync_state`
  (`ClaimSync`, `BEGIN IMMEDIATE`). A running sync renews the lease before
  each GitHub request and each write (`RenewSync`), and saves its final state
  only while it still holds the lease (`ReleaseSync`); one HTTP request is
  limited to 30 seconds so a renewal is always due before the lease expires.
- The first sync of a database marks every stored item read. A first sync
  that fails resets its cursors, so the next one is a first sync again.

## Conventions

- Errors: `github.com/m-mizutani/goerr/v2`; wrap with `goerr.Wrap` and attach
  identifiers with `goerr.V`. Never attach the token. Discriminate with
  `errors.Is` / `errors.As` / `goerr.HasTag`, never by message text.
- An error that is returned is not logged. An error that stops propagating
  (`cli.Run`, the tail of a background goroutine, a TUI command) is recorded
  once with `errutil.Handle`.
- Logging: `logging.From(ctx)`; never the global `slog` functions. Logs go to
  the log file only, because the inbox owns stdout. `model.GitHubToken` values
  are redacted.
- Close resources with `safe.Close(ctx, c)`; start background goroutines with
  `async.Go`.
- Source comments and string literals are English. Unexport everything not used
  by another package; test-only access goes through `export_test.go`.
- This repository is public: use placeholder names (`example-org`, `alice`)
  in code, tests, and documents.

## Tests

- Assertions use `github.com/m-mizutani/gt`. Test packages are `{name}_test`;
  `xyz.go` is tested in `xyz_test.go`.
- No test calls GitHub: adapters use `httptest`, the secure storage uses
  `keyring.MockInit()`, and usecase tests use a SQLite file in `t.TempDir()`
  with the doubles of `usecasetest`. Tests assert stored data and the calls
  made to GitHub, not only the returned error.
- In tests, unset environment variables with `os.Unsetenv`: urfave/cli takes an
  empty variable as the flag value and skips the default.

## Checks before finishing

- `go vet ./...`, `gofmt -l .` (no output), `go test ./...`
- Update `README.md` when commands, flags, environment variables, key
  bindings, OAuth settings, or stored files change.
- Commit messages: one line, `<type>: <subject>`, English.
