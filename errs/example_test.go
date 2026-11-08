package errs_test

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/shellphy-kit/tools/errs"
)

func findOrder(id int) error {
	err := sql.ErrNoRows // stands in for a repository call
	if errors.Is(err, sql.ErrNoRows) {
		return errs.Wrap(err, errs.NotFound, "订单不存在")
	}
	return fmt.Errorf("find order %d: %w", id, err)
}

func validateSignup(email, name string) error {
	fe := errs.FieldErrors{}
	if email == "taken@example.com" {
		fe["email"] = "邮箱已被使用"
	}
	if name == "" {
		fe["name"] = "不能为空"
	}
	return fe.Err()
}

func Example() {
	err := fmt.Errorf("handle request: %w", findOrder(42))
	fmt.Println(errs.CodeOf(err))    // for the transport layer
	fmt.Println(errs.MessageOf(err)) // safe to show to users
	fmt.Println(err)                 // full chain, for logs

	err = validateSignup("taken@example.com", "")
	fmt.Println(errs.CodeOf(err))
	if fe, ok := errors.AsType[errs.FieldErrors](err); ok {
		fmt.Println(fe["email"], fe["name"])
	}

	// Output:
	// not_found
	// 订单不存在
	// handle request: 订单不存在: sql: no rows in result set
	// invalid_argument
	// 邮箱已被使用 不能为空
}
