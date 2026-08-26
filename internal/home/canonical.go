package home

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// LayoutVersion is the current durable layout version. Only homes whose
	// identity reports this layout are supported; anything else fails closed.
	LayoutVersion = 1

	// layoutSchemaVersion is the identity file schema version.
	layoutSchemaVersion = 1

	// IdentityFileName is the canonical home identity file at the home root.
	IdentityFileName = "identity.json"

	// JournalDirName is the home-relative write-ahead journal directory.
	JournalDirName = ".journal"

	// LockDirName is the home-relative scoped lock directory.
	LockDirName = ".lock"

	// LeaseDirName is the home-relative scoped lease directory.
	LeaseDirName = ".lease"
)

// Logical root names exposed by the canonical layout.
const (
	RootState    = "state"
	RootData     = "data"
	RootConfig   = "config"
	RootProjects = "projects"
)

// Layout describes the canonical v1 home layout. Each field is a home-relative
// directory name for one logical root.
type Layout struct {
	State    string
	Data     string
	Config   string
	Projects string
}

// CanonicalLayout is the current v1 layout. Owner modules address durable
// state through these logical roots; they never write to the home directly.
var CanonicalLayout = Layout{
	State:    RootState,
	Data:     RootData,
	Config:   RootConfig,
	Projects: RootProjects,
}

