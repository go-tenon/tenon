package random_test

import (
	"fmt"

	"github.com/shellphy-kit/tools/support/random"
)

func ExampleToken() {
	token := random.Token(32)       // send to the client, e.g. in a password-reset link
	hash := random.HashToken(token) // store only this

	fmt.Println(len(token), random.VerifyToken(token, hash))
	// Output: 43 true
}

func ExampleString() {
	code := random.String(6, random.Digits) // SMS verification code
	fmt.Println(len(code))
	// Output: 6
}

func ExampleBytes() {
	key := random.Bytes(32) // e.g. an HMAC key
	fmt.Println(len(key))
	// Output: 32
}

func ExampleHashToken() {
	fmt.Println(random.HashToken("abc"))
	// Output: ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
}

func ExampleVerifyToken() {
	hash := random.HashToken("presented-token")
	fmt.Println(random.VerifyToken("presented-token", hash), random.VerifyToken("other", hash))
	// Output: true false
}
