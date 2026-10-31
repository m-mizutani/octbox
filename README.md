# octbox

octbox lists the GitHub issues and pull requests that involve you in your terminal, one per line. It keeps its own copy of them in a local database, so filtering and read marks work without GitHub's notification inbox.

An item is listed when it is open and at least one of the following applies:

- A review is requested from you, or you have reviewed it (pull requests)
- You are one of its assignees
- You created it
- You commented on it
- GitHub sends you notifications for it (you watch the repository, you participate, or you subscribed)

Closed and merged items, and items of archived or deleted repositories, are removed from the list.

A pull request whose latest review by you is an approval is hidden from the list, the unread count, and the herdr status. It is listed again, as unread, when any of the following happens after your approval:

- Your review is requested again (from you or from a team you belong to)
- Someone else writes a comment that mentions `@your-login`: a comment on the conversation, a review, or a comment on a line of the diff
- Someone else replies to a comment you wrote on a line of the diff
- Your approval is dismissed, or you submit a later review that is not an approval (a comment or a change request)

New commits, and comments that neither mention you nor reply to you, do not list it again. A mention of a team you belong to (`@org/team`) does not list it again either. octbox reads the comments of a hidden pull request when it is updated. If it cannot read them, it keeps the pull request as it was and tries again at the next sync. When more than 1,000 comments and reviews were added since your approval, only the latest 1,000 are checked.

After upgrading from an earlier version, pull requests you have already approved stay listed until octbox fetches every listed item again (every 10 minutes with the default interval); press `r` to fetch them now.

## Requirements

- macOS or Linux
- On Linux, a Secret Service provider (such as GNOME Keyring or KeePassXC) running on the D-Bus session bus. octbox stores the GitHub token there; on macOS it uses the login keychain.
- `open` (macOS) or `xdg-open` (Linux) to open items in the browser

## Install

```sh
go install github.com/m-mizutani/octbox@latest
```

## Usage

Run `octbox`. On the first run it asks you to authorize it on GitHub:

```
First, copy your one-time code: ABCD-1234
Then open https://github.com/login/device in your browser and enter the code.
Waiting for authorization...
```

After you enter the code and approve, the inbox opens and the first sync starts. Every item fetched by the first sync is marked read; from then on, an item becomes unread when it is updated on GitHub after you last opened it.

| Command | What it does |
| --- | --- |
| `octbox` | Opens the inbox. Logs in first when no token is stored. |
| `octbox auth login` | Logs in again, for example to switch accounts. Logging in as another account clears the local database. |
| `octbox auth logout` | Deletes the stored token. The local database is kept. |

### Keys

| Key | Action |
| --- | --- |
| `j` / `k`, `Ctrl-n` / `Ctrl-p`, `↓` / `↑` | Move the selection |
| `g` / `G` | Go to the first / last item |
| `Enter` | Open the item in the browser and mark it read |
| `/` | Filter: type words; items containing all of them are shown. `Enter` keeps the filter, `Esc` clears it |
| `m` | Toggle read / unread |
| `A` | Mark every listed item read |
| `r` | Sync now, also while waiting to retry a failed sync. Every listed item is fetched again, so items closed, archived, or deleted on GitHub disappear |
| `q`, `Ctrl-c` | Quit |

The filter matches the title, the repository, the number (`#482`), the author (`@alice`), and the type (`pr` or `issue`).

### Reading a row

```
● P ✓ approved example-org/api#482   Add retry to the webhook sender   @alice  12m
```

From the left: `●` unread, `P` pull request or `I` issue (gray for a draft), the CI status (`✓` passed, `✗` failed, `●` running), the review status (`approved`, `changes` requested, `review` required), the repository and number, the title, the author, and the time since the last update. An unread row has a bold title; a read row shows its repository, title, author, and time in gray, while the type, CI, and review statuses keep their colors.

### Running in herdr

