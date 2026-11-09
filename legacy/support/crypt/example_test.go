package crypt_test

import (
	"fmt"

	"github.com/ryan053857/go-kit/support/crypt"
)

// appKey would come from configuration; generate one with: openssl rand -base64 32
const appKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="

func Example() {
	key, err := crypt.ParseKey(appKey)
	if err != nil {
		panic(err)
	}
	c, err := crypt.New(key)
	if err != nil {
		panic(err)
	}

	stored := c.EncryptString("third-party access token")
	plain, err := c.DecryptString(stored)
	fmt.Println(plain, err)
	// Output: third-party access token <nil>
}

func ExampleCipher_DecryptString() {
	key, _ := crypt.ParseKey(appKey)
	c, _ := crypt.New(key)

	stored := c.EncryptString("secret")
	tampered := "A" + stored[1:]
	if stored[0] == 'A' {
		tampered = "B" + stored[1:]
	}
	_, err := c.DecryptString(tampered)
	fmt.Println(err)
	// Output: crypt: message authentication failed
}

func ExampleCipher_Encrypt() {
	key, _ := crypt.ParseKey(appKey)
	c, _ := crypt.New(key)

	ct := c.Encrypt([]byte("hello"))
	pt, _ := c.Decrypt(ct)
	fmt.Println(len(ct), string(pt)) // 12-byte nonce + 5 bytes + 16-byte tag
	// Output: 33 hello
}
