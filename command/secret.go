package command

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/go-tenon/tenon/errorx"
)

// ErrSecretMismatch 表示 ReadSecret 要求输入两次时，两次输入的值不同。
var ErrSecretMismatch = errorx.Define(errorx.InvalidArgument, "command.secret_mismatch")

// ReadSecret 读取一个不应出现在命令行参数和 shell 历史中的值，例如新账号的密码。标准输入是终端时关闭回显，
// 在标准错误上显示 prompt 后读取一行；confirm 不为空时再显示 confirm 读取一次，两次不同时返回 ErrSecretMismatch。
// 标准输入不是终端时（脚本、管道、docker compose run -T）不显示提示，读取第一行，不要求确认。
// 返回的值去掉了行尾的换行符。
func ReadSecret(cmd *cobra.Command, prompt, confirm string) (string, error) {
	in, out := cmd.InOrStdin(), cmd.ErrOrStderr()
	f, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.Wrap(err, "command: read secret")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	read := func(label string) (string, error) {
		if _, err := fmt.Fprint(out, label); err != nil {
			return "", err
		}
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(out) // 关闭回显时用户按下的回车不显示，补一个换行
		return string(b), errors.Wrap(err, "command: read secret")
	}
	first, err := read(prompt)
	if err != nil || confirm == "" {
		return first, err
	}
	second, err := read(confirm)
	if err != nil {
		return "", err
	}
	if first != second {
		return "", ErrSecretMismatch.New()
	}
	return first, nil
}
