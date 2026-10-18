package certs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const testDirectory = "https://acme.test/directory"

func testStore(t *testing.T) *FileStore {
	t.Helper()
	store, err := OpenFileStore(t.TempDir(), testDirectory)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestFileStoreRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	start := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, "preview.example.com", start, start.Add(90*24*time.Hour))

	if err := store.Save(ctx, "preview.example.com", gen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, ok, err := store.Load(ctx, "preview.example.com")
	if err != nil || !ok {
		t.Fatalf("Load = ok %v, err %v", ok, err)
	}
	if string(loaded.CertPEM) != string(gen.CertPEM) || string(loaded.KeyPEM) != string(gen.KeyPEM) {
		t.Fatal("loaded generation differs from the saved one")
	}
}

func TestFileStoreSkipsIncompleteGenerations(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	start := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, "preview.example.com", start, start.Add(90*24*time.Hour))
	if err := store.Save(ctx, "preview.example.com", gen); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dir := filepath.Join(store.dir, generationsDir, "preview.example.com")
	// Newer interrupted save: half a pair without the completion marker.
	interrupted := filepath.Join(dir, "9999999999999999999-interrupted")
	if err := os.MkdirAll(interrupted, stateDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(interrupted, "cert.pem"), []byte("partial"), stateFileMode); err != nil {
		t.Fatal(err)
	}
	// Newer marked directory missing half of the pair.
	broken := filepath.Join(dir, "9999999999999999998-broken")
	if err := os.MkdirAll(broken, stateDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "cert.pem"), []byte("partial"), stateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, completeMarker), nil, stateFileMode); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := store.Load(ctx, "preview.example.com")
	if err != nil || !ok {
		t.Fatalf("Load = ok %v, err %v", ok, err)
	}
	if string(loaded.CertPEM) != string(gen.CertPEM) {
		t.Fatal("an incomplete generation must never be loaded")
	}
}

func TestFileStorePrunesGenerations(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	start := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		gen, _ := testGeneration(t, "preview.example.com", start.Add(time.Duration(i)*time.Hour), start.Add(90*24*time.Hour))
		if err := store.Save(ctx, "preview.example.com", gen); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(store.dir, generationsDir, "preview.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != RetainGenerations {
		t.Fatalf("kept %d generations, want %d", len(entries), RetainGenerations)
	}
}

