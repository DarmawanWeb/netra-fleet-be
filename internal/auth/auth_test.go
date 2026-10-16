package auth

import "testing"

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
