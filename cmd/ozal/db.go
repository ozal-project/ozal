package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ozal-project/ozal/internal/store/sqlite"
)

func runDB(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: ozal db backup|restore [flags]")
	}
	action, rest := args[0], args[1:]
	fs := flag.NewFlagSet("db "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	sourceDB := fs.String("db", defaultDBPath, "backup: existing SQLite state database")
	from := fs.String("from", "", "restore: existing SQLite backup")
	to := fs.String("to", "", "restore: new state database path")
	output := fs.String("out", "", "backup: new snapshot path")
	keepSessions := fs.Bool("keep-sessions", false, "restore: preserve sessions from the backup (may reactivate old cookies)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q (usage: ozal db backup|restore [flags])", fs.Arg(0))
	}
	switch action {
	case "backup":
		if *output == "" || *from != "" || *to != "" || *keepSessions {
			return errors.New("usage: ozal db backup -db PATH -out NEW_PATH")
		}
		if err := copyDatabaseSnapshot(ctx, *sourceDB, *output); err != nil {
			return fmt.Errorf("database backup failed: %w", err)
		}
		fmt.Fprintf(out, "created consistent snapshot at %s (mode 0600)\n", *output)
		return nil
	case "restore":
		if *from == "" || *to == "" || *output != "" || *sourceDB != defaultDBPath {
			return errors.New("usage: ozal db restore -from BACKUP -to NEW_PATH [-keep-sessions]")
		}
		if err := copyDatabaseSnapshotWithSessionPolicy(ctx, *from, *to, !*keepSessions); err != nil {
			return fmt.Errorf("database restore failed: %w", err)
		}
		if *keepSessions {
			fmt.Fprintf(out, "restored a consistent snapshot to %s (mode 0600; sessions preserved by explicit request); the source and any existing database were not changed\n", *to)
		} else {
			fmt.Fprintf(out, "restored a consistent snapshot to %s (mode 0600; prior sessions invalidated); user and passkey records still reflect the backup time\n", *to)
		}
		return nil
	default:
		return fmt.Errorf("unknown db command %q (choose backup or restore)", action)
	}
}

// copyDatabaseSnapshot uses SQLite's online backup form, VACUUM INTO, so WAL
// pages are included consistently. The private staging directory is on the
// destination filesystem; a hard link publishes the finished snapshot without
// overwriting a path that appeared concurrently.
func copyDatabaseSnapshot(ctx context.Context, source, destination string) (err error) {
	return copyDatabaseSnapshotWithSessionPolicy(ctx, source, destination, false)
}

func copyDatabaseSnapshotWithSessionPolicy(ctx context.Context, source, destination string, invalidateSessions bool) (err error) {
	info, err := os.Lstat(source)
	if err != nil {
		return errors.New("source is unavailable")
	}
	if !info.Mode().IsRegular() {
		return errors.New("source must be a regular file, not a symlink or special file")
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination %s already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	parent := filepath.Dir(destination)
	stageDir, err := os.MkdirTemp(parent, ".ozal-db-")
	if err != nil {
		return fmt.Errorf("create private staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)
	stagePath := filepath.Join(stageDir, "snapshot.db")

	db, err := sqlite.OpenReadOnly(ctx, source)
	if err != nil {
		return fmt.Errorf("open source read-only: %w", err)
	}
	if err := sqlite.IntegrityCheck(ctx, db); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, stagePath); err != nil {
		_ = db.Close()
		return fmt.Errorf("create consistent SQLite snapshot: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close source database: %w", err)
	}
	if invalidateSessions {
		restored, err := sqlite.Open(ctx, stagePath)
		if err != nil {
			return fmt.Errorf("open staged restore to invalidate sessions: %w", err)
		}
		_, deleteErr := restored.ExecContext(ctx, `DELETE FROM sessions`)
		closeErr := restored.Close()
		if deleteErr != nil || closeErr != nil {
			return fmt.Errorf("invalidate restored sessions: %w", errors.Join(deleteErr, closeErr))
		}
	}
	staged, err := sqlite.OpenReadOnly(ctx, stagePath)
	if err != nil {
		return fmt.Errorf("open completed snapshot read-only: %w", err)
	}
	if err := sqlite.IntegrityCheck(ctx, staged); err != nil {
		_ = staged.Close()
		return fmt.Errorf("verify completed snapshot: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("close completed snapshot: %w", err)
	}
	if err := os.Chmod(stagePath, 0o600); err != nil {
		return fmt.Errorf("set snapshot permissions: %w", err)
	}
	file, err := os.OpenFile(stagePath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open completed snapshot: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync completed snapshot: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close completed snapshot: %w", err)
	}
	if err := os.Link(stagePath, destination); err != nil {
		return fmt.Errorf("publish snapshot without overwriting destination: %w", err)
	}
	syncDirectory(parent)
	return nil
}

func syncDirectory(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	dir, err := os.Open(path)
	if err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
}
