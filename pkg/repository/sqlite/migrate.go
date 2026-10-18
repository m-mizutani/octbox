package sqlite

import (
	"context"
	"database/sql"
	_ "embed"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
)

//go:embed schema_v1.sql
var schemaV1 string

const schemaVersion = 1

// migrate brings the schema to schemaVersion. PRAGMA user_version holds the
// version of the file.
func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return goerr.Wrap(err, "failed to read the schema version")
	}

	switch {
	case version == schemaVersion:
		return nil
	case version > schemaVersion:
		return goerr.Wrap(interfaces.ErrSchemaTooNew, "cannot open the database",
			goerr.V("file_version", version), goerr.V("supported_version", schemaVersion))
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return goerr.Wrap(err, "failed to begin the migration")
	}
	defer func() { _ = tx.Rollback() }()

	// The version is read again inside the write transaction: another process
	// may have migrated the file in the meantime.
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return goerr.Wrap(err, "failed to read the schema version")
	}
	if version >= schemaVersion {
		return nil
	}

	if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
		return goerr.Wrap(err, "failed to create the schema")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sync_state (id) VALUES (1)"); err != nil {
		return goerr.Wrap(err, "failed to create the sync state")
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return goerr.Wrap(err, "failed to set the schema version")
	}
	if err := tx.Commit(); err != nil {
		return goerr.Wrap(err, "failed to commit the migration")
	}
	return nil
}
