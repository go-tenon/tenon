package crypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
)

var (
	key      = bytes.Repeat([]byte{1}, KeySize)
	otherKey = bytes.Repeat([]byte{2}, KeySize)
)

func mustNew(t *testing.T, k []byte) *Cipher {
	t.Helper()
	c, err := New(k)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNewInvalidKey(t *testing.T) {
	for _, n := range []int{0, 16, 24, 31, 33} {
		if _, err := New(make([]byte, n)); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("New(%d-byte key) err = %v, want ErrInvalidKey", n, err)
		}
	}
}

func TestParseKey(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid", base64.StdEncoding.EncodeToString(key), false},
		{"empty", "", true},
		{"not base64", "!!!!", true},
		{"too short", base64.StdEncoding.EncodeToString(key[:16]), true},
		{"url alphabet rejected", base64.RawURLEncoding.EncodeToString(key), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseKey(c.in)
			if c.wantErr {
				if !errors.Is(err, ErrInvalidKey) {
					t.Errorf("err = %v, want ErrInvalidKey", err)
				}
				return
			}
			if err != nil || !bytes.Equal(got, key) {
				t.Errorf("ParseKey = %x, %v; want %x, nil", got, err, key)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	c := mustNew(t, key)
	for _, msg := range []string{"", "a", "你好，世界", strings.Repeat("x", 1<<16)} {
		ct := c.Encrypt([]byte(msg))
		if len(ct) != len(msg)+28 {
			t.Errorf("len(Encrypt(%d bytes)) = %d, want %d", len(msg), len(ct), len(msg)+28)
		}
		pt, err := c.Decrypt(ct)
		if err != nil || string(pt) != msg {
			t.Errorf("Decrypt(Encrypt(%.20q)) = %.20q, %v", msg, pt, err)
		}

		s := c.EncryptString(msg)
		got, err := c.DecryptString(s)
		if err != nil || got != msg {
			t.Errorf("DecryptString(EncryptString(%.20q)) = %.20q, %v", msg, got, err)
		}
	}
}

func TestEncryptIsRandomized(t *testing.T) {
	c := mustNew(t, key)
	a, b := c.EncryptString("same"), c.EncryptString("same")
	if a == b {
		t.Error("two encryptions of the same plaintext are equal: nonce is not random")
	}
}

func TestEncryptDoesNotMutate(t *testing.T) {
	c := mustNew(t, key)
	in := []byte("plaintext")
	_ = c.Encrypt(in)
	if string(in) != "plaintext" {
		t.Errorf("plaintext mutated: %q", in)
	}
}

func TestDecryptFailures(t *testing.T) {
	c := mustNew(t, key)
	ct := c.Encrypt([]byte("secret"))
	flipped := bytes.Clone(ct)
	flipped[len(flipped)-1] ^= 1
	nonceFlipped := bytes.Clone(ct)
	nonceFlipped[0] ^= 1

	cases := map[string][]byte{
		"nil":            nil,
		"too short":      ct[:27],
		"tampered tag":   flipped,
		"tampered nonce": nonceFlipped,
		"truncated":      ct[:len(ct)-1],
		"appended":       append(bytes.Clone(ct), 0),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if pt, err := c.Decrypt(in); !errors.Is(err, ErrDecrypt) || pt != nil {
				t.Errorf("Decrypt = %q, %v; want nil, ErrDecrypt", pt, err)
			}
		})
	}

	t.Run("wrong key", func(t *testing.T) {
		if _, err := mustNew(t, otherKey).Decrypt(ct); !errors.Is(err, ErrDecrypt) {
			t.Errorf("err = %v, want ErrDecrypt", err)
		}
	})
}

func TestDecryptStringFailures(t *testing.T) {
	c := mustNew(t, key)
	valid := c.EncryptString("secret")
	cases := map[string]string{
		"empty":         "",
		"not base64url": "***",
		"std padding":   valid + "==",
		"wrong key":     mustNew(t, otherKey).EncryptString("secret"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := c.DecryptString(in); !errors.Is(err, ErrDecrypt) || got != "" {
				t.Errorf("DecryptString(%q) = %q, %v; want \"\", ErrDecrypt", in, got, err)
			}
		})
	}
}

func TestConcurrentUse(t *testing.T) {
	c := mustNew(t, key)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if got, err := c.DecryptString(c.EncryptString("x")); err != nil || got != "x" {
					t.Errorf("round trip = %q, %v", got, err)
					return
				}
			}
		})
	}
	wg.Wait()
}