When octbox runs in a pane of [Herdr TUI](https://herdr.dev/tui/), it appears in the herdr sidebar as the agent `octbox`. When a sync brings a new update to an item (the item is unread and its update time differs from what octbox saw at the previous sync) while you are not viewing that pane, its status turns to done (a filled dot); viewing the pane turns it back to idle. The status does not change for items that are already unread when octbox starts, for items you mark unread with `m`, or for items fetched again without a new update, such as pull requests whose CI is still running.

octbox also shows the number of unread items. In its row of the herdr sidebar, the agent name `octbox` becomes `octbox 2 unread`. In addition, `[octbox] 2 unread` becomes the name of its tab when its pane is the only one in the tab, and the name of its pane when the tab is split (the sidebar shows tab names but, by default, not pane names). The number is read from the database every 2 seconds, so it follows items you open or mark, syncs, and other octbox processes sharing the database. Splitting the tab, closing the other panes, or moving the pane to another tab moves the number, and the name it leaves is put back. A name you set while octbox runs is kept until the number changes. When octbox quits, the sidebar row is removed and the tab or pane gets its previous name back; herdr cannot turn a tab back to its automatic name (its number), so such a tab keeps the same number as a name of its own.

octbox detects herdr from the variables herdr sets in its panes (`HERDR_ENV=1`, `HERDR_PANE_ID`, and `HERDR_SOCKET_PATH`) and needs no setup; outside herdr it sends nothing. Requests to herdr's socket contain the pane and tab IDs, the name `octbox`, the state, and the names with the unread count; GitHub tokens and item contents are never sent. When octbox quits, it removes itself from the sidebar. herdr may play a sound when the status turns to done, as set by `[ui.sound]` in herdr's configuration.

## Options

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--interval` | `OCTBOX_INTERVAL` | `60s` | Time between syncs. At least `30s`. |
| `--db-path` | `OCTBOX_DB_PATH` | `~/.octbox/octbox.db` | Local database |
| `--log-file` | `OCTBOX_LOG_FILE` | `~/.octbox/octbox.log` | Log file (appended) |
| `--log-level` | `OCTBOX_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `--client-id` | `OCTBOX_CLIENT_ID` | octbox's OAuth app | Client ID of the OAuth app used for login |

The database and the log file are created with mode `0600` in a `0700` directory. The token is stored only in the OS secure storage (service `octbox`, account `github-token`), never in the database or the log.

## GitHub access

octbox logs in through the OAuth device flow of an OAuth app and requests the `repo` scope, which GitHub requires to search and read notifications of private repositories. It calls only the GraphQL API and the notifications API of github.com; GitHub Enterprise Server is not supported.

If an organization restricts OAuth app access, items in its private repositories do not appear until an organization owner approves the app. See [Approving OAuth apps for your organization](https://docs.github.com/en/organizations/managing-oauth-access-to-your-organizations-data/approving-oauth-apps-for-your-organization).

If an organization enforces SAML single sign-on, GitHub withholds its items from a token that was authorized without an active SSO session for that organization; octbox lists the other items and writes a warning to the log file. To include them, open `https://github.com/orgs/ORGANIZATION-NAME/sso` in a browser to start an SSO session, then run `octbox auth login` again. See [About authentication with SAML single sign-on](https://docs.github.com/en/authentication/authenticating-with-saml-single-sign-on/about-authentication-with-saml-single-sign-on).

### Using your own OAuth app

1. On GitHub, open Settings → Developer settings → OAuth Apps → New OAuth App.
2. Enter any Homepage URL and Authorization callback URL (the device flow does not use the callback).
3. After creating the app, check "Enable Device Flow" and save. Leave "Expire user access tokens" unchecked: octbox does not refresh tokens.
4. Pass the Client ID with `--client-id` or `OCTBOX_CLIENT_ID`. No client secret is needed.

### API usage

With the default 60-second interval, a sync fetches only items updated since the previous sync, and a full sync runs every 6 hours to notice items you no longer belong to (for example, a withdrawn review request). Every 10 minutes, and whenever you press `r`, octbox also fetches every listed item by its ID, because archiving or deleting a repository does not change the update time of its items. When a pull request you approved is updated, octbox also reads its comments since your approval, up to 10 pull requests per request. The GraphQL usage stays around 7–10% of the 5,000 points per hour limit. The notifications API is called with `If-Modified-Since`, so an unchanged response does not count against the rate limit. When fewer than 200 points remain, or GitHub asks octbox to wait, syncing pauses until the limit resets.

A request that GitHub answers with 502, 503, or 504 is sent again after 2 and then 5 seconds. If a full sync still fails, octbox runs the regular sync instead, so the list keeps updating and merged pull requests keep disappearing; the full sync is tried again 15 minutes later.

Several octbox processes may share one database; only one of them syncs at a time.

## License

Apache License 2.0
