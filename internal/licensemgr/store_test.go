package licensemgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

var day = time.Date(2026, 11, 4, 3, 0, 8, 0, time.UTC)

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil { // made beforehand, too open
		t.Fatal(err)
	}
	st := openStore(t, dir)
	user := User{Username: "agus", Password: "pbkdf2-sha256$1$c2FsdA$aGFzaA", CreatedAt: day}
	if err := st.AddUser(user, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddUser(User{Username: "other", CreatedAt: day}, true); !errors.Is(err, ErrExists) {
		t.Fatalf("a second first operator: %v", err)
	}
	c, err := st.SaveClient(Client{Name: "PLN UPT Bekasi", Contact: "Budi, +62 811 000", Notes: "substations",
		CreatedAt: day, UpdatedAt: day,
		Deployments: []Deployment{{Name: "GI Cawang", Installation: "NETRA-7KQ3-M9XD-2WPA", CreatedAt: day, UpdatedAt: day}}})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Deployments[0].ID == "" {
		t.Fatalf("no IDs given: %+v", c)
	}
	rec := Record{
		ID: "20261104-0123456789ab", ClientID: c.ID, DeploymentID: c.Deployments[0].ID,
		License: license.License{Product: license.Product, ID: "20261104-0123456789ab", Client: c.Name,
			Edition: "substation", Tier: "pro", Features: []string{"substation"}, Map: license.MapGPS, MaxRobots: 3,
			Installation: "NETRA-7KQ3-M9XD-2WPA", IssuedAt: day, ExpiresAt: day.AddDate(1, 0, 0),
			Branding: license.Branding{Name: "NETRA Gardu", Color: "#0f766e", ColorDark: "#0f766e", Logo: "data:image/png;base64,iVBORw0KGgo="}},
		Text: "NETRA-LIC-1.cGF5bG9hZA.c2ln", Key: "0123 4567 89ab cdef", IssuedBy: "agus", Note: "PO 17/2026", Kind: KindIssue,
	}
	if err := st.SaveLicense(rec); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveLicense(rec); !errors.Is(err, ErrExists) {
		t.Fatalf("a license ID reused: %v", err)
	}
	kf := &KeyFile{PublicKey: "pub", KDF: kdfName, Iterations: KDFIterations, Salt: "s", Cipher: cipherName,
		Nonce: "n", Sealed: "x", Source: "generated", CreatedAt: day}
	if err := st.SaveKeyFile(kf, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveKeyFile(&KeyFile{PublicKey: "other"}, false); !errors.Is(err, ErrExists) {
		t.Fatalf("a second key: %v", err)
	}
	for i := range 3 {
		if err := st.Audit(AuditEntry{Time: day.Add(time.Duration(i) * time.Minute), User: "agus", Action: fmt.Sprint("a", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Files 0600, folders 0700, no temporary file left, the .lic is the text.
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: mode %o, want %o", path, info.Mode().Perm(), want)
		}
		if strings.HasPrefix(d.Name(), tmpPrefix) {
			t.Errorf("temporary file left: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, licensesDir, rec.ID+".lic")); string(raw) != rec.Text+"\n" {
		t.Fatalf(".lic: %q", raw)
	}

	st = openStore(t, dir)
	defer st.Close()
	if got := st.Users(); len(got) != 1 || !reflect.DeepEqual(got[0], user) {
		t.Fatalf("users: %+v", got)
	}
	c.Schema = schema
	if got, ok := st.Client(c.ID); !ok || !reflect.DeepEqual(got, c) {
		t.Fatalf("client: %+v, want %+v", got, c)
	}
	if got, _, ok := st.Installation("netra-7kq3-m9xd-2wpa"); !ok || got.ID != c.ID {
		t.Fatal("installation lookup")
	}
	// A client handed out is a copy: changing its deployments does not change the store.
	copied, _ := st.Client(c.ID)
	copied.Deployments[0].Installation = "NETRA-AAAA-BBBB-CCCC"
	if got, _ := st.Client(c.ID); got.Deployments[0].Installation != "NETRA-7KQ3-M9XD-2WPA" {
		t.Fatal("the store shares its deployments with callers")
	}
	rec.Schema = schema
	if got, err := st.License(rec.ID); err != nil || !reflect.DeepEqual(got, rec) {
		t.Fatalf("license: %+v %v", got, err)
	}
	list := st.Licenses()
	if len(list) != 1 || list[0].Text != "" || list[0].License.Branding.Logo != "" || !st.HasLogo(rec.ID) {
		t.Fatalf("license list keeps the text or the logo: %+v", list)
	}
	if got, err := st.KeyFile(); err != nil || got.PublicKey != "pub" || got.Iterations != KDFIterations {
		t.Fatalf("key file: %+v %v", got, err)
	}
	entries, err := st.AuditLog(2)
	if err != nil || len(entries) != 2 || entries[0].Action != "a2" || entries[1].Action != "a1" {
		t.Fatalf("audit: %+v %v", entries, err)
	}
	if _, err := st.License("../users"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("path outside the folder: %v", err)
	}
}

func TestStoreRefusesNewerFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, clientsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"schema": schema + 1, "id": "abc", "name": "Future"})
	if err := os.WriteFile(filepath.Join(dir, clientsDir, "abc.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("a file of a newer manager opened: %v", err)
	}
}

func TestStoreIsLocked(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t, dir)
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("two managers on one folder")
	}
	_ = st.Close()
	openStore(t, dir).Close()
}

func TestAuditLogReadsTheTail(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t, dir)
	defer st.Close()
	var sb strings.Builder
	for i := range 3000 { // more than one read chunk
		line, _ := json.Marshal(AuditEntry{Time: day, User: "agus", Action: fmt.Sprint("action-", i), Target: strings.Repeat("x", 40)})
		sb.Write(append(line, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, auditFile), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	last, err := st.AuditLog(5)
	if err != nil || len(last) != 5 || last[0].Action != "action-2999" || last[4].Action != "action-2995" {
		t.Fatalf("tail: %+v %v", last, err)
	}
	all, err := st.AuditLog(10_000)
	if err != nil || len(all) != 3000 || all[2999].Action != "action-0" {
		t.Fatalf("whole log: %d %v", len(all), err)
	}
}
