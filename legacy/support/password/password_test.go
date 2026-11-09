package password

import (
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
)

// fast keeps the tests quick; DefaultParams is exercised separately.
var fast = Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestHashFormat(t *testing.T) {
	h := Hash("secret")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("Hash = %q, want argon2id PHC string with DefaultParams", h)
	}
	if Hash("secret") == h {
		t.Error("two hashes of the same password are equal: salt is not random")
	}
}

func TestVerify(t *testing.T) {
	h := fast.Hash("correct horse 电池")
	cases := []struct {
		name     string
		password string
		want     bool
	}{
		{"match", "correct horse 电池", true},
		{"mismatch", "correct horse", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Verify(c.password, h)
			if err != nil || got != c.want {
				t.Errorf("Verify = %v, %v; want %v, nil", got, err, c.want)
			}
		})
	}
}

func TestVerifyEmptyPassword(t *testing.T) {
	ok, err := Verify("", fast.Hash(""))
	if !ok || err != nil {
		t.Errorf("Verify(\"\", Hash(\"\")) = %v, %v; want true, nil", ok, err)
	}
}

// TestVerifyReferenceHash checks interoperability with the reference libargon2,
// using a hash produced by PHP:
//
//	password_hash("correct horse 电池", PASSWORD_ARGON2ID,
//		["memory_cost" => 1024, "time_cost" => 2, "threads" => 1])
func TestVerifyReferenceHash(t *testing.T) {
	const h = "$argon2id$v=19$m=1024,t=2,p=1$WWVhQTRReU5FVlprbG5HMw$D54b7yuvGwA73QvnhwSvadPi2Jkd8JnY3CeV/gz5XXg"
	if ok, err := Verify("correct horse 电池", h); !ok || err != nil {
		t.Errorf("Verify(reference hash) = %v, %v; want true, nil", ok, err)
	}
	if ok, _ := Verify("correct horse", h); ok {
		t.Error("Verify(wrong password, reference hash) = true")
	}
}

func TestVerifyInvalid(t *testing.T) {
	valid := fast.Hash("x")
	parts := strings.Split(valid, "$")
	cases := map[string]string{
		"empty":           "",
		"bcrypt":          "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy",
		"argon2i":         strings.Replace(valid, "argon2id", "argon2i", 1),
		"wrong version":   strings.Replace(valid, "v=19", "v=16", 1),
		"bad params":      strings.Replace(valid, parts[3], "m=x,t=1,p=1", 1),
		"zero iterations": strings.Replace(valid, parts[3], "m=64,t=0,p=1", 1),
		"zero threads":    strings.Replace(valid, parts[3], "m=64,t=1,p=0", 1),
		"bad salt":        strings.Replace(valid, parts[4], "!!!", 1),
		"empty salt":      strings.Replace(valid, "$"+parts[4]+"$", "$$", 1),
		"bad key":         strings.Replace(valid, parts[5], "!!!", 1),
		"empty key":       strings.TrimSuffix(valid, parts[5]),
		"extra field":     valid + "$x",
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := Verify("x", h)
			if ok || !errors.Is(err, ErrInvalidHash) {
				t.Errorf("Verify(%q) = %v, %v; want false, ErrInvalidHash", h, ok, err)
			}
		})
	}
}

func TestNeedsRehash(t *testing.T) {
	stronger := fast
	stronger.Iterations = 2
	longerKey := fast
	longerKey.KeyLength = 64
	h := fast.Hash("x")
	cases := []struct {
		name string
		p    Params
		hash string
		want bool
	}{
		{"same params", fast, h, false},
		{"more iterations", stronger, h, true},
		{"longer key", longerKey, h, true},
		{"invalid hash", fast, "garbage", true},
	}
	for _, c := range cases {
		if got := c.p.NeedsRehash(c.hash); got != c.want {
			t.Errorf("%s: NeedsRehash = %v, want %v", c.name, got, c.want)
		}
	}
	if NeedsRehash(Hash("x")) {
		t.Error("NeedsRehash(Hash(x)) = true, want false")
	}
}

func TestHashZeroParams(t *testing.T) {
	h := Params{Memory: 64, SaltLength: 16, KeyLength: 32}.Hash("x") // must not panic
	if ok, err := Verify("x", h); !ok || err != nil {
		t.Errorf("Verify after zero Iterations/Parallelism = %v, %v; want true, nil", ok, err)
	}
}
