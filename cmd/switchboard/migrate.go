package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/switchboard-project/switchboard/internal/store/sqlite"
)

const defaultDBPath = "switchboard.db"

// runMigrate implements `switchboard migrate`: apply pending schema
// migrations to the state database, or with -status only report them.
func runMigrate(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(out)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	statusOnly := fs.Bool("status", false, "report applied and pending migrations without applying them")
	backup := fs.String("backup", "", "up: when migrations are pending, first write a consistent copy of the database here (must not exist)")
	// The action words of scripts/migrate.sh are accepted here too. Any
	// other positional argument is an error: the flag parser stops at the
	// first one, so `migrate up -db X` used to ignore -db and migrate the
	// default database instead of X.
	if len(args) > 0 {
		switch args[0] {
		case "up":
			args = args[1:]
		case "status":
			args = append([]string{"-status"}, args[1:]...)
		}
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q (usage: switchboard migrate [up|status] [-db PATH])", fs.Arg(0))
	}

	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err != nil {
		return err
	}
	db, err := sqlite.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if *statusOnly {
		statuses, err := sqlite.MigrationStatus(ctx, db, migrations)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			state := "pending"
			if s.Applied {
				state = "applied " + s.AppliedAt.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(out, "%03d_%s\t%s\n", s.Migration.Version, s.Migration.Name, state)
		}
		return nil
	}

	if *backup != "" {
		if err := backupBeforeMigrating(ctx, db, migrations, *backup, out); err != nil {
			return err
		}
	}
	applied, err := sqlite.Migrate(ctx, db, migrations)
	for _, m := range applied {
		fmt.Fprintf(out, "applied %03d_%s\n", m.Version, m.Name)
	}
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Fprintf(out, "%s is up to date (%d migrations)\n", *dbPath, len(migrations))
	}
	return nil
}

// openMigratedDB validates the embedded schema before starting a consumer.
// Applying migrations is an explicit operator step, never a startup side effect.
func openMigratedDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	migrations, err := sqlite.LoadMigrations(sqlite.Migrations())
	if err == nil {
		var statuses []sqlite.Status
		statuses, err = sqlite.MigrationStatus(ctx, db, migrations)
		if err == nil {
			for _, s := range statuses {
				if !s.Applied {
					err = errors.New("database has pending migrations: run `switchboard migrate` first")
					break
				}
			}
		}
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// backupBeforeMigrating writes a consistent copy of db to path when any
// migration is pending, so a failed upgrade can put back the schema the
// previous binary understands. With nothing pending it writes nothing: the
// caller tells the two cases apart by whether path exists.
func backupBeforeMigrating(ctx context.Context, db *sql.DB, migrations []sqlite.Migration, path string, out io.Writer) error {
	statuses, err := sqlite.MigrationStatus(ctx, db, migrations)
	if err != nil {
		return err
	}
	pending := 0
	for _, s := range statuses {
		if !s.Applied {
			pending++
		}
	}
	if pending == 0 {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("-backup %s already exists", path)
	}
	// VACUUM INTO writes a complete, consistent database (WAL included)
	// without holding writers off for longer than the copy.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("back up before migrating: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "backed up the database to %s before %d pending migrations\n", path, pending)
	return nil
}
