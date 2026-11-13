package article

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/identity"
)

// system 在文档中与 Job 一起给出。
var system identity.Subject

// Command 处理 article publish <id>，输出发布后的文章 slug。
func (a *Publish) Command(cmd *cobra.Command, args []string) error {
	id, err := uuid.Parse(args[0])
	if err != nil {
		return command.AsExitError(command.ExitUsage, err)
	}
	item, err := a.Handle(cmd.Context(), system, id)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), item.Slug)
	return err
}
