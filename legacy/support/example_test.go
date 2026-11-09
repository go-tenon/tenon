package support_test

import (
	"fmt"

	"github.com/cockroachdb/errors"

	"github.com/ryan053857/go-kit/support"
)

func ExampleCatch() {
	err := support.Catch(func() error {
		var items []string
		_ = items[3] // panics: index out of range
		return nil
	})
	var pe *support.PanicError
	fmt.Println(errors.As(err, &pe), pe.Value)
	// Output: true runtime error: index out of range [3] with length 0
}