func TestFileStoreRejectsSecondWriter(t *testing.T) {
	dir := t.TempDir()
	first, err := OpenFileStore(dir, testDirectory)
	if err != nil {
		t.Fatalf("first OpenFileStore: %v", err)
	}
	defer first.Close()

	if _, err := OpenFileStore(dir, testDirectory); err == nil {
		t.Fatal("a second writer must be rejected while the first holds the lock")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	third, err := OpenFileStore(dir, testDirectory)
	if err != nil {
		t.Fatalf("OpenFileStore after Close: %v", err)
	}
	third.Close()
}

func TestFileStorePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}
	dir := t.TempDir()
	store, err := OpenFileStore(filepath.Join(dir, "state"), testDirectory)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	defer store.Close()

	start := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	gen, _ := testGeneration(t, "preview.example.com", start, start.Add(90*24*time.Hour))
	if err := store.Save(context.Background(), "preview.example.com", gen); err != nil {
		t.Fatalf("Save: %v", err)
	}
	stateDir, err := os.Stat(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := stateDir.Mode().Perm(); perm != stateDirMode {
		t.Fatalf("state dir mode = %o, want %o", perm, stateDirMode)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "state", generationsDir, "preview.example.com"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("generation dir missing: %v", err)
	}
	genDir := filepath.Join(dir, "state", generationsDir, "preview.example.com", entries[0].Name())
	for _, name := range []string{"cert.pem", "key.pem"} {
		info, err := os.Stat(filepath.Join(genDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != stateFileMode {
			t.Fatalf("%s mode = %o, want %o", name, perm, stateFileMode)
		}
	}
}

func TestFileStoreRefusesTraversalHosts(t *testing.T) {
	store := testStore(t)
	gen, _ := testGeneration(t, "preview.example.com", time.Now(), time.Now().Add(time.Hour))
	for _, host := range []string{"../escape", "a/b", "..", ""} {
		if err := store.Save(context.Background(), host, gen); err == nil {
			t.Fatalf("host %q must be rejected", host)
		}
	}
}

func TestFileStoreAccountsArePerDirectory(t *testing.T) {
	ctx := context.Background()
	staging := testStore(t)
	stagingURL := staging.Directory()
	account := ACMEAccount{
		DirectoryURL: stagingURL,
		Email:        "ops@example.com",
		KID:          "https://acme-staging.example/acct/1",
		KeyPEM:       []byte("-----BEGIN PRIVATE KEY-----\nignored\n-----END PRIVATE KEY-----\n"),
	}
	if err := staging.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	loaded, ok, err := staging.LoadAccount(ctx, stagingURL)
	if err != nil || !ok {
		t.Fatalf("LoadAccount = ok %v, err %v", ok, err)
	}
	if loaded.KID != account.KID || loaded.Email != account.Email {
		t.Fatalf("loaded account = %+v", loaded)
	}
	if entries, err := os.ReadDir(filepath.Join(staging.dir, accountDir)); err != nil || len(entries) != 1 {
		t.Fatalf("account files = %v, err %v", entries, err)
	}

	// A different directory gets its own state directory, never the same
	// account file.
	production, err := OpenFileStore(t.TempDir(), "https://acme-v02.example/directory")
	if err != nil {
		t.Fatal(err)
	}
	defer production.Close()
	if _, ok, err := production.LoadAccount(ctx, "https://acme-v02.example/directory"); err != nil || ok {
		t.Fatalf("production lookup = ok %v, err %v; want a miss", ok, err)
	}
	if err := os.WriteFile(staging.accountPath(stagingURL), []byte("{"), stateFileMode); err != nil {
		t.Fatal(err)
	}
	if _, _, err := staging.LoadAccount(ctx, stagingURL); !errors.Is(err, ErrStorage) {
		t.Fatalf("corrupt account err = %v, want ErrStorage", err)
	}
}

// The state directory claims one ACME directory for its whole life: pointing
// production at the staging directory (or the reverse) fails at startup
// instead of mixing state.
func TestFileStoreRefusesAnotherACMEDirectory(t *testing.T) {
	dir := t.TempDir()
	staging, err := OpenFileStore(dir, "https://acme-staging.example/directory")
	if err != nil {
		t.Fatalf("OpenFileStore staging: %v", err)
	}
	staging.Close()
	if _, err := OpenFileStore(dir, "https://acme-v02.example/directory"); !errors.Is(err, ErrStorage) {
		t.Fatalf("err = %v, want ErrStorage for a directory mismatch", err)
	}
	reopened, err := OpenFileStore(dir, "https://acme-staging.example/directory")
	if err != nil {
		t.Fatalf("reopen with the same directory: %v", err)
	}
	reopened.Close()
}

func TestFileStoreIntents(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	id, err := store.SaveIntent(ctx, DNSIntent{Name: "_acme-challenge.preview.example.com", Value: "value-1"})
	if err != nil {
		t.Fatalf("SaveIntent: %v", err)
	}
	// The same intent is updated once the exact record ID is known.
	if _, err := store.SaveIntent(ctx, DNSIntent{ID: id, Name: "_acme-challenge.preview.example.com", Value: "value-1", ZoneID: "z1", RecordID: "r1"}); err != nil {
		t.Fatalf("SaveIntent update: %v", err)
	}
	intents, err := store.ListIntents(ctx)
	if err != nil || len(intents) != 1 {
		t.Fatalf("ListIntents = %v, err %v", len(intents), err)
	}
	if intents[0].RecordID != "r1" || intents[0].ZoneID != "z1" {
		t.Fatalf("intent = %+v", intents[0])
	}
	if err := store.DeleteIntent(ctx, id); err != nil {
		t.Fatalf("DeleteIntent: %v", err)
	}
	if intents, err := store.ListIntents(ctx); err != nil || len(intents) != 0 {
		t.Fatalf("after delete: %d intents, err %v", len(intents), err)
	}
	if err := store.DeleteIntent(ctx, "../../escape"); !errors.Is(err, ErrStorage) {
		t.Fatalf("traversal delete err = %v, want ErrStorage", err)
	}
	if _, err := store.SaveIntent(ctx, DNSIntent{Name: "x"}); err == nil {
		t.Fatal("an intent without a value must be refused")
	}
}
