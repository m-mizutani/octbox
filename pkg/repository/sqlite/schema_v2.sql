-- The viewer's own review of a pull request, and the latest comment
-- addressed to the viewer after the approval (Unix milliseconds; NULL when
-- none). Rows stored by version 1 get NULL and 0 until the next refresh by
-- ID fetches them again.
ALTER TABLE items ADD COLUMN viewer_approved_at INTEGER;
ALTER TABLE items ADD COLUMN viewer_review_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN viewer_addressed_at INTEGER;
