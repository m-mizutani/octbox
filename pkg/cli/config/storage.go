package config

import (
	"context"
	"os"
	"path/filepath"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/octbox/pkg/repository/sqlite"
)

// errNoHome is returned when a default path is needed but the home
// directory is unknown.
var errNoHome = goerr.New("cannot determine the home directory; set --db-path and --log-file")

// dataDir is $HOME/.octbox on macOS and Linux, or "" when the home directory
// is unknown. XDG_STATE_HOME is not used: every file of octbox stays in one
// directory the user can find.
func dataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".octbox")
}

func defaultPath(name string) string {
	dir := dataDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

type Storage struct {
	dbPath string
}

func (x *Storage) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "db-path",
			Category:    "Storage",
			Usage:       "Path of the local SQLite database",
			Value:       defaultPath("octbox.db"),
			Sources:     cli.EnvVars("OCTBOX_DB_PATH"),
			Destination: &x.dbPath,
		},
	}
}

// Open opens the database, creating its directory (0700) and file (0600).
func (x *Storage) Open(ctx context.Context) (*sqlite.Repository, error) {
	if x.dbPath == "" {
		return nil, errNoHome
	}
	repo, err := sqlite.New(ctx, x.dbPath)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to open the database", goerr.V("path", x.dbPath))
	}
	return repo, nil
}
