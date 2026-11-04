package licensemgr

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func base64Key(k []byte) string { return base64.StdEncoding.EncodeToString(k) }

func TestKeyEncryptionRoundTrip(t *testing.T) {
	pub, priv := newKey(t)
	kf, err := SealKey(priv, "correct horse battery staple", KDFIterations, "generated", day)
	if err != nil {
		t.Fatal(err)
	}
	if kf.Iterations < 600_000 || kf.KDF != "pbkdf2-sha256" || kf.Cipher != "aes-256-gcm" {
		t.Fatalf("key file parameters: %+v", kf)
	}
	if kf.PublicKey != base64Key(pub) {
		t.Fatal("public key not kept in clear")
	}
	raw, _ := json.Marshal(kf)
	for _, secret := range []string{base64Key(priv), base64Key(priv.Seed()),
		hex.EncodeToString(priv.Seed())} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("the private key is in the file in clear")
		}
	}
	got, err := kf.Open("correct horse battery staple")
	if err != nil || !bytes.Equal(got, priv) {
		t.Fatalf("open: %v", err)
	}
	if _, err := kf.Open("correct horse battery stapler"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
}

func TestKeyFileTampering(t *testing.T) {
	_, priv := newKey(t)
	other, _ := newKey(t)
	kf, err := SealKey(priv, "a long passphrase", 1000, "generated", day)
	if err != nil {
		t.Fatal(err)
	}
	swapped := *kf
	swapped.PublicKey = base64Key(other)
	if _, err := swapped.Open("a long passphrase"); err == nil {
		t.Fatal("a key file with another public key opened")
	}
	fewer := *kf
	fewer.Iterations = 999
	if _, err := fewer.Open("a long passphrase"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("other iterations: %v", err)
	}
	damaged := *kf
	damaged.Nonce = "AAAA"
	if _, err := damaged.Open("a long passphrase"); err == nil {
		t.Fatal("a damaged key file opened")
	}
}

func TestParseKeyFromKeygen(t *testing.T) {
	_, priv := newKey(t)
	text := base64Key(priv) + "\n" // what netra-license keygen writes
	got, err := ParseKey(text)
	if err != nil || !bytes.Equal(got, priv) {
		t.Fatalf("parse: %v", err)
	}
	broken := append(ed25519.PrivateKey(nil), priv...)
	broken[40] ^= 1 // the public half no longer matches the seed
	if _, err := ParseKey(base64Key(broken)); err == nil {
		t.Fatal("a damaged key accepted")
	}
	for _, bad := range []string{"", "not base64", base64Key(priv[:32])} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestKeyringLocksAfterIdle(t *testing.T) {
	pub, priv := newKey(t)
	clock := &fakeClock{t: day}
	var mu sync.Mutex
	var reasons []string
	k := newKeyring(IdleLock, clock.Now, func(r string) { mu.Lock(); reasons = append(reasons, r); mu.Unlock() })
	defer k.Lock()
	l := license.License{ID: "x", Client: "c", Edition: "custom", Tier: "basic", IssuedAt: day}
	if _, err := k.Sign(l); !errors.Is(err, ErrLocked) {
		t.Fatalf("signed while locked: %v", err)
	}
	k.Unlock(append(ed25519.PrivateKey(nil), priv...))
	text, err := k.Sign(l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := license.Verify(text, []ed25519.PublicKey{pub}); err != nil {
		t.Fatal(err)
	}
	clock.Add(29 * time.Minute)
	if _, err := k.Sign(l); err != nil { // a use restarts the idle time
		t.Fatal(err)
	}
	clock.Add(29 * time.Minute)
	if unlocked, at := k.Status(); !unlocked || !at.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("status: %v %v", unlocked, at)
	}
	clock.Add(time.Minute)
	if _, err := k.Sign(l); !errors.Is(err, ErrLocked) {
		t.Fatalf("signed after 30 idle minutes: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reasons) != 1 || reasons[0] != "idle" {
		t.Fatalf("lock reported %v", reasons)
	}
}

func TestKeyringTimerWipesTheKey(t *testing.T) {
	_, priv := newKey(t)
	held := append(ed25519.PrivateKey(nil), priv...)
	locked := make(chan struct{})
	k := newKeyring(20*time.Millisecond, time.Now, func(string) { close(locked) })
	k.Unlock(held)
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("the key did not lock itself")
	}
	if unlocked, _ := k.Status(); unlocked {
		t.Fatal("still unlocked")
	}
	if !bytes.Equal(held, make([]byte, len(held))) {
		t.Fatal("the key was not wiped")
	}
}
