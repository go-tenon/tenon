package password_test

import (
	"fmt"
	"strings"

	"github.com/shellphy-kit/tools/support/password"
)

func ExampleHash() {
	hash := password.Hash("correct horse battery staple")
	fmt.Println(strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$"))
	// Output: true
}

func ExampleVerify() {
	stored := password.Hash("correct horse battery staple")

	ok, err := password.Verify("correct horse battery staple", stored)
	fmt.Println(ok, err)
	ok, err = password.Verify("wrong", stored)
	fmt.Println(ok, err)
	// Output:
	// true <nil>
	// false <nil>
}

func ExampleNeedsRehash() {
	// A hash stored before DefaultParams was raised.
	old := password.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}.Hash("pw")

	if ok, _ := password.Verify("pw", old); ok && password.NeedsRehash(old) {
		fmt.Println("rehash and save:", password.NeedsRehash(password.Hash("pw")))
	}
	// Output: rehash and save: false
}
