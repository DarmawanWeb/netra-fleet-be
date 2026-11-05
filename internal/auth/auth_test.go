package auth

import (
	"encoding/hex"
	"testing"
)

// The usual PBKDF2-HMAC-SHA256 vectors for P "password", S "salt" and 32 bytes.
func TestDeriveKeyVectors(t *testing.T) {
	for _, c := range []struct {
		iter int
		want string
	}{
		{1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{4096, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
	} {
		if got := hex.EncodeToString(DeriveKey([]byte("password"), []byte("salt"), c.iter)); got != c.want {
			t.Errorf("%d iterations: %s, want %s", c.iter, got, c.want)
		}
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	h := HashPassword("rahasia")
	if !CheckPassword("rahasia", h) || CheckPassword("salah", h) || CheckPassword("rahasia", "garbage") {
		t.Fatal("password check failed")
	}
}

func TestTokenHashIsStable(t *testing.T) {
	tok := NewToken()
	first, again := HashToken(tok), HashToken(tok)
	if first != again || first == HashToken(NewToken()) {
		t.Fatal("token hash not stable/unique")
	}
}
