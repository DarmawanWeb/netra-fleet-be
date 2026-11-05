package licensemgr

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"
)

type keyView struct {
	Exists      bool       `json:"exists"`
	Unlocked    bool       `json:"unlocked"`
	LocksAt     *time.Time `json:"locks_at,omitempty"`
	IdleMinutes int        `json:"idle_minutes"`
	PublicKey   string     `json:"public_key,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	FileName    string     `json:"file_name,omitempty"` // a name for the .pub file
	Source      string     `json:"source,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	Iterations  int        `json:"iterations,omitempty"`
	// Trusted: this build of NETRA accepts the manager's licenses (its key is built in).
	Trusted     bool   `json:"trusted"`
	TrustedKeys int    `json:"trusted_keys"`
	TrustError  string `json:"trust_error,omitempty"`
}

func (s *Server) keyView() (keyView, error) {
	v := keyView{IdleMinutes: int(s.keys.idle / time.Minute)}
	kf, err := s.store.KeyFile()
	if err != nil || kf == nil {
		return v, err
	}
	pub, err := kf.Public()
	if err != nil {
		return v, err
	}
	created := kf.CreatedAt
	v.Exists, v.PublicKey, v.Source, v.CreatedAt, v.Iterations = true, kf.PublicKey, kf.Source, &created, kf.Iterations
	v.Fingerprint = Fingerprint(pub)
	v.FileName = "ezra-" + strings.ReplaceAll(v.Fingerprint, " ", "")[:8] + ".pub"
	if unlocked, at := s.keys.Status(); unlocked {
		v.Unlocked, v.LocksAt = true, &at
	}
	trusted, err := s.trusted()
	if err != nil {
		v.TrustError = err.Error()
	}
	v.TrustedKeys = len(trusted)
	for _, k := range trusted {
		if bytes.Equal(k, pub) {
			v.Trusted = true
		}
	}
	return v, nil
}

func (s *Server) replyKey(w http.ResponseWriter, status int) {
	v, err := s.keyView()
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, status, v)
}

func (s *Server) keyStatus(w http.ResponseWriter, _ *http.Request, _ string) {
	s.replyKey(w, http.StatusOK)
}

// keyPublic downloads the public key as a .pub file for internal/license/keys/.
func (s *Server) keyPublic(w http.ResponseWriter, _ *http.Request, _ string) {
	v, err := s.keyView()
	if err != nil || !v.Exists {
		replyErr(w, errors.Join(ErrNotFound, err))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": v.FileName}))
	_, _ = w.Write([]byte(v.PublicKey + "\n"))
}

type passphraseBody struct {
	Key        string `json:"key"` // import: the content of a .key file
	Passphrase string `json:"passphrase"`
}

func checkPassphrase(field, p string) error {
	if len([]rune(p)) < MinPassphrase {
		return fieldErr(field, "passphrase: at least %d characters", MinPassphrase)
	}
	return nil
}

// keyGenerate makes a new key pair, keeps it encrypted and unlocked.
func (s *Server) keyGenerate(w http.ResponseWriter, r *http.Request, user string) {
	var body passphraseBody
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		replyErr(w, err)
		return
	}
	s.storeKey(w, r, user, priv, body.Passphrase, "generated")
}

// keyImport keeps a key made by netra-license keygen, encrypted and unlocked.
func (s *Server) keyImport(w http.ResponseWriter, r *http.Request, user string) {
	var body passphraseBody
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	priv, err := ParseKey(body.Key)
	if err != nil {
		replyErr(w, fieldErr("key", "%v", err))
		return
	}
	s.storeKey(w, r, user, priv, body.Passphrase, "imported")
}

func (s *Server) storeKey(w http.ResponseWriter, r *http.Request, user string, priv ed25519.PrivateKey, passphrase, source string) {
	if err := checkPassphrase("passphrase", passphrase); err != nil {
		replyErr(w, err)
		return
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	kf, err := SealKey(priv, passphrase, s.iterations, source, s.now())
	if err != nil {
		replyErr(w, err)
		return
	}
	err = s.store.SaveKeyFile(kf, false)
	if errors.Is(err, ErrExists) {
		replyError(w, http.StatusConflict, "there is a signing key already; it is never replaced from here")
		return
	}
	if err != nil {
		replyErr(w, err)
		return
	}
	s.keys.Unlock(priv)
	pub, _ := kf.Public()
	action := map[string]string{"generated": "key.generate", "imported": "key.import"}[source]
	s.record(r, user, action, Fingerprint(pub), nil)
	s.replyKey(w, http.StatusCreated)
}

func (s *Server) keyUnlock(w http.ResponseWriter, r *http.Request, user string) {
	key := "unlock " + clientIP(r)
	if s.limiter.blocked(key, s.now()) {
		replyError(w, http.StatusTooManyRequests, "too many wrong passphrases: try again in 10 minutes")
		return
	}
	var body passphraseBody
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	kf, err := s.store.KeyFile()
	if err != nil || kf == nil {
		replyErr(w, errors.Join(ErrNoKey, err))
		return
	}
	priv, err := kf.Open(body.Passphrase)
	if errors.Is(err, ErrPassphrase) {
		s.limiter.fail(key, s.now())
		s.record(r, user, "key.unlock_failed", "", nil)
		replyErr(w, fieldErr("passphrase", "wrong passphrase"))
		return
	}
	if err != nil {
		replyErr(w, err)
		return
	}
	s.limiter.reset(key)
	s.keys.Unlock(priv)
	pub, _ := kf.Public()
	s.record(r, user, "key.unlock", Fingerprint(pub), nil)
	s.replyKey(w, http.StatusOK)
}

func (s *Server) keyLock(w http.ResponseWriter, r *http.Request, user string) {
	if s.keys.Lock() {
		s.record(r, user, "key.lock", "", map[string]any{"reason": "manual"})
	}
	s.replyKey(w, http.StatusOK)
}

// keyPassphrase encrypts the key again with a new passphrase.
func (s *Server) keyPassphrase(w http.ResponseWriter, r *http.Request, user string) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	if err := checkPassphrase("new", body.New); err != nil {
		replyErr(w, err)
		return
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	kf, err := s.store.KeyFile()
	if err != nil || kf == nil {
		replyErr(w, errors.Join(ErrNoKey, err))
		return
	}
	priv, err := kf.Open(body.Current)
	if errors.Is(err, ErrPassphrase) {
		replyErr(w, fieldErr("current", "wrong passphrase"))
		return
	}
	if err != nil {
		replyErr(w, err)
		return
	}
	defer wipe(priv)
	next, err := SealKey(priv, body.New, s.iterations, kf.Source, kf.CreatedAt)
	if err != nil {
		replyErr(w, err)
		return
	}
	if err := s.store.SaveKeyFile(next, true); err != nil {
		replyErr(w, err)
		return
	}
	pub, _ := kf.Public()
	s.record(r, user, "key.passphrase", Fingerprint(pub), nil)
	s.replyKey(w, http.StatusOK)
}
