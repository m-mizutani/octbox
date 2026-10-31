package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/domain/model"
	"github.com/m-mizutani/octbox/pkg/repository/sqlite"
)

func userVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	gt.NoError(t, err).Required()
	defer func() { _ = db.Close() }()
	var v int
	gt.NoError(t, db.QueryRow("PRAGMA user_version").Scan(&v)).Required()
	return v
}

func TestNewCreatesFileAndSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "octbox.db")

	repo, err := sqlite.New(ctx, path)
	gt.NoError(t, err).Required()
	gt.NoError(t, repo.Close())

	dirInfo, err := os.Stat(filepath.Dir(path))
	gt.NoError(t, err).Required()
	gt.Equal(t, dirInfo.Mode().Perm(), os.FileMode(0o700))

	fileInfo, err := os.Stat(path)
	gt.NoError(t, err).Required()
	gt.Equal(t, fileInfo.Mode().Perm(), os.FileMode(0o600))

	gt.Equal(t, userVersion(t, path), 2)

	// A second open does not run the migration again (it would fail on
	// CREATE TABLE).
	repo, err = sqlite.New(ctx, path)
	gt.NoError(t, err).Required()
	gt.NoError(t, repo.Close())
	gt.Equal(t, userVersion(t, path), 2)
}

// createVersion1 writes a database of schema version 1 holding one pull
// request with a category and a read mark.
func createVersion1(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	gt.NoError(t, err).Required()
	for _, stmt := range []string{
		// Every file octbox opened is in WAL mode, which persists in the file.
		"PRAGMA journal_mode = WAL",
		sqlite.SchemaV1,
		"INSERT INTO sync_state (id, viewer) VALUES (1, 'alice')",
		`INSERT INTO items (id, kind, repo_owner, repo_name, number, title, url, author, updated_at)
VALUES ('PR_1', 'pull_request', 'example-org', 'api', 1, 'Add retry', 'https://github.com/example-org/api/pull/1', 'bob', 1790000000000)`,
		"INSERT INTO item_categories (item_id, category) VALUES ('PR_1', 'review')",
		"INSERT INTO read_marks (item_id, read_updated_at) VALUES ('PR_1', 1790000000000)",
		"PRAGMA user_version = 1",
	} {
		_, err := db.Exec(stmt)
		gt.NoError(t, err).Required()
	}
	gt.NoError(t, db.Close())
}

func TestNewMigratesVersion1(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "octbox.db")
	createVersion1(t, path)

	repo, err := sqlite.New(ctx, path)
	gt.NoError(t, err).Required()
	defer func() { _ = repo.Close() }()
	gt.Equal(t, userVersion(t, path), 2)
	assertMigratedVersion1(t, repo)

	// The added columns are written by the next save.
	e, err := repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	e.Item.ViewerApproved = true
	gt.NoError(t, repo.SaveItems(ctx, []model.Item{e.Item})).Required()
	saved, err := repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.Equal(t, saved.Item, e.Item)
}

func TestNewMigratesVersion1Concurrently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "octbox.db")
	createVersion1(t, path)

	// Two processes sharing the database start at the same time.
	const opens = 2
	repos := make([]*sqlite.Repository, opens)
	errs := make([]error, opens)
	var wg sync.WaitGroup
	for i := range opens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			repos[i], errs[i] = sqlite.New(ctx, path)
		}()
	}
	wg.Wait()

	for i := range opens {
		gt.NoError(t, errs[i]).Required()
		defer func() { _ = repos[i].Close() }()
	}
	gt.Equal(t, userVersion(t, path), 2)
	for _, repo := range repos {
		assertMigratedVersion1(t, repo)
	}
}

// assertMigratedVersion1 checks the rows written by createVersion1 after the
// migration.
func assertMigratedVersion1(t *testing.T, repo *sqlite.Repository) {
	t.Helper()
	ctx := context.Background()

	// Rows of version 1 are kept, with the viewer's review unknown (false)
	// until the next refresh.
	e, err := repo.GetEntry(ctx, "PR_1")
	gt.NoError(t, err).Required()
	gt.Equal(t, e.Item.Title, "Add retry")
	gt.False(t, e.Item.ViewerApproved)
	gt.False(t, e.Item.ViewerReviewRequested)
	gt.A(t, e.Categories).Equal([]model.Category{model.CategoryReview})
	gt.Equal(t, e.ReadUpdatedAt.UnixMilli(), int64(1790000000000))

	state, err := repo.GetSyncState(ctx)
	gt.NoError(t, err).Required()
	gt.Equal(t, state.Viewer, "alice")
}

func TestNewRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "octbox.db")

	db, err := sql.Open("sqlite", "file:"+path)
	gt.NoError(t, err).Required()
	_, err = db.Exec("PRAGMA user_version = 3")
	gt.NoError(t, err).Required()
	gt.NoError(t, db.Close())

	_, err = sqlite.New(ctx, path)
	gt.Error(t, err).Is(interfaces.ErrSchemaTooNew)
}
