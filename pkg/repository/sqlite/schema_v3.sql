-- The user's archive of an item: the item's updated_at (Unix milliseconds)
-- shown when it was archived. The item is archived while items.updated_at
-- is not later than archived_updated_at.
CREATE TABLE archive_marks (
    item_id             TEXT    PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    archived_updated_at INTEGER NOT NULL
);
