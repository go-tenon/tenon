package main

import (
	"example.com/blog/internal/cli"
	"github.com/go-tenon/tenon/command"
)

func main() {
	command.Execute(cli.New())
}
