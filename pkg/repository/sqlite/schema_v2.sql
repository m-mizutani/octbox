-- The viewer's own review of a pull request. Rows stored by version 1 get 0
-- until the next refresh by ID (at most 10 minutes) fetches them again.
ALTER TABLE items ADD COLUMN viewer_approved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN viewer_review_requested INTEGER NOT NULL DEFAULT 0;
