# 应用命令

[文档首页](README.md) / 进阶使用

command 包提供返回原生 Cobra Command 的工厂。应用选择需要的命令，回调负责具体的配置、资源和执行逻辑。

本章内容：

- [命令工厂](#reference)
- [构造离线文档命令](#factories)
- [执行与退出](#exits)
- [参数与输出](#output)
- [开发工具](#development)

<a id="reference"></a>

## 命令工厂

| 工厂 | 创建的命令 | 回调职责 |
| --- | --- | --- |
| Root | 应用根命令 | 设置输入输出与应用信息 |
| NewServe | serve | 打开运行资源并启动 HTTP |
| NewConfig | config check / show | 校验配置、输出脱敏结果 |
| NewDoctor | doctor | 限时探测应用启用的外部依赖 |
| NewMigrate | migrate up / status / down | 连接数据库并调用 goose.Provider |
| NewOpenAPI | openapi | 离线登记接口并输出文档 |

这些工厂不会自动安装到应用。配置文件路径、版本信息和命令需要的资源，都由应用显式连接。

<a id="factories"></a>

## 构造离线文档命令

下面是完整的命令构造函数，它接收应用自己的路由登记函数：

```go
package cli

import (
    "context"
    "io"

    "github.com/spf13/cobra"
    "github.com/ryan053857/mygoravel/command"
    "github.com/ryan053857/mygoravel/httpx/rest"
)

func New(register func(*rest.API), out, diagnostic io.Writer) *cobra.Command {
    root := command.Root("hello", "Hello API", command.IO{
        Out: out, Err: diagnostic,
    })
    root.AddCommand(command.NewOpenAPI(func(ctx context.Context,
        cmd *cobra.Command, args []string,
    ) error {
        api := rest.New(rest.Options{Mode: rest.ModeOpenAPI})
        register(api)
        data, err := api.OpenAPIJSON()
        if err != nil {
            return err
        }
        _, err = cmd.OutOrStdout().Write(append(data, '\n'))
        return err
    }))
    return root
}
```

传入的 register 可以使用[路由章节](routing.md#first-route)的写法。构造命令不会执行回调，ExecuteContext 才进入对应逻辑。

<a id="exits"></a>

## 执行与退出

main 的最外层负责信号、ExecuteContext 和退出码。回调返回错误，可以用 command.AsExitError 分类，再用 command.ExitCode 取得进程退出码：

| 退出码 | 常量 | 含义 |
| --- | --- | --- |
| 0 | ExitOK | 成功 |
| 2 | ExitUsage | 用法错误 |
| 3 | ExitConfig | 配置错误 |
| 4 | ExitUnavailable | 外部依赖不可用 |
| 5 | ExitInternal | 未分类执行错误 |

配置和连接错误不会仅凭文本自动分类。应用需要在拥有处理决定的位置附加正确的退出码。

<a id="output"></a>

## 参数与输出

Root 接收 command.IO，命令可以使用 cmd.OutOrStdout 和 cmd.ErrOrStderr。stdout 留给结果，stderr 留给诊断。

需要配置路径时，用 Cobra 原生 Flags 或 PersistentFlags 定义。NewConfig 的 show 子命令提供 format 标志，但实际格式验证与序列化由 show 回调实现。其他选项同样由应用定义。

避免让文档导出回调读取运行密钥，或让迁移命令加载与数据库无关的配置。按命令选择资源，有助于保持命令独立可测试。

<a id="development"></a>

## 开发工具

应用直接使用 go test，不需要专用测试运行器。热重载或任务工具可以调用普通的 Go 构建、测试和应用启动命令；它们不是框架运行的前提。

---

[上一篇：本地化](localization.md) · [下一篇：测试](testing.md)
