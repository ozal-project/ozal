package main

import (
	"context"
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
