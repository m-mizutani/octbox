CREATE TABLE items (
    id           TEXT    PRIMARY KEY,
    kind         TEXT    NOT NULL CHECK (kind IN ('issue', 'pull_request')),
    repo_owner   TEXT    NOT NULL,
    repo_name    TEXT    NOT NULL,
    number       INTEGER NOT NULL CHECK (number > 0),
    title        TEXT    NOT NULL,
    url          TEXT    NOT NULL,
    author       TEXT    NOT NULL,
    is_draft     INTEGER NOT NULL DEFAULT 0,
    ci_state     TEXT    NOT NULL DEFAULT 'none'
                 CHECK (ci_state IN ('none', 'success', 'failure', 'pending')),
    review_state TEXT    NOT NULL DEFAULT 'none'
                 CHECK (review_state IN ('none', 'approved', 'changes_requested', 'review_required')),
    updated_at   INTEGER NOT NULL,
    UNIQUE (repo_owner, repo_name, number)
);
CREATE INDEX items_updated_at ON items (updated_at DESC, id);
CREATE INDEX items_pr_ci ON items (kind, ci_state);
-- No state column: only open items are stored (SaveItems rejects others).

CREATE TABLE item_categories (
    item_id  TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    category TEXT NOT NULL CHECK (category IN ('review', 'assigned', 'created', 'commented', 'subscribed')),
    PRIMARY KEY (item_id, category)
);

CREATE TABLE read_marks (
    item_id         TEXT    PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    read_updated_at INTEGER NOT NULL
);

CREATE TABLE sync_state (
    id                         INTEGER PRIMARY KEY CHECK (id = 1),
    viewer                     TEXT    NOT NULL DEFAULT '',
    search_cursor              INTEGER,
    notification_cursor        INTEGER,
    notification_last_modified TEXT    NOT NULL DEFAULT '',
    last_full_sync_at          INTEGER,
    last_open_refresh_at       INTEGER,
    last_sync_at               INTEGER,
    next_run_at                INTEGER,
    lease_until                INTEGER,
    consecutive_failures       INTEGER NOT NULL DEFAULT 0,
    last_error                 TEXT    NOT NULL DEFAULT '',
    rate_remaining             INTEGER NOT NULL DEFAULT -1,
    rate_reset_at              INTEGER
);
