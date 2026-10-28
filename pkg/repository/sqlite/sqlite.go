// Package sqlite stores items, category memberships, read marks, and the
// sync state in one SQLite file.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/m-mizutani/goerr/v2"
	_ "modernc.org/sqlite"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

// Repository implements interfaces.Repository.
type Repository struct {
	db *sql.DB
}

var _ interfaces.Repository = (*Repository)(nil)

// New opens the database at path, creating the parent directory (0700) and
// the file (0600) when they do not exist, and migrates the schema.
func New(ctx context.Context, path string) (*Repository, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, goerr.Wrap(err, "failed to create the database directory", goerr.V("path", path))
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create the database file", goerr.V("path", path))
	}
	if err := f.Close(); err != nil {
		return nil, goerr.Wrap(err, "failed to close the database file", goerr.V("path", path))
	}

	// _txlock=immediate starts every transaction with BEGIN IMMEDIATE, so
	// ClaimSync holds the write lock between reading and updating the lease.
	dsn := "file:" + path +
		"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to open the database", goerr.V("path", path))
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, goerr.Wrap(err, "failed to migrate the database", goerr.V("path", path))
	}
	return &Repository{db: db}, nil
}

func (x *Repository) Close() error {
	return x.db.Close()
}

func toMillis(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func fromMillis(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64).UTC()
}

func (x *Repository) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return goerr.Wrap(err, "failed to begin a transaction")
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return goerr.Wrap(err, "failed to commit")
	}
	return nil
}

