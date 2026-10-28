package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
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

	gt.Equal(t, userVersion(t, path), 1)

	// A second open does not run the migration again (it would fail on
	// CREATE TABLE).
	repo, err = sqlite.New(ctx, path)
	gt.NoError(t, err).Required()
	gt.NoError(t, repo.Close())
	gt.Equal(t, userVersion(t, path), 1)
}

func TestNewRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "octbox.db")

	db, err := sql.Open("sqlite", "file:"+path)
	gt.NoError(t, err).Required()
	_, err = db.Exec("PRAGMA user_version = 2")
	gt.NoError(t, err).Required()
	gt.NoError(t, db.Close())

	_, err = sqlite.New(ctx, path)
	gt.Error(t, err).Is(interfaces.ErrSchemaTooNew)
}
