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
	if HashToken(tok) != HashToken(tok) || HashToken(tok) == HashToken(NewToken()) {
		t.Fatal("token hash not stable/unique")
	}
}
