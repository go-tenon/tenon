package ssr

import (
	"os"
	"sync"
)

// Runtime 是几个前端共用的 Bun 运行时：tenon-start runtime 编译出的可执行文件，只含 Bun 和一个加载器，
// 通常用 go:embed 嵌入一次。每个 Host 用它启动自己的 SSR 进程，前端的代码（Options.Bundle）经文件描述符 3
// 交给加载器，所以一个程序托管几个前端，也只带一份 Bun。
//
// 第一个 SSR 进程启动时把它写进内存文件（memfd），之后的进程共用；它在程序的整个生命周期内保留。
type Runtime struct {
	executable []byte

	once sync.Once
	file *os.File // 一直被引用：*os.File 被回收时终结器会关闭 fd
	path string
	err  error
}

// NewRuntime 返回共用 executable 的 Runtime。开发模式不使用它，executable 可以是 tenon dev 写入的空占位文件。
func NewRuntime(executable []byte) *Runtime { return &Runtime{executable: executable} }

// BundleFD 是 SSR 进程中前端代码所在的文件描述符，加载器从这里读取 Options.Bundle。
const BundleFD = 3