func (x *Repository) SaveItems(ctx context.Context, items []model.Item) error {
	for i := range items {
		if err := items[i].Validate(); err != nil {
			return goerr.Wrap(err, "failed to save items")
		}
		if items[i].State != model.ItemStateOpen || items[i].RepoArchived {
			return goerr.New("only open items of repositories that are not archived can be saved",
				goerr.V("item_id", items[i].ID))
		}
	}

	return x.inTx(ctx, func(tx *sql.Tx) error {
		for _, item := range items {
			// An issue transferred to another repository keeps its number
			// slot here under a new ID; the old row is removed first.
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM items WHERE repo_owner = ? AND repo_name = ? AND number = ? AND id <> ?",
				item.Repo.Owner, item.Repo.Name, item.Number, string(item.ID)); err != nil {
				return goerr.Wrap(err, "failed to delete the old row", goerr.V("item_id", item.ID))
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO items (id, kind, repo_owner, repo_name, number, title, url, author, is_draft, ci_state, review_state, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    kind = excluded.kind, repo_owner = excluded.repo_owner, repo_name = excluded.repo_name,
    number = excluded.number, title = excluded.title, url = excluded.url, author = excluded.author,
    is_draft = excluded.is_draft, ci_state = excluded.ci_state, review_state = excluded.review_state,
    updated_at = excluded.updated_at`,
				string(item.ID), string(item.Kind), item.Repo.Owner, item.Repo.Name, item.Number,
				item.Title, item.URL, item.Author, item.IsDraft, string(item.CI), string(item.Review),
				item.UpdatedAt.UnixMilli()); err != nil {
				return goerr.Wrap(err, "failed to save an item", goerr.V("item_id", item.ID))
			}
		}
		return nil
	})
}

func (x *Repository) DeleteItems(ctx context.Context, ids []model.ItemID) error {
	if len(ids) == 0 {
		return nil
	}
	return x.inTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, "DELETE FROM items WHERE id = ?", string(id)); err != nil {
				return goerr.Wrap(err, "failed to delete an item", goerr.V("item_id", id))
			}
		}
		return nil
	})
}

func addCategory(ctx context.Context, tx *sql.Tx, category model.Category, ids []model.ItemID) error {
	for _, id := range ids {
		// ON CONFLICT does not apply to foreign keys, so an unknown ID fails.
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO item_categories (item_id, category) VALUES (?, ?) ON CONFLICT DO NOTHING",
			string(id), string(category)); err != nil {
			return goerr.Wrap(err, "failed to add a category",
				goerr.V("item_id", id), goerr.V("category", category))
		}
	}
	return nil
}

func (x *Repository) AddCategory(ctx context.Context, category model.Category, ids []model.ItemID) error {
	if len(ids) == 0 {
		return nil
	}
	return x.inTx(ctx, func(tx *sql.Tx) error {
		return addCategory(ctx, tx, category, ids)
	})
}

func (x *Repository) ReplaceCategory(ctx context.Context, category model.Category, ids []model.ItemID) error {
	return x.inTx(ctx, func(tx *sql.Tx) error {
		if err := addCategory(ctx, tx, category, ids); err != nil {
			return err
		}

		keep := make(map[model.ItemID]struct{}, len(ids))
		for _, id := range ids {
			keep[id] = struct{}{}
		}

		rows, err := tx.QueryContext(ctx, "SELECT item_id FROM item_categories WHERE category = ?", string(category))
		if err != nil {
			return goerr.Wrap(err, "failed to list category members", goerr.V("category", category))
		}
		var remove []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				safe.Close(ctx, rows)
				return goerr.Wrap(err, "failed to scan a category member")
			}
			if _, ok := keep[model.ItemID(id)]; !ok {
				remove = append(remove, id)
			}
		}
		if err := rows.Err(); err != nil {
			safe.Close(ctx, rows)
			return goerr.Wrap(err, "failed to list category members", goerr.V("category", category))
		}
		safe.Close(ctx, rows)

		for _, id := range remove {
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM item_categories WHERE item_id = ? AND category = ?", id, string(category)); err != nil {
				return goerr.Wrap(err, "failed to remove a category",
					goerr.V("item_id", id), goerr.V("category", category))
			}
		}
		return nil
	})
}

const selectEntry = `
SELECT i.id, i.kind, i.repo_owner, i.repo_name, i.number, i.title, i.url, i.author,
       i.is_draft, i.ci_state, i.review_state, i.updated_at, r.read_updated_at
FROM items i LEFT JOIN read_marks r ON r.item_id = i.id`

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(s scanner) (*model.Entry, error) {
	var (
		e                    model.Entry
		id, kind, ci, review string
		updatedAt            int64
		readUpdatedAt        sql.NullInt64
	)
	if err := s.Scan(&id, &kind, &e.Item.Repo.Owner, &e.Item.Repo.Name, &e.Item.Number,
		&e.Item.Title, &e.Item.URL, &e.Item.Author, &e.Item.IsDraft, &ci, &review,
		&updatedAt, &readUpdatedAt); err != nil {
		return nil, err
	}
	e.Item.ID = model.ItemID(id)
	e.Item.Kind = model.ItemKind(kind)
	e.Item.State = model.ItemStateOpen
	e.Item.CI = model.CIState(ci)
	e.Item.Review = model.ReviewState(review)
	e.Item.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	e.ReadUpdatedAt = fromMillis(readUpdatedAt)
	return &e, nil
}

func (x *Repository) loadCategories(ctx context.Context, where string, args ...any) (map[model.ItemID][]model.Category, error) {
	rows, err := x.db.QueryContext(ctx, "SELECT item_id, category FROM item_categories "+where, args...)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list categories")
	}
	defer safe.Close(ctx, rows)

	found := map[model.ItemID]map[model.Category]bool{}
	for rows.Next() {
		var id, category string
		if err := rows.Scan(&id, &category); err != nil {
			return nil, goerr.Wrap(err, "failed to scan a category")
		}
		if found[model.ItemID(id)] == nil {
			found[model.ItemID(id)] = map[model.Category]bool{}
		}
		found[model.ItemID(id)][model.Category(category)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, goerr.Wrap(err, "failed to list categories")
	}

	result := make(map[model.ItemID][]model.Category, len(found))
	for id, set := range found {
		for _, c := range model.Categories {
			if set[c] {
				result[id] = append(result[id], c)
			}
		}
	}
	return result, nil
}

func (x *Repository) ListEntries(ctx context.Context) ([]*model.Entry, error) {
	rows, err := x.db.QueryContext(ctx, selectEntry+" ORDER BY i.updated_at DESC, i.id ASC")
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list items")
	}
	defer safe.Close(ctx, rows)

	var entries []*model.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to scan an item")
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, goerr.Wrap(err, "failed to list items")
	}

	categories, err := x.loadCategories(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		e.Categories = categories[e.Item.ID]
	}
	return entries, nil
}

func (x *Repository) GetEntry(ctx context.Context, id model.ItemID) (*model.Entry, error) {
	e, err := scanEntry(x.db.QueryRowContext(ctx, selectEntry+" WHERE i.id = ?", string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, goerr.Wrap(interfaces.ErrItemNotFound, "no such item", goerr.V("item_id", id))
	}
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get an item", goerr.V("item_id", id))
	}
	categories, err := x.loadCategories(ctx, "WHERE item_id = ?", string(id))
	if err != nil {
		return nil, err
	}
	e.Categories = categories[id]
	return e, nil
}

func (x *Repository) FindItemsByRefs(ctx context.Context, refs []model.ItemRef) (map[model.ItemRef]*model.Item, error) {
	result := make(map[model.ItemRef]*model.Item, len(refs))
	for _, ref := range refs {
		e, err := scanEntry(x.db.QueryRowContext(ctx,
			selectEntry+" WHERE i.repo_owner = ? AND i.repo_name = ? AND i.number = ?",
			ref.Repo.Owner, ref.Repo.Name, ref.Number))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to find an item",
				goerr.V("repo", ref.Repo.String()), goerr.V("number", ref.Number))
		}
		item := e.Item
		result[ref] = &item
	}
	return result, nil
}

func (x *Repository) ListRefreshIDs(ctx context.Context, pendingOnly bool) ([]model.ItemID, error) {
	query := "SELECT id FROM items"
	if pendingOnly {
		query += " WHERE kind = 'pull_request' AND ci_state = 'pending'"
	}
	rows, err := x.db.QueryContext(ctx, query+" ORDER BY id")
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list items to refresh")
	}
	defer safe.Close(ctx, rows)

	var ids []model.ItemID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, goerr.Wrap(err, "failed to scan a pull request ID")
		}
		ids = append(ids, model.ItemID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, goerr.Wrap(err, "failed to list pull requests")
	}
	return ids, nil
}

func (x *Repository) SetRead(ctx context.Context, marks map[model.ItemID]time.Time) error {
	if len(marks) == 0 {
		return nil
	}
	return x.inTx(ctx, func(tx *sql.Tx) error {
		for id, at := range marks {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO read_marks (item_id, read_updated_at) VALUES (?, ?)
ON CONFLICT (item_id) DO UPDATE SET read_updated_at = excluded.read_updated_at`,
				string(id), at.UnixMilli()); err != nil {
				return goerr.Wrap(err, "failed to set a read mark", goerr.V("item_id", id))
			}
		}
		return nil
	})
}

