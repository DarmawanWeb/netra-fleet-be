// Package licensemgr is the NETRA License Manager: the vendor's tool to keep its clients and their
// deployments, sign licenses with a passphrase-protected key and hand them over. Everything is a
// JSON file in one folder, so a backup is a copy of that folder. It is never shipped to clients.
package licensemgr

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

// schema is the version of the files this manager writes. A file from a newer manager is refused
// instead of being rewritten without the fields it does not know.
const schema = 1

// Files and folders of the data folder.
const (
	clientsDir  = "clients"  // <id>.json: a client and its deployments
	licensesDir = "licenses" // <id>.json: an issued license; <id>.lic: its text
	usersFile   = "users.json"
	keyFileName = "key.enc"   // the signing key, encrypted with a passphrase
	auditFile   = "audit.log" // one JSON line per action
	tmpPrefix   = ".tmp-"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")

	idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// User is an operator of the manager.
type User struct {
	Username  string     `json:"username"`
	Password  string     `json:"password"` // auth.HashPassword
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by,omitempty"`
	LastLogin *time.Time `json:"last_login,omitempty"`
}

// Client is a company that buys NETRA.
type Client struct {
	Schema      int          `json:"schema"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Contact     string       `json:"contact,omitempty"`
	Notes       string       `json:"notes,omitempty"`
	Deployments []Deployment `json:"deployments"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// Deployment is a site of a client: one NETRA server, known by its installation ID.
type Deployment struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Installation string    `json:"installation,omitempty"` // NETRA-XXXX-XXXX-XXXX; empty until known
	Notes        string    `json:"notes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Deployment returns the deployment of c with this ID.
func (c Client) Deployment(id string) (Deployment, bool) {
	for _, d := range c.Deployments {
		if d.ID == id {
			return d, true
		}
	}
	return Deployment{}, false
}

// Kinds of issued licenses.
const (
	KindIssue  = "issue"  // a new license
	KindRenew  = "renew"  // the same terms with new dates
	KindChange = "change" // new terms replacing a license
)

// Record is an issued license as the manager keeps it.
type Record struct {
	Schema       int             `json:"schema"`
	ID           string          `json:"id"` // the license ID
	ClientID     string          `json:"client_id"`
	DeploymentID string          `json:"deployment_id,omitempty"`
	License      license.License `json:"license"` // the signed payload
	Text         string          `json:"text"`    // NETRA-LIC-1.<payload>.<signature>
	Key          string          `json:"key"`     // fingerprint of the signing key
	IssuedBy     string          `json:"issued_by"`
	Note         string          `json:"note,omitempty"`
	Kind         string          `json:"kind"`
	Supersedes   string          `json:"supersedes,omitempty"` // the license this one replaces
}

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	Time   time.Time      `json:"time"`
	User   string         `json:"user"`
	Action string         `json:"action"`
	Target string         `json:"target,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
	IP     string         `json:"ip,omitempty"`
}

// Store is the data folder. Clients, operators and a summary of each license are kept in memory;
// a license's text and logo are read from its file when needed. One process at a time uses a
// folder (a lock file guards it).
type Store struct {
	dir    string
	unlock func()

	mu       sync.RWMutex
	users    []User
	clients  map[string]Client
	licenses map[string]Record // without Text and the branding logo
	logos    map[string]bool   // licenses with a logo
}

// OpenStore opens (or creates) the data folder dir.
func OpenStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("no data folder")
	}
	for _, d := range []string{dir, filepath.Join(dir, clientsDir), filepath.Join(dir, licensesDir)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
		_ = os.Chmod(d, 0o700) // also a folder made beforehand: the records are private
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, unlock: unlock, clients: map[string]Client{}, licenses: map[string]Record{},
		logos: map[string]bool{}}
	if err := s.load(); err != nil {
		unlock()
		return nil, err
	}
	return s, nil
}

// Dir is the data folder.
func (s *Store) Dir() string { return s.dir }

// Close releases the folder.
func (s *Store) Close() error {
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
	return nil
}

func (s *Store) load() error {
	var users struct {
		Schema int    `json:"schema"`
		Users  []User `json:"users"`
	}
	switch err := loadJSON(filepath.Join(s.dir, usersFile), &users); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		s.users = users.Users
	}
	err := eachJSON(filepath.Join(s.dir, clientsDir), func(path string) error {
		var c Client
		if err := loadJSON(path, &c); err != nil {
			return err
		}
		if c.ID+".json" != filepath.Base(path) {
			return fmt.Errorf("%s: holds client %q", path, c.ID)
		}
		s.clients[c.ID] = c
		return nil
	})
	if err != nil {
		return err
	}
	return eachJSON(filepath.Join(s.dir, licensesDir), func(path string) error {
		var r Record
		if err := loadJSON(path, &r); err != nil {
			return err
		}
		if r.ID+".json" != filepath.Base(path) {
			return fmt.Errorf("%s: holds license %q", path, r.ID)
		}
		s.index(r)
		return nil
	})
}

