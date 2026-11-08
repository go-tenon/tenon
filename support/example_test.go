package support_test

import (
	"errors"
	"fmt"

	"github.com/shellphy-kit/tools/support"
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
