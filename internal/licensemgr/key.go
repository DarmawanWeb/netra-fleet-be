package licensemgr

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

// The signing key is kept in key.enc, encrypted with a passphrase: PBKDF2-SHA256 derives an
// AES-256-GCM key, which seals the Ed25519 seed. Unlocked, the key lives in this process only and
// locks itself after IdleLock without use.
const (
	KDFIterations = 600_000
	IdleLock      = 30 * time.Minute
	MinPassphrase = 12

	kdfName    = "pbkdf2-sha256"
	cipherName = "aes-256-gcm"
	keyLabel   = "netra-license-manager/key/1 " // additional data: binds the sealed seed to its public key
)

var (
	ErrPassphrase = errors.New("wrong passphrase")
	ErrLocked     = errors.New("the signing key is locked")
	ErrNoKey      = errors.New("no signing key yet: generate or import one")
)

// KeyFile is key.enc: the public key in clear, the private key sealed with the passphrase.
type KeyFile struct {
	Schema     int       `json:"schema"`
	PublicKey  string    `json:"public_key"` // base64, the content of a .pub file
	KDF        string    `json:"kdf"`
	Iterations int       `json:"iterations"`
	Salt       string    `json:"salt"`
	Cipher     string    `json:"cipher"`
	Nonce      string    `json:"nonce"`
	Sealed     string    `json:"sealed"` // the Ed25519 seed, encrypted
	Source     string    `json:"source"` // generated or imported
	CreatedAt  time.Time `json:"created_at"`
}

// SealKey encrypts priv with passphrase.
func SealKey(priv ed25519.PrivateKey, passphrase string, iterations int, source string, created time.Time) (*KeyFile, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("not an Ed25519 private key")
	}
	if iterations < 1 {
		return nil, errors.New("no iterations")
	}
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	k := &KeyFile{
		Schema: schema, PublicKey: base64.StdEncoding.EncodeToString(pub), KDF: kdfName, Iterations: iterations,
		Salt: base64.StdEncoding.EncodeToString(salt), Cipher: cipherName,
		Nonce: base64.StdEncoding.EncodeToString(nonce), Source: source, CreatedAt: created.UTC().Truncate(time.Second),
	}
	aead, err := k.aead(passphrase, salt)
	if err != nil {
		return nil, err
	}
	k.Sealed = base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, priv.Seed(), []byte(keyLabel+k.PublicKey)))
	return k, nil
}

func (k *KeyFile) aead(passphrase string, salt []byte) (cipher.AEAD, error) {
	key := auth.DeriveKey([]byte(passphrase), salt, k.Iterations)
	defer wipe(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Open decrypts the private key; ErrPassphrase when the passphrase is wrong.
func (k *KeyFile) Open(passphrase string) (ed25519.PrivateKey, error) {
	if k.KDF != kdfName || k.Cipher != cipherName {
		return nil, fmt.Errorf("key.enc: unsupported %s / %s", k.KDF, k.Cipher)
	}
	if k.Iterations < 1 || k.Iterations > 50_000_000 {
		return nil, fmt.Errorf("key.enc: %d iterations", k.Iterations)
	}
	pub, err := k.Public()
	if err != nil {
		return nil, err
	}
	salt, err1 := base64.StdEncoding.DecodeString(k.Salt)
	nonce, err2 := base64.StdEncoding.DecodeString(k.Nonce)
	sealed, err3 := base64.StdEncoding.DecodeString(k.Sealed)
	if err1 != nil || err2 != nil || err3 != nil || len(salt) < 16 || len(nonce) != 12 {
		return nil, errors.New("key.enc is damaged")
	}
	aead, err := k.aead(passphrase, salt)
	if err != nil {
		return nil, err
	}
	seed, err := aead.Open(nil, nonce, sealed, []byte(keyLabel+k.PublicKey))
	if err != nil {
		return nil, ErrPassphrase
	}
	defer wipe(seed)
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("key.enc is damaged")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if subtle.ConstantTimeCompare(priv[ed25519.SeedSize:], pub) != 1 {
		wipe(priv)
		return nil, errors.New("key.enc: the private key does not match its public key")
	}
	return priv, nil
}

// Public is the public key of the file.
func (k *KeyFile) Public() (ed25519.PublicKey, error) {
	pub, err := license.ParsePublicKey(k.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("key.enc: %w", err)
	}
	return pub, nil
}

// ParseKey reads an Ed25519 private key from a netra-license keygen .key file and checks that
// its public half belongs to it.
func ParseKey(text string) (ed25519.PrivateKey, error) {
	priv, err := license.ParsePrivateKey(text)
	if err != nil {
		return nil, err
	}
	again := ed25519.NewKeyFromSeed(priv.Seed())
	defer wipe(again)
	if subtle.ConstantTimeCompare(again, priv) != 1 {
		return nil, errors.New("the key is damaged: its public half does not match")
	}
	return priv, nil
}

// Fingerprint is a short name of a public key: the start of its SHA-256 in hex, in groups of four.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	h := hex.EncodeToString(sum[:8])
	return strings.Join([]string{h[0:4], h[4:8], h[8:12], h[12:16]}, " ")
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Keyring holds the unlocked signing key of this process. It locks itself (and wipes the key)
// once the key has not been used for idle.
type Keyring struct {
	mu       sync.Mutex
	priv     ed25519.PrivateKey
	lastUsed time.Time
	timer    *time.Timer
	idle     time.Duration
	now      func() time.Time
	onLock   func(reason string) // called after an automatic lock
}

func newKeyring(idle time.Duration, now func() time.Time, onLock func(string)) *Keyring {
	return &Keyring{idle: idle, now: now, onLock: onLock}
}

// Unlock holds priv as the signing key.
func (k *Keyring) Unlock(priv ed25519.PrivateKey) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.wipeLocked()
	k.priv = priv
	k.lastUsed = k.now()
	k.arm(k.idle)
}

// Lock wipes the key; it reports whether it was unlocked.
func (k *Keyring) Lock() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	was := k.priv != nil
	k.wipeLocked()
	return was
}

// Status tells whether the key is unlocked and when it will lock without use.
func (k *Keyring) Status() (bool, time.Time) {
	if k.lockIfIdle() {
		return false, time.Time{}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.priv == nil {
		return false, time.Time{}
	}
	return true, k.lastUsed.Add(k.idle)
}

// Sign signs l with the unlocked key.
func (k *Keyring) Sign(l license.License) (string, error) {
	if k.lockIfIdle() {
		return "", ErrLocked
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.priv == nil {
		return "", ErrLocked
	}
	k.lastUsed = k.now()
	return license.Sign(l, k.priv)
}

// lockIfIdle locks a key whose idle time is over (the timer may not have run yet).
func (k *Keyring) lockIfIdle() bool {
	k.mu.Lock()
	if k.priv == nil || k.now().Sub(k.lastUsed) < k.idle {
		k.mu.Unlock()
		return false
	}
	k.wipeLocked()
	k.mu.Unlock()
	if k.onLock != nil {
		k.onLock("idle")
	}
	return true
}

func (k *Keyring) wipeLocked() {
	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
	wipe(k.priv)
	k.priv = nil
}

func (k *Keyring) arm(d time.Duration) {
	if k.timer != nil {
		k.timer.Stop()
	}
	k.timer = time.AfterFunc(d, k.expire)
}

// expire runs when the idle time may be over: it locks, or waits for the rest of it.
func (k *Keyring) expire() {
	if k.lockIfIdle() {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.priv != nil {
		k.arm(k.lastUsed.Add(k.idle).Sub(k.now()))
	}
}