// eachJSON calls fn for each .json file of dir and removes temporary files left by a crash.
func eachJSON(dir string, fn func(path string) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir():
		case strings.HasPrefix(name, tmpPrefix):
			_ = os.Remove(filepath.Join(dir, name))
		case strings.HasSuffix(name, ".json"):
			if err := fn(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) index(r Record) {
	s.logos[r.ID] = r.License.Branding.Logo != ""
	r.Text, r.License.Branding.Logo = "", ""
	s.licenses[r.ID] = r
}

// loadJSON reads a file written by saveJSON, refusing one without a schema or from a newer manager.
func loadJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	switch {
	case head.Schema < 1:
		return fmt.Errorf("%s: no schema version, not a license manager file", path)
	case head.Schema > schema:
		return fmt.Errorf("%s: schema %d is from a newer license manager (this one writes %d)", path, head.Schema, schema)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func saveJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return saveFile(path, append(raw, '\n'))
}

// saveFile replaces path atomically: a temporary file in the same folder (mode 0600), synced,
// then renamed over it. A crash leaves the old file or the new one, never half of one.
func saveFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, tmpPrefix+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // gone after the rename
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // make the rename durable
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b)
}

// Users.

// HasUsers reports whether an operator exists (the first run creates one).
func (s *Store) HasUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users) > 0
}

// Users lists the operators.
func (s *Store) Users() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]User(nil), s.users...)
}

// User returns the operator with this username.
func (s *Store) User(name string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Username == name {
			return u, true
		}
	}
	return User{}, false
}

// AddUser adds an operator; first refuses when one already exists (the first-run setup).
func (s *Store) AddUser(u User, first bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if first && len(s.users) > 0 {
		return ErrExists
	}
	for _, x := range s.users {
		if x.Username == u.Username {
			return ErrExists
		}
	}
	return s.saveUsers(append(append([]User(nil), s.users...), u))
}

// UpdateUser applies change to the operator with this username and saves it.
func (s *Store) UpdateUser(name string, change func(*User)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := append([]User(nil), s.users...)
	for i := range users {
		if users[i].Username == name {
			change(&users[i])
			return s.saveUsers(users)
		}
	}
	return ErrNotFound
}

// DeleteUser removes an operator; the last one stays.
func (s *Store) DeleteUser(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := make([]User, 0, len(s.users))
	for _, u := range s.users {
		if u.Username != name {
			users = append(users, u)
		}
	}
	switch {
	case len(users) == len(s.users):
		return ErrNotFound
	case len(users) == 0:
		return errors.New("the last operator cannot be removed")
	}
	return s.saveUsers(users)
}

func (s *Store) saveUsers(users []User) error {
	file := struct {
		Schema int    `json:"schema"`
		Users  []User `json:"users"`
	}{schema, users}
	if err := saveJSON(filepath.Join(s.dir, usersFile), file); err != nil {
		return err
	}
	s.users = users
	return nil
}

// Clients.

// Clients lists the clients by name.
func (s *Store) Clients() []Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Client, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, c.clone())
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Client returns the client with this ID.
func (s *Store) Client(id string) (Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[id]
	return c.clone(), ok
}

// clone copies the deployments too, so a caller changing them does not change the store.
func (c Client) clone() Client {
	if c.Deployments != nil {
		c.Deployments = append([]Deployment{}, c.Deployments...)
	}
	return c
}

// SaveClient creates or replaces a client. A new client without an ID gets one.
func (s *Store) SaveClient(c Client) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c = c.clone()
	if c.ID == "" {
		for c.ID == "" || s.clients[c.ID].ID != "" {
			c.ID = newID(5)
		}
	}
	if !idPattern.MatchString(c.ID) {
		return c, fmt.Errorf("invalid client ID %q", c.ID)
	}
	for i := range c.Deployments {
		if c.Deployments[i].ID == "" {
			c.Deployments[i].ID = s.newDeploymentID()
		}
		if !idPattern.MatchString(c.Deployments[i].ID) {
			return c, fmt.Errorf("invalid deployment ID %q", c.Deployments[i].ID)
		}
	}
	if c.Deployments == nil {
		c.Deployments = []Deployment{}
	}
	c.Schema = schema
	if err := saveJSON(filepath.Join(s.dir, clientsDir, c.ID+".json"), c); err != nil {
		return c, err
	}
	s.clients[c.ID] = c
	return c.clone(), nil
}