func (x *Repository) ClearRead(ctx context.Context, id model.ItemID) error {
	if _, err := x.db.ExecContext(ctx, "DELETE FROM read_marks WHERE item_id = ?", string(id)); err != nil {
		return goerr.Wrap(err, "failed to clear a read mark", goerr.V("item_id", id))
	}
	return nil
}

func (x *Repository) PurgeUncategorized(ctx context.Context) (int, error) {
	res, err := x.db.ExecContext(ctx, "DELETE FROM items WHERE id NOT IN (SELECT item_id FROM item_categories)")
	if err != nil {
		return 0, goerr.Wrap(err, "failed to purge uncategorized items")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, goerr.Wrap(err, "failed to count purged items")
	}
	return int(n), nil
}

const selectSyncState = `
SELECT viewer, search_cursor, notification_cursor, notification_last_modified,
       last_full_sync_at, last_open_refresh_at, last_sync_at, next_run_at,
       lease_until, consecutive_failures, last_error,
       rate_remaining, rate_reset_at
FROM sync_state WHERE id = 1`

func scanSyncState(s scanner) (*model.SyncState, error) {
	var (
		st                                                      model.SyncState
		searchCursor, notificationCursor, lastFull, lastRefresh sql.NullInt64
		lastSync, nextRun, leaseUntil, rateResetAt              sql.NullInt64
		lastError                                               string
	)
	if err := s.Scan(&st.Viewer, &searchCursor, &notificationCursor, &st.NotificationLastModified,
		&lastFull, &lastRefresh, &lastSync, &nextRun, &leaseUntil,
		&st.ConsecutiveFailures, &lastError, &st.RateRemaining, &rateResetAt); err != nil {
		return nil, err
	}
	st.SearchCursor = fromMillis(searchCursor)
	st.NotificationCursor = fromMillis(notificationCursor)
	st.LastFullSyncAt = fromMillis(lastFull)
	st.LastOpenRefreshAt = fromMillis(lastRefresh)
	st.LastSyncAt = fromMillis(lastSync)
	st.NextRunAt = fromMillis(nextRun)
	st.LeaseUntil = fromMillis(leaseUntil)
	st.LastError = model.SyncError(lastError)
	st.RateResetAt = fromMillis(rateResetAt)
	return &st, nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func putSyncState(ctx context.Context, db execer, st *model.SyncState) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO sync_state (id, viewer, search_cursor, notification_cursor, notification_last_modified,
    last_full_sync_at, last_open_refresh_at, last_sync_at, next_run_at,
    lease_until, consecutive_failures, last_error, rate_remaining, rate_reset_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    viewer = excluded.viewer, search_cursor = excluded.search_cursor,
    notification_cursor = excluded.notification_cursor,
    notification_last_modified = excluded.notification_last_modified,
    last_full_sync_at = excluded.last_full_sync_at, last_open_refresh_at = excluded.last_open_refresh_at,
    last_sync_at = excluded.last_sync_at, next_run_at = excluded.next_run_at,
    lease_until = excluded.lease_until,
    consecutive_failures = excluded.consecutive_failures, last_error = excluded.last_error,
    rate_remaining = excluded.rate_remaining, rate_reset_at = excluded.rate_reset_at`,
		st.Viewer, toMillis(st.SearchCursor), toMillis(st.NotificationCursor), st.NotificationLastModified,
		toMillis(st.LastFullSyncAt), toMillis(st.LastOpenRefreshAt), toMillis(st.LastSyncAt),
		toMillis(st.NextRunAt), toMillis(st.LeaseUntil),
		st.ConsecutiveFailures, string(st.LastError), st.RateRemaining, toMillis(st.RateResetAt))
	if err != nil {
		return goerr.Wrap(err, "failed to save the sync state")
	}
	return nil
}

func (x *Repository) GetSyncState(ctx context.Context) (*model.SyncState, error) {
	st, err := scanSyncState(x.db.QueryRowContext(ctx, selectSyncState))
	if errors.Is(err, sql.ErrNoRows) {
		return model.NewSyncState(), nil
	}
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the sync state")
	}
	return st, nil
}

func (x *Repository) PutSyncState(ctx context.Context, state *model.SyncState) error {
	return putSyncState(ctx, x.db, state)
}

func (x *Repository) ClaimSync(ctx context.Context, now time.Time, lease time.Duration, force bool) (*model.SyncState, bool, error) {
	var (
		state   *model.SyncState
		claimed bool
	)
	err := x.inTx(ctx, func(tx *sql.Tx) error {
		st, err := scanSyncState(tx.QueryRowContext(ctx, selectSyncState))
		if errors.Is(err, sql.ErrNoRows) {
			st = model.NewSyncState()
		} else if err != nil {
			return goerr.Wrap(err, "failed to read the sync state")
		}
		state = st

		if now.Before(st.LeaseUntil) || (!force && now.Before(st.NextRunAt)) {
			return nil
		}

		st.LeaseUntil = now.Add(lease)
		if err := putSyncState(ctx, tx, st); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil {
		return nil, false, goerr.Wrap(err, "failed to claim the sync")
	}
	return state, claimed, nil
}

func (x *Repository) RenewSync(ctx context.Context, held, until time.Time) (bool, error) {
	res, err := x.db.ExecContext(ctx,
		"UPDATE sync_state SET lease_until = ? WHERE id = 1 AND lease_until = ?",
		toMillis(until), toMillis(held))
	if err != nil {
		return false, goerr.Wrap(err, "failed to renew the sync lease")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, goerr.Wrap(err, "failed to renew the sync lease")
	}
	return n == 1, nil
}

func (x *Repository) ReleaseSync(ctx context.Context, state *model.SyncState, held time.Time) (bool, error) {
	released := false
	err := x.inTx(ctx, func(tx *sql.Tx) error {
		var current sql.NullInt64
		if err := tx.QueryRowContext(ctx, "SELECT lease_until FROM sync_state WHERE id = 1").Scan(&current); err != nil {
			return goerr.Wrap(err, "failed to read the sync lease")
		}
		if current != toMillis(held) {
			return nil
		}
		if err := putSyncState(ctx, tx, state); err != nil {
			return err
		}
		released = true
		return nil
	})
	if err != nil {
		return false, goerr.Wrap(err, "failed to release the sync lease")
	}
	return released, nil
}

func (x *Repository) ResetAll(ctx context.Context) error {
	return x.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM items"); err != nil {
			return goerr.Wrap(err, "failed to delete items")
		}
		return putSyncState(ctx, tx, model.NewSyncState())
	})
}
