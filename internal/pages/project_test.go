package pages

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjectDeploymentPersistsFirstSourceAndScopesLookup(t *testing.T) {
	ctx := context.Background()
	store := newStore(t, migratedDB(t), t.TempDir(), DefaultKeep)
	source := SourceMetadata{Project: "portfolio", CommitSHA: strings.Repeat("a", 40), RunURL: "https://github.com/owner/repo/actions/runs/42"}
	deployed, err := store.DeployProject(ctx, "portfolio", source, site(t, "first build"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := store.Deployment(ctx, "portfolio", deployed.ID)
	if err != nil || found.Project != source.Project || found.CommitSHA != source.CommitSHA || found.RunURL != source.RunURL || !found.Live {
		t.Fatalf("lookup = %+v, %v", found, err)
	}
	if _, err := store.Deployment(ctx, "other-site", deployed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-site lookup = %v", err)
	}
	source.CommitSHA = strings.Repeat("b", 40)
	repeated, err := store.DeployProject(ctx, "portfolio", source, site(t, "first build"))
	if err != nil || repeated.CommitSHA != found.CommitSHA {
		t.Fatalf("repeat rewrote first provenance: %+v, %v", repeated, err)
	}
	if _, err := store.DeployProject(ctx, "portfolio", source, site(t, "new live build")); err != nil {
		t.Fatal(err)
	}
	source.Project = "different-project"
	if _, err := store.DeployProject(ctx, "portfolio", source, site(t, "first build")); err == nil {
		t.Fatal("another project adopted an existing artifact's provenance")
	}
	if got := liveIndex(t, store, "portfolio"); got != "new live build" {
		t.Fatalf("refused cross-project reupload changed live site to %q", got)
	}
}