func (s *Store) newDeploymentID() string {
	for {
		id := newID(5)
		taken := false
		for _, c := range s.clients {
			if _, ok := c.Deployment(id); ok {
				taken = true
			}
		}
		if !taken {
			return id
		}
	}
}

// DeleteClient removes a client without licenses.
func (s *Store) DeleteClient(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[id]; !ok {
		return ErrNotFound
	}
	for _, r := range s.licenses {
		if r.ClientID == id {
			return errors.New("the client has licenses; they are the record of what was sold")
		}
	}
	if err := os.Remove(filepath.Join(s.dir, clientsDir, id+".json")); err != nil {
		return err
	}
	delete(s.clients, id)
	return nil
}

// Installation finds the deployment bound to an installation ID.
func (s *Store) Installation(id string) (Client, Deployment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id = license.NormalizeInstallation(id)
	for _, c := range s.clients {
		for _, d := range c.Deployments {
			if d.Installation != "" && d.Installation == id {
				return c.clone(), d, true
			}
		}
	}
	return Client{}, Deployment{}, false
}

// Licenses.

// SaveLicense stores a newly issued license: its record and its text (<id>.lic). An ID is never
// reused.
func (s *Store) SaveLicense(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(r.ID) {
		return fmt.Errorf("invalid license ID %q", r.ID)
	}
	if _, ok := s.licenses[r.ID]; ok {
		return ErrExists
	}
	r.Schema = schema
	base := filepath.Join(s.dir, licensesDir, r.ID)
	if err := saveFile(base+".lic", []byte(r.Text+"\n")); err != nil {
		return err
	}
	if err := saveJSON(base+".json", r); err != nil {
		return err
	}
	s.index(r)
	return nil
}

// Licenses lists the issued licenses, newest first, without their text and logo.
func (s *Store) Licenses() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0, len(s.licenses))
	for _, r := range s.licenses {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].License.IssuedAt, out[j].License.IssuedAt
		if a.Equal(b) {
			return out[i].ID > out[j].ID
		}
		return a.After(b)
	})
	return out
}

// HasLogo reports whether the license id carries a logo.
func (s *Store) HasLogo(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.logos[id]
}

// License reads the complete record of the license id.
func (s *Store) License(id string) (Record, error) {
	var r Record
	if !idPattern.MatchString(id) {
		return r, ErrNotFound
	}
	s.mu.RLock()
	_, ok := s.licenses[id]
	s.mu.RUnlock()
	if !ok {
		return r, ErrNotFound
	}
	err := loadJSON(filepath.Join(s.dir, licensesDir, id+".json"), &r)
	return r, err
}

// SupersededBy maps a license ID to the newest license that replaces it.
func (s *Store) SupersededBy() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for _, r := range s.licenses {
		if r.Supersedes == "" {
			continue
		}
		if prev, ok := out[r.Supersedes]; !ok || s.licenses[prev].License.IssuedAt.Before(r.License.IssuedAt) {
			out[r.Supersedes] = r.ID
		}
	}
	return out
}

// Signing key.

// KeyFile reads the encrypted signing key; nil when there is none yet.
func (s *Store) KeyFile() (*KeyFile, error) {
	var k KeyFile
	err := loadJSON(filepath.Join(s.dir, keyFileName), &k)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// SaveKeyFile writes the encrypted signing key. A new key never replaces another (replace is for
// a new passphrase of the same key).
func (s *Store) SaveKeyFile(k *KeyFile, replace bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, keyFileName)
	old, err := s.KeyFile()
	if err != nil {
		return err
	}
	switch {
	case old != nil && !replace:
		return ErrExists
	case old != nil && old.PublicKey != k.PublicKey:
		return errors.New("a different key cannot replace the signing key")
	case old == nil && replace:
		return ErrNotFound
	}
	k.Schema = schema
	return saveJSON(path, k)
}

// Audit log.

// Audit appends an entry to the audit log.
func (s *Store) Audit(e AuditEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.dir, auditFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// AuditLog returns the last limit entries, newest first. It reads the end of the file only.
func (s *Store) AuditLog(limit int) ([]AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, err := os.Open(filepath.Join(s.dir, auditFile))
	if errors.Is(err, os.ErrNotExist) {
		return []AuditEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	for chunk := int64(64 << 10); ; chunk *= 4 {
		start := max(size-chunk, 0)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
		if start > 0 {
			lines = lines[1:] // the first line may be cut
		}
		if len(lines) > limit || start == 0 {
			out := make([]AuditEntry, 0, limit)
			for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
				var e AuditEntry
				if len(lines[i]) == 0 || json.Unmarshal(lines[i], &e) != nil {
					continue
				}
				out = append(out, e)
			}
			return out, nil
		}
	}
}
