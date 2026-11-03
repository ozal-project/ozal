package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAppNamesSetReplaceAndClear(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, _ := LoadMigrations(Migrations())
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	names := NewAppNames(db)
	if err := names.SetName(ctx, "wiki.example.com", "  Team wiki "); err != nil {
		t.Fatal(err)
	}
	if err := names.SetName(ctx, "wiki.example.com", "Wiki"); err != nil {
		t.Fatal(err)
	}
	got, err := names.Names(ctx)
	if err != nil || len(got) != 1 || got["wiki.example.com"] != "Wiki" {
		t.Fatalf("names = %v, %v", got, err)
	}
	for _, bad := range []string{strings.Repeat("a", 61), "two\nlines"} {
		if err := names.SetName(ctx, "wiki.example.com", bad); !errors.Is(err, ErrInvalidAppName) {
			t.Fatalf("%q = %v, want ErrInvalidAppName", bad, err)
		}
	}
	if err := names.SetName(ctx, "wiki.example.com", "  "); err != nil {
		t.Fatal(err)
	}
	if got, _ := names.Names(ctx); len(got) != 0 {
		t.Fatalf("after clearing: %v", got)
	}
}
