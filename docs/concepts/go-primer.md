# Go 知识准备

这一页讲文档中经常出现、但 Go 入门教程不一定讲到的几种写法：`context`、错误的包装与判断、结构体标签、把方法当作值传递、泛型、`embed`、指针表示“没有提供”，以及用 `defer` 和 `context` 结束后台工作。

你不需要一次读完。后面的页面第一次用到其中一种写法时，会链接到这里对应的小节，到时候再来看即可。如果你已经熟悉这些写法，可以跳过这一页。

- [context：一次请求的上下文](#context)
- [错误：包装与判断](#errors)
- [结构体标签](#struct-tags)
- [把方法当作值传递](#method-values)
- [泛型](#generics)
- [embed：把文件编译进程序](#embed)
- [指针表示“没有提供”](#optional-fields)
- [defer 与结束后台工作](#cleanup)

<a id="context"></a>
## context：一次请求的上下文

`context.Context`（下文简称 ctx）是 Go 标准库中的一个接口，它跟着一次请求从头传到尾，携带两样东西：

- **取消信号**：客户端断开连接、请求超时时，ctx 被取消。正在执行的数据库查询、HTTP 调用看到 ctx 已经取消，会提前返回，不再做无用功。
- **请求范围内的值**：例如“这次请求是谁发起的”。框架在认证之后把当前用户放进 ctx，你的代码再从 ctx 中取出来。

所以你会看到，几乎每个函数的第一个参数都是 `ctx context.Context`，并且会把它继续传给下一层：

```go
func (h *Handlers) Get(ctx context.Context, in *GetInput) (*ArticleOutput, error) {
    // 从 ctx 中取出当前登录的用户
    sub, _ := identity.FromContext(ctx)

    // 把 ctx 传给数据库查询：请求被取消时，查询也随之结束
    a, err := article.Find(ctx, h.db, in.ID, sub)
    // ……
}
```

下面这个完整的程序演示取消信号：`work` 本来要运行 1 秒，但 ctx 在 100 毫秒后超时，它提前返回：

```go
package main

import (
	"context"
	"fmt"
	"time"
)

// work 模拟一次耗时的操作，ctx 被取消时提前返回。
func work(ctx context.Context) error {
	select {
	case <-time.After(time.Second):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	fmt.Println(work(ctx)) // context deadline exceeded
}
```

使用 ctx 的三条规则：

- **作为第一个参数传递**，名字叫 `ctx`。这样一眼就能看出这个函数会受取消信号影响。
- **不要把 ctx 保存在结构体中。** ctx 只属于一次请求，保存起来以后，下一次请求会用到上一次请求的 ctx，已经取消的信号和别人的用户信息都会混进来。
- **只放请求范围内的信息**，例如当前用户、请求 ID。数据库连接这类长期使用的依赖应当作为结构体的字段，见[依赖装配](dependencies.md)。

<a id="errors"></a>
## 错误：包装与判断

Go 的函数通过返回 `error` 报告失败。一个错误从数据库一路返回到 HTTP 处理器，中间经过好几层函数。每一层都可以给它**包装**一句说明，最后的错误就像一串线索，告诉你在哪一步、因为什么失败：

```go
if err := tx.NewInsert().Model(a).Exec(ctx); err != nil {
    return errors.Wrap(err, "insert article")
}
```

最后记录到日志中的错误信息类似 `publish article: insert article: duplicate key value ...`，从左往右读就是调用的顺序。标准库的写法是 `fmt.Errorf("insert article: %w", err)`；Tenon 使用 [cockroachdb/errors](https://github.com/cockroachdb/errors) 的 `errors.Wrap`，效果相同，并且会记录出错的位置（文件和行号）。

包装之后，原来的错误仍然在里面。要判断“是不是某一种错误”时，用 `errors.Is`，它会沿着包装一层层往里找：

```go
a, err := article.Find(ctx, db, id)
if errors.Is(err, article.ErrNotFound) {
    // 文章不存在：返回 404
}
```

不要用 `err == article.ErrNotFound` 比较，也不要比较错误信息的字符串：错误一旦被包装过，这两种比较都会失败。

Tenon 中的业务错误用 `errorx.Define` 定义，它同时决定了返回给客户端的 HTTP 状态码和错误码，见[错误处理](../general/errors.md)。

<a id="struct-tags"></a>
## 结构体标签

结构体字段后面反引号中的内容叫**标签**（tag）。标签本身不影响程序的运行，但其他代码可以读取它，决定怎样处理这个字段：

```go
type CreateInput struct {
    Body struct {
        Title string   `json:"title" minLength:"1" maxLength:"200"`
        Tags  []string `json:"tags,omitempty" required:"false"`
    }
}
```

- `json:"title"`：标准库的 `encoding/json` 读取它，JSON 中的字段名是 `title`。`omitempty` 表示值为空时不输出这个字段。
- `minLength:"1"`、`maxLength:"200"`、`required:"false"`：处理 HTTP 请求的 [Huma](https://huma.rocks) 读取它们，作为校验规则。请求中的标题为空或超过 200 个字符时，请求在进入你的代码之前就被拒绝，见[验证](../http/validation.md)。

一个字段可以有多个标签，用空格分开；标签中的引号和反引号不能写错，否则这个标签会被悄悄忽略。`go vet` 能检查出常见的格式错误。

<a id="method-values"></a>
## 把方法当作值传递

在 Go 中，函数和方法都可以当作值传递。`h.Create` 不加括号时表示“`h` 的 `Create` 方法”这个值，可以交给别的函数，由它在需要的时候调用：

```go
h := &Handlers{db: db}

// 注册接口：收到 POST /articles 时，框架调用 h.Create
rest.Register(api, createOp, h.Create)
```

`h.Create` 记住了 `h`，被调用时能用到 `h.db`。所以处理器通常写成一个结构体的方法，结构体中保存它需要的依赖，见[依赖装配](dependencies.md)。

同样，参数的类型也可以是一个函数。例如下面的 `require` 是“修改一个接口定义”的函数，调用方传入不同的函数，就能给接口加上不同的登录要求：

```go
func Register(api *rest.API, require func(*huma.Operation), db *bun.DB) {
    op := huma.Operation{OperationID: "article-create", Method: http.MethodPost, Path: "/articles"}
    require(&op) // 由调用方决定这个接口需要怎样的登录
    rest.Register(api, op, NewCreate(db).HTTP)
}
```

<a id="generics"></a>
## 泛型

泛型让一个类型或函数适用于多种元素类型。方括号中的是**类型参数**：

```go
// page.Offset[T] 是“一页数据”：T 可以是任何类型
type Offset[T any] struct {
    Items []T   `json:"items"`
    Total int64 `json:"total"`
}
```

使用时在方括号中写出具体的类型，例如 `page.Offset[ArticleDTO]` 是“一页文章”，`Items` 的类型就是 `[]ArticleDTO`。`any` 表示任何类型都可以。

读到 `func New[T any](client *memcache.Client) *Cache[T]` 这样的签名时，可以这样理解：`New` 创建一个缓存，缓存中保存的值的类型由你决定，例如 `cachex.New[Profile](client)` 创建保存 `Profile` 的缓存，取出的值直接就是 `Profile`，不需要再做类型转换。

<a id="embed"></a>
## embed：把文件编译进程序

`//go:embed` 是一条写在变量上方的注释指令，它让 Go 在编译时把文件读进程序中：

```go
package migrations

import "embed"

// FS 是这个目录中的 SQL 迁移文件，编译时打包进程序。
//
//go:embed *.sql
var FS embed.FS
```

这样部署时只需要一个可执行文件：迁移文件、前端的构建产物、翻译文件都在其中，不需要另外复制目录。注意 `//go:` 与 `embed` 之间没有空格，变量的类型是 `embed.FS`，并且要导入 `embed` 包，缺少任何一点都会编译失败或者读不到文件。

<a id="optional-fields"></a>
## 指针表示“没有提供”

修改一篇待办事项时，客户端可能只想改标题，不提供“是否完成”。如果字段是 `bool`，“没有提供”和“提供了 `false`”都是 `false`，代码无法区分。改成指针 `*bool` 就能区分：

```go
type UpdateInput struct {
    Body struct {
        Title string `json:"title,omitempty"`
        Done  *bool  `json:"done,omitempty"` // nil 表示没有提供
    }
}

if in.Body.Done != nil {
    todo.Done = *in.Body.Done // 提供了才修改
}
```

`nil` 表示请求中没有这个字段；不是 `nil` 时，用 `*` 取出它的值。

<a id="cleanup"></a>
## defer 与结束后台工作

`defer` 让一个调用在函数返回时执行，常用来释放资源，例如关闭文件、结束事务。不管函数从哪里返回、是否出错，它都会执行：

```go
rows, err := db.QueryContext(ctx, "SELECT id FROM articles")
if err != nil {
    return err
}
defer rows.Close() // 函数返回时关闭，不会忘记
```

服务端程序还有一类“后台工作”：HTTP 服务、队列的 worker、定时任务，它们一直运行，直到程序收到停止信号（例如部署新版本时的 `SIGTERM`）。Go 的惯用法是把“停止”也表示为一个 ctx 的取消：

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// 收到 Ctrl+C（SIGINT）或 SIGTERM 时，ctx 被取消
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("运行中，按 Ctrl+C 停止")
	<-ctx.Done() // 等到收到停止信号
	fmt.Println("正在停止：等进行中的请求处理完，再关闭数据库连接")
}
```

Tenon 的 `serve` 等命令就是这样运行的：收到停止信号后，先停止接收新请求，等进行中的请求处理完，再关闭数据库等资源，见[命令行](../general/commands.md)。
