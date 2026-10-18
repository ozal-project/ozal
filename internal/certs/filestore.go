package certs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	stateDirMode  fs.FileMode = 0o700
	stateFileMode fs.FileMode = 0o600

	generationsDir  = "certs"
	accountDir      = "account"
	intentDir       = "dns-intents"
	lockName        = "lock"
	completeMarker  = "complete"
	directoryMarker = "acme-directory"

	// RetainGenerations is how many complete generations survive a save:
	// the active one and two fallbacks for inspection or manual rollback.
	RetainGenerations = 3
)

// FileStore is the file-backed Store. Everything lives under one state
// directory with private permissions, and the directory has at most one
// writer at a time: the advisory lock rejects a second process before it can
// race on the ACME account, a certificate generation or a DNS cleanup
// intent.
type FileStore struct {
	dir       string
	directory string
	release   func()
}

// OpenFileStore creates the state directory when needed, takes its advisory
// lock and claims it for one ACME directory. A directory that already
// belongs to another ACME directory is refused: staging and production
// state never mix, not even by pointing two configurations at one path.
func OpenFileStore(dir, directoryURL string) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: state directory is required", ErrStorage)
	}
	if directoryURL == "" {
		return nil, fmt.Errorf("%w: acme directory URL is required", ErrStorage)
	}
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return nil, fmt.Errorf("%w: create state directory: %v", ErrStorage, err)
	}
	release, err := acquireLock(filepath.Join(dir, lockName))
	if err != nil {
		return nil, fmt.Errorf("%w: state directory %q is already in use: %v", ErrStorage, dir, err)
	}
	store := &FileStore{dir: dir, directory: directoryURL, release: release}
	if err := store.claimDirectory(directoryURL); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

