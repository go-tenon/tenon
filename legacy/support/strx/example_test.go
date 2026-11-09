package strx_test

import (
	"fmt"

	"github.com/ryan053857/go-kit/support/strx"
)

func ExampleMask() {
	fmt.Println(strx.Mask("13812345678", 3, 4))
	fmt.Println(strx.Mask("110101199003071234", 6, 4))
	fmt.Println(strx.Mask("张三丰", 1, 0))
	// Output:
	// 138****5678
	// 110101********1234
	// 张**
}

func ExampleMaskEmail() {
	fmt.Println(strx.MaskEmail("alice@example.com"))
	// Output: a****@example.com
}
