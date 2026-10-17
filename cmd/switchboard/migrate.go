package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/store/sqlite"
)

const defaultDBPath = "switchboard.db"

// runMigrate implements `switchboard migrate`: apply pending schema
// migrations to the state database, or with -status only report them.
func runMigrate(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(out)
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	statusOnly := fs.Bool("status", false, "report applied and pending migrations without applying them")
	if err := fs.Parse(args); err != nil {
		return err
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