// claimDirectory writes the directory marker on first use and refuses a
// different one afterwards.
func (s *FileStore) claimDirectory(directoryURL string) error {
	marker := filepath.Join(s.dir, directoryMarker)
	existing, err := os.ReadFile(marker)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := writeFileAtomic(marker, []byte(directoryURL+"\n")); err != nil {
			return fmt.Errorf("%w: claim state directory: %v", ErrStorage, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("%w: read state directory marker: %v", ErrStorage, err)
	}
	if claimed := strings.TrimSpace(string(existing)); claimed != directoryURL {
		return fmt.Errorf("%w: state directory %q belongs to ACME directory %q; use a separate state_dir", ErrStorage, s.dir, claimed)
	}
	return nil
}

// Close releases the directory lock. flock-based locks are also released by
// process exit, so a crash never leaves one that needs manual removal.
func (s *FileStore) Close() error {
	if s.release != nil {
		s.release()
		s.release = nil
	}
	return nil
}

// hostDir maps a hostname to its directory, refusing anything that could
// escape the state directory.
func (s *FileStore) hostDir(host string) (string, error) {
	if host == "" || strings.ContainsAny(host, `/\`) || strings.Contains(host, "..") {
		return "", fmt.Errorf("%w: refusing host %q as a directory name", ErrStorage, host)
	}
	return filepath.Join(s.dir, generationsDir, host), nil
}

// Save writes cert.pem and key.pem into a fresh generation directory and
// fsyncs them, then writes the completion marker and fsyncs the directory.
// Load only accepts directories with the marker, so an interrupted save is
// invisible: the previously active generation keeps being served.
func (s *FileStore) Save(ctx context.Context, host string, gen StoredGeneration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(gen.CertPEM) == 0 || len(gen.KeyPEM) == 0 {
		return fmt.Errorf("%w: refusing to store an incomplete generation", ErrStorage)
	}
	dir, err := s.hostDir(host)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return fmt.Errorf("%w: create generation directory: %v", ErrStorage, err)
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("%w: randomness: %v", ErrStorage, err)
	}
	genDir := filepath.Join(dir, fmt.Sprintf("%019d-%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:])))
	if err := os.Mkdir(genDir, stateDirMode); err != nil {
		return fmt.Errorf("%w: create generation: %v", ErrStorage, err)
	}
	abort := func(err error) error {
		os.RemoveAll(genDir) // best effort: without the marker this directory is never loadable
		return err
	}
	if err := writeFileSynced(filepath.Join(genDir, "cert.pem"), gen.CertPEM); err != nil {
		return abort(fmt.Errorf("%w: write certificate: %v", ErrStorage, err))
	}
	if err := writeFileSynced(filepath.Join(genDir, "key.pem"), gen.KeyPEM); err != nil {
		return abort(fmt.Errorf("%w: write key: %v", ErrStorage, err))
	}
	// The marker goes last: its presence is what makes the generation
	// complete.
	if err := writeFileSynced(filepath.Join(genDir, completeMarker), nil); err != nil {
		return abort(fmt.Errorf("%w: write completion marker: %v", ErrStorage, err))
	}
	if err := syncDirectory(genDir); err != nil {
		return abort(fmt.Errorf("%w: sync generation: %v", ErrStorage, err))
	}
	// Persist every newly created directory entry before activation, including
	// the host and certs parents on first issuance.
	for _, parent := range []string{dir, filepath.Dir(dir), s.dir} {
		if err := syncDirectory(parent); err != nil {
			return abort(fmt.Errorf("%w: sync generation parent: %v", ErrStorage, err))
		}
	}
	s.prune(dir)
	return nil
}

// Load returns the newest complete generation. Incomplete directories — an
// interrupted save — are skipped, and a directory that lacks one half is
// never combined with another generation's other half.
func (s *FileStore) Load(ctx context.Context, host string) (StoredGeneration, bool, error) {
	if err := ctx.Err(); err != nil {
		return StoredGeneration{}, false, err
	}
	dir, err := s.hostDir(host)
	if err != nil {
		return StoredGeneration{}, false, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return StoredGeneration{}, false, nil
	}
	if err != nil {
		return StoredGeneration{}, false, fmt.Errorf("%w: read generations: %v", ErrStorage, err)
	}
	// The ID starts with the creation time, so a descending name sort puts
	// the newest attempt first.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		gen, ok, err := readGeneration(filepath.Join(dir, entry.Name()))
		if err != nil {
			return StoredGeneration{}, false, err
		}
		if ok {
			return gen, true, nil
		}
	}
	return StoredGeneration{}, false, nil
}

func readGeneration(dir string) (StoredGeneration, bool, error) {
	if _, err := os.Stat(filepath.Join(dir, completeMarker)); err != nil {
		return StoredGeneration{}, false, nil // incomplete save or plain junk: not loadable
	}
	cert, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return StoredGeneration{}, false, nil
	}
	key, err := os.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		return StoredGeneration{}, false, nil
	}
	return StoredGeneration{CertPEM: cert, KeyPEM: key}, true, nil
}

// prune keeps the newest RetainGenerations complete generations and removes
// older or incomplete ones. It is best effort: a failure only costs disk
// space and never affects what is served.
func (s *FileStore) prune(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var complete []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), completeMarker)); err == nil {
			complete = append(complete, entry.Name())
		} else {
			os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(complete)
	for _, name := range complete[:max(0, len(complete)-RetainGenerations)] {
		os.RemoveAll(filepath.Join(dir, name))
	}
}

// LoadAccount returns the persisted account for an ACME directory, or
// ok=false when this directory has never registered.
func (s *FileStore) LoadAccount(ctx context.Context, directoryURL string) (ACMEAccount, bool, error) {
	if err := ctx.Err(); err != nil {
		return ACMEAccount{}, false, err
	}
	data, err := os.ReadFile(s.accountPath(directoryURL))
	if errors.Is(err, fs.ErrNotExist) {
		return ACMEAccount{}, false, nil
	}
	if err != nil {
		return ACMEAccount{}, false, fmt.Errorf("%w: read account: %v", ErrStorage, err)
	}
	var account ACMEAccount
	if err := json.Unmarshal(data, &account); err != nil {
		return ACMEAccount{}, false, fmt.Errorf("%w: parse account: %v", ErrStorage, err)
	}
	if account.DirectoryURL != directoryURL || account.KID == "" || len(account.KeyPEM) == 0 {
		return ACMEAccount{}, false, fmt.Errorf("%w: account file does not match directory %q", ErrStorage, directoryURL)
	}
	return account, true, nil
}

// SaveAccount persists the account key and registration. The file is
// written with owner-only permissions and replaced atomically.
func (s *FileStore) SaveAccount(ctx context.Context, account ACMEAccount) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if account.DirectoryURL == "" || account.KID == "" || len(account.KeyPEM) == 0 {
		return fmt.Errorf("%w: refusing to store an incomplete account", ErrStorage)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, accountDir), stateDirMode); err != nil {
		return fmt.Errorf("%w: create account directory: %v", ErrStorage, err)
	}
	data, err := json.Marshal(account)
	if err != nil {
		return fmt.Errorf("%w: encode account: %v", ErrStorage, err)
	}
	if err := writeFileAtomic(s.accountPath(account.DirectoryURL), data); err != nil {
		return fmt.Errorf("%w: write account: %v", ErrStorage, err)
	}
	return nil
}

func (s *FileStore) accountPath(directoryURL string) string {
	sum := sha256.Sum256([]byte(directoryURL))
	return filepath.Join(s.dir, accountDir, hex.EncodeToString(sum[:16])+".json")
}

// Directory reports the ACME directory this state directory belongs to.
func (s *FileStore) Directory() string { return s.directory }

var intentIDPattern = regexp.MustCompile(`^[0-9]{19}-[0-9a-f]{8,64}$`)

// SaveIntent persists the intent to own one TXT record and returns its ID.
// The intent is written before the DNS create call, so an interrupted
// attempt is still recoverable.
func (s *FileStore) SaveIntent(ctx context.Context, intent DNSIntent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if intent.Name == "" || intent.Value == "" {
		return "", fmt.Errorf("%w: intent without record name or value", ErrStorage)
	}
	if intent.ID == "" {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", fmt.Errorf("%w: randomness: %v", ErrStorage, err)
		}
		intent.ID = fmt.Sprintf("%019d-%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
	}
	if !intentIDPattern.MatchString(intent.ID) {
		return "", fmt.Errorf("%w: invalid intent ID %q", ErrStorage, intent.ID)
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now()
	}
	if err := os.MkdirAll(filepath.Join(s.dir, intentDir), stateDirMode); err != nil {
		return "", fmt.Errorf("%w: create intent directory: %v", ErrStorage, err)
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return "", fmt.Errorf("%w: encode intent: %v", ErrStorage, err)
	}
	if err := writeFileAtomic(filepath.Join(s.dir, intentDir, intent.ID+".json"), data); err != nil {
		return "", fmt.Errorf("%w: write intent: %v", ErrStorage, err)
	}
	return intent.ID, nil
}

// DeleteIntent removes a cleanup intent after its record is gone.
func (s *FileStore) DeleteIntent(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !intentIDPattern.MatchString(id) {
		return fmt.Errorf("%w: invalid intent ID %q", ErrStorage, id)
	}
	err := os.Remove(filepath.Join(s.dir, intentDir, id+".json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: remove intent: %v", ErrStorage, err)
	}
	return nil
}

// ListIntents returns every pending cleanup intent, oldest first.
func (s *FileStore) ListIntents(ctx context.Context) ([]DNSIntent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, intentDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read intents: %v", ErrStorage, err)
	}
	var intents []DNSIntent
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, intentDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("%w: read intent: %v", ErrStorage, err)
		}
		var intent DNSIntent
		if err := json.Unmarshal(data, &intent); err != nil {
			return nil, fmt.Errorf("%w: parse intent %s: %v", ErrStorage, entry.Name(), err)
		}
		intents = append(intents, intent)
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].ID < intents[j].ID })
	return intents, nil
}

// writeFileSynced creates a private file and fsyncs its contents before
// returning, so a crash after the call cannot lose what was written.
func writeFileSynced(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, stateFileMode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// writeFileAtomic replaces path through a temporary file in the same
// directory: readers see either the old or the new contents, and a crash
// leaves at most a stray temporary file.
func writeFileAtomic(path string, data []byte) error {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+hex.EncodeToString(suffix[:]))
	if err := writeFileSynced(tmp, data); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
