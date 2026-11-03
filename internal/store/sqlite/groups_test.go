package sqlite

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestGroupCatalogCreatesEmptyGroupsOnce(t *testing.T) {
	ctx := context.Background()
	db, _ := openTestDB(t)
	migrations, _ := LoadMigrations(Migrations())
	if _, err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	catalog := NewGroupCatalog(db)
	if name, err := catalog.Create(ctx, "  ops "); err != nil || name != "ops" {
		t.Fatalf("create = %q, %v", name, err)
	}
	if _, err := catalog.Create(ctx, "ops"); !errors.Is(err, ErrGroupExists) {
		t.Fatalf("second create = %v, want ErrGroupExists", err)
	}
	if _, err := catalog.Create(ctx, "bad\nname"); err == nil {
		t.Fatal("accepted a control character")
	}
	if got, _ := catalog.Defined(ctx); !slices.Equal(got, []string{"ops"}) {
		t.Fatalf("defined = %v", got)
	}
	if err := catalog.Delete(ctx, "ops"); err != nil {
		t.Fatal(err)
	}
	if got, _ := catalog.Defined(ctx); len(got) != 0 {
		t.Fatalf("after delete = %v", got)
	}
}
