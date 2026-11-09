package random

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBytes(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 32} {
		b := Bytes(n)
		if b == nil || len(b) != max(n, 0) {
			t.Errorf("Bytes(%d) = %#v, want non-nil with length %d", n, b, max(n, 0))
		}
	}
	a, b := Bytes(16), Bytes(16)
	if string(a) == string(b) {
		t.Error("two calls to Bytes(16) returned the same value")
	}
}

func TestToken(t *testing.T) {
	cases := []struct {
		nBytes, wantLen int
	}{
		{-1, 0},
		{0, 0},
		{1, 2},
		{32, 43},
	}
	for _, c := range cases {
		tok := Token(c.nBytes)
		if len(tok) != c.wantLen {
			t.Errorf("Token(%d) length = %d, want %d", c.nBytes, len(tok), c.wantLen)
		}
		if b, err := base64.RawURLEncoding.DecodeString(tok); err != nil || len(b) != max(c.nBytes, 0) {
			t.Errorf("Token(%d) = %q does not decode to %d bytes: %v", c.nBytes, tok, c.nBytes, err)
		}
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		n        int
		alphabet string
		wantLen  int
	}{
		{6, Digits, 6},
		{20, Readable, 20},
		{5, "甲乙丙", 5},
		{0, Digits, 0},
		{-3, Digits, 0},
		{5, "", 0},
		{4, "x", 4},
	}
	for _, c := range cases {
		s := String(c.n, c.alphabet)
		if utf8.RuneCountInString(s) != c.wantLen {
			t.Errorf("String(%d, %q) = %q, want %d characters", c.n, c.alphabet, s, c.wantLen)
		}
		for _, r := range s {
			if !strings.ContainsRune(c.alphabet, r) {
				t.Errorf("String(%d, %q) = %q contains %q outside the alphabet", c.n, c.alphabet, s, r)
			}
		}
	}
}

func TestStringCoversAlphabet(t *testing.T) {
	seen := map[rune]bool{}
	for _, r := range String(2000, Digits) {
		seen[r] = true
	}
	if len(seen) != len(Digits) {
		t.Errorf("2000 digits used only %d of %d symbols", len(seen), len(Digits))
	}
}

func TestHashAndVerifyToken(t *testing.T) {
	tok := Token(32)
	hash := HashToken(tok)
	if len(hash) != 64 {
		t.Errorf("HashToken length = %d, want 64", len(hash))
	}
	if HashToken("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("HashToken(abc) = %s, want the SHA-256 of \"abc\"", HashToken("abc"))
	}
	cases := []struct {
		token, hash string
		want        bool
	}{
		{tok, hash, true},
		{tok + "x", hash, false},
		{tok, strings.ToUpper(hash), false},
		{tok, "", false},
		{"", HashToken(""), true},
	}
	for _, c := range cases {
		if got := VerifyToken(c.token, c.hash); got != c.want {
			t.Errorf("VerifyToken(%q, %q) = %v, want %v", c.token, c.hash, got, c.want)
		}
	}
}