// HomeIdentity is the stable, durable identity of one canonical home. It is
// written once at fresh initialization and verified on every subsequent Open.
type HomeIdentity struct {
	SchemaVersion int    `json:"schema_version"`
	LayoutVersion int    `json:"layout_version"`
	ID            string `json:"id"`
	CanonicalRoot string `json:"canonical_root"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}

// Home is a canonical, domain-neutral durable-storage home. It is the
// coarse-grained surface for the owner-clean mechanics: verified roots,
// containment and no-follow safety, owner-private permissions, scoped fenced
// locks/leases, and atomic journaled change-set commits. Durable state may not
// bypass Home through raw writes or private lock protocols.
type Home struct {
	root string
	id   HomeIdentity
}

// Identity returns the stable identity of this home.
func (h *Home) Identity() HomeIdentity { return h.id }

// Root returns the canonical physical root of this home.
func (h *Home) Root() string { return h.root }

// Init creates exactly one verified current-v1 home rooted at root, with a
// stable Home Identity. It is idempotent: opening an already-current home
// returns it unchanged. A directory that exists but is not a recognized home,
// or a home whose identity is incompatible or malformed, fails closed.
func Init(root string) (*Home, error) {
	return initWithLstat(root, os.Lstat)
}

func initWithLstat(root string, lstat func(string) (os.FileInfo, error)) (*Home, error) {
	if root == "" {
		return nil, ErrEmptyRoot
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("home: resolve root: %w", err)
	}
	abs = filepath.Clean(abs)

	info, err := lstat(abs)
	switch {
	case err == nil:
		if !info.IsDir() {
			return nil, &incompatibleError{kind: "root is not a directory", path: abs, err: ErrNotDirectory}
		}
		entries, rerr := os.ReadDir(abs)
		if rerr != nil {
			return nil, fmt.Errorf("home: read root: %w", rerr)
		}
		hasIdentity := false
		for _, e := range entries {
			if e.Name() == IdentityFileName {
				hasIdentity = true
				break
			}
		}
		if len(entries) == 0 {
			return createHome(abs)
		}
		if !hasIdentity {
			return nil, &incompatibleError{kind: "non-empty directory is not a munsu home", path: abs, err: ErrNonEmptyHome}
		}
		return Open(abs)
	case os.IsNotExist(err):
		return createHome(abs)
	default:
		return nil, fmt.Errorf("home: stat root: %w", err)
	}
}

// Open opens an existing home and verifies it is the current v1 layout with a
// matching identity. Incompatible or malformed state fails closed. Any
// incomplete write-ahead journal is recovered mechanically before returning.
func Open(root string) (*Home, error) {
	if root == "" {
		return nil, ErrEmptyRoot
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("home: resolve root: %w", err)
	}
	abs = filepath.Clean(abs)

	id, err := readIdentity(abs)
	if err != nil {
		return nil, err
	}
	if err := verifyLayout(abs); err != nil {
		return nil, err
	}
	// The home's owner boundary must be owner-private. A root or logical root
	// whose protection was weakened or tampered with fails closed rather than
	// exposing durable records to other principals.
	if err := verifyHomeProtection(abs); err != nil {
		return nil, err
	}
	h := &Home{root: id.CanonicalRoot, id: id}
	if err := h.recover(); err != nil {
		return nil, err
	}
	return h, nil
}

// RootFor returns the verified on-disk path for a logical root name.
func (h *Home) RootFor(name string) (string, error) {
	rel, ok := canonicalLayoutRoot(name)
	if !ok {
		return "", ErrUnknownRoot
	}
	return filepath.Join(h.root, rel), nil
}

// Path returns a contained, no-follow verified path for key within the logical
// root. key must be relative and must not escape the root via "..", absolute
// paths, or symlinks. The returned path is suitable for reading; durable state
// is written through Commit.
func (h *Home) Path(root, key string) (string, error) {
	rootPath, err := h.RootFor(root)
	if err != nil {
		return "", err
	}
	path, err := joinContained(rootPath, key)
	if err != nil {
		return "", err
	}
	if err := verifyNoFollow(rootPath, path); err != nil {
		return "", err
	}
	return path, nil
}

// Read returns the bytes stored at key within the logical root, or an error if
// the key is absent or escapes the root.
func (h *Home) Read(root, key string) ([]byte, error) {
	path, err := h.Path(root, key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ReadDir returns the directory entries at key within the logical root. It
// resolves and verifies the path through the same contained, no-follow seam as
// Read, so enumeration can never bypass the home security boundary via a raw
// filesystem path. A missing directory or a non-directory target returns the
// native os.ReadDir error unchanged; callers may treat os.ErrNotExist as absence.
func (h *Home) ReadDir(root, key string) ([]os.DirEntry, error) {
	path, err := h.Path(root, key)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(path)
}

func createHome(abs string) (*Home, error) {
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, fmt.Errorf("home: create root: %w", err)
	}
	if err := secureDir(abs); err != nil {
		return nil, fmt.Errorf("home: secure root: %w", err)
	}
	canonical, err := canonicalRoot(abs)
	if err != nil {
		return nil, err
	}
	abs = canonical
	if err := createLayout(abs); err != nil {
		return nil, err
	}
	id := HomeIdentity{
		SchemaVersion: layoutSchemaVersion,
		LayoutVersion: LayoutVersion,
		ID:            newIdentityID(),
		CanonicalRoot: canonical,
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := writeIdentity(abs, id); err != nil {
		return nil, err
	}
	return &Home{root: abs, id: id}, nil
}

func canonicalLayoutRoot(name string) (string, bool) {
	switch name {
	case RootState:
		return CanonicalLayout.State, true
	case RootData:
		return CanonicalLayout.Data, true
	case RootConfig:
		return CanonicalLayout.Config, true
	case RootProjects:
		return CanonicalLayout.Projects, true
	}
	return "", false
}

func createLayout(root string) error {
	for _, dir := range []string{
		root,
		filepath.Join(root, CanonicalLayout.State),
		filepath.Join(root, CanonicalLayout.Data),
		filepath.Join(root, CanonicalLayout.Config),
		filepath.Join(root, CanonicalLayout.Projects),
		filepath.Join(root, JournalDirName),
		filepath.Join(root, LockDirName),
		filepath.Join(root, LeaseDirName),
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("home: create layout %s: %w", dir, err)
		}
		if err := secureDir(dir); err != nil {
			return fmt.Errorf("home: secure layout %s: %w", dir, err)
		}
	}
	return nil
}

// verifyHomeProtection confirms that the home's owner boundary is still
// owner-private: the root and each logical root directory must be accessible
// only by the owner. A home whose protection was weakened or tampered with
// fails closed so durable records are never exposed to other principals.
func verifyHomeProtection(root string) error {
	for _, name := range []string{
		"", // the home root itself
		CanonicalLayout.State,
		CanonicalLayout.Data,
		CanonicalLayout.Config,
		CanonicalLayout.Projects,
	} {
		path := root
		if name != "" {
			path = filepath.Join(root, name)
		}
		if err := verifyProtection(path, true); err != nil {
			return err
		}
	}
	return nil
}

func verifyLayout(root string) error {
	for _, dir := range []string{
		root,
		filepath.Join(root, CanonicalLayout.State),
		filepath.Join(root, CanonicalLayout.Data),
		filepath.Join(root, CanonicalLayout.Config),
		filepath.Join(root, CanonicalLayout.Projects),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			return &incompatibleError{kind: "layout is malformed", path: dir, err: ErrMalformedLayout}
		}
		if !info.IsDir() {
			return &incompatibleError{kind: "layout is not a directory", path: dir, err: ErrMalformedLayout}
		}
	}
	return nil
}

func identityPath(root string) string { return filepath.Join(root, IdentityFileName) }

func writeIdentity(root string, id HomeIdentity) error {
	if err := id.validate(root); err != nil {
		return err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("home: encode identity: %w", err)
	}
	return canonicalAtomicWrite(identityPath(root), append(data, '\n'))
}

func readIdentity(root string) (HomeIdentity, error) {
	data, err := os.ReadFile(identityPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return HomeIdentity{}, ErrNotInitialized
		}
		return HomeIdentity{}, fmt.Errorf("home: read identity: %w", err)
	}
	var id HomeIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		return HomeIdentity{}, &incompatibleError{kind: "identity is malformed", path: identityPath(root), err: ErrMalformedIdentity}
	}
	if err := id.validate(root); err != nil {
		return HomeIdentity{}, err
	}
	return id, nil
}

func (id HomeIdentity) validate(root string) error {
	if id.SchemaVersion != layoutSchemaVersion {
		return &incompatibleError{kind: "unsupported schema", path: identityPath(root), err: ErrUnsupportedSchema}
	}
	if id.LayoutVersion != LayoutVersion {
		return &incompatibleError{kind: "unsupported layout", path: identityPath(root), err: ErrUnsupportedLayout}
	}
	if strings.TrimSpace(id.ID) == "" {
		return &incompatibleError{kind: "identity is malformed", path: identityPath(root), err: ErrMalformedIdentity}
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	if id.CanonicalRoot != canonical {
		return &incompatibleError{kind: "identity root mismatch", path: identityPath(root), err: ErrIdentityMismatch}
	}
	return nil
}

// canonicalRoot returns the physical absolute path of root after resolving
// symlinks. The root must already exist.
func canonicalRoot(root string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("home: resolve canonical root: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func newIdentityID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is effectively unreachable; fall back to a
		// time-based id so initialization can still produce a stable identity.
		return fmt.Sprintf("home-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
