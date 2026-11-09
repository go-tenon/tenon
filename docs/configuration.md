# 配置

[文档首页](README.md) / 入门

配置由应用的 Go 结构体和一个 YAML 文件组成。先创建默认值，再将文件内容解码到结构体，最后校验；业务服务接收已经校验好的具体值。

本章内容：

- [读取配置](#reading)
- [环境变量](#environment)
- [默认值与覆盖](#defaults)
- [按命令加载](#command-scope)
- [保护敏感值](#secrets)
- [校验与输出配置](#inspect)

<a id="reading"></a>

## 读取配置

下面是一个完整的配置读取函数。它只声明并读取 `http` 段：

```go
package settings

import (
    "os"

    "github.com/cockroachdb/errors"
    "github.com/ryan053857/mygoravel/config"
)

type HTTPConfig struct {
    Addr string `yaml:"addr"`
}

func (c HTTPConfig) Validate() error {
    if c.Addr == "" {
        return errors.New("http.addr is required")
    }
    return nil
}

func LoadHTTP(path string) (HTTPConfig, error) {
    cfg := HTTPConfig{Addr: ":8080"}
    doc, err := config.ReadFile(path,
        config.WithSections("http"),
        config.WithLookup(os.LookupEnv),
    )
    if err != nil {
        return cfg, err
    }
    err = doc.Decode("http", &cfg)
    return cfg, err
}
```

对应的 YAML：

```yaml
http:
  addr: ${HTTP_ADDR:-:8080}
```

`ReadFile` 解析文件，`Decode` 展开所选段的环境引用并填充结构体。目标实现 `Validate() error` 时，解码成功后自动调用它。已有配置字节时，可以用 `config.Parse` 替代 `ReadFile`。

<a id="environment"></a>

## 环境变量

| 写法 | 行为 |
| --- | --- |
| `${NAME}` | 变量必须已设置；已设置为空字符串也算设置 |
| `${NAME:-fallback}` | 未设置或值为空时使用 fallback |
| `${NAME:-}` | 允许未设置，并使用空字符串 |
| `$${NAME}` | 输出字面量 ${NAME}，不读取这个变量 |

环境引用只在 YAML 标量值中展开。变量内容不能插入新的 YAML 字段。`config` 不自动加载 `.env` 文件，也不扫描环境变量来覆盖同名配置；通过 shell 或部署平台设置变量，并显式传入 `WithLookup`。

测试可以注入 map 查找函数，不必修改进程环境：

```go
lookup := func(name string) (string, bool) {
    value, ok := map[string]string{"HTTP_ADDR": ":9000"}[name]
    return value, ok
}
```

把 `lookup` 传给 `config.WithLookup` 即可。

<a id="defaults"></a>

## 默认值与覆盖

缺失的字段保留 Go 结构体中的默认值；显式写入的 `false`、`0`、`""` 和 `[]` 会参与覆盖。是否允许这些值，由配置类型的 `Validate` 决定。

map 按键合并，slice 整体替换。因此，使用 `{}` 不会清空默认 map 中的已有键。不要把默认凭据放进会被合并的 map。凭据集合可以使用没有默认值的 slice，让配置文件完整定义当前允许的凭据。

显式 `null` 会被拒绝。未知顶层段、重复键、未知字段、类型错误和多文档输入也会报错。配置加载后应当作为不可变数据使用。

<a id="command-scope"></a>

## 按命令加载

先声明文件中允许的全部顶层段，再解码当前命令需要的部分：

```go
doc, err := config.ReadFile(path,
    config.WithSections("http", "database", "auth", "cache"),
    config.WithLookup(os.LookupEnv),
)
if err != nil {
    return err
}
cfg := database.DefaultConfig()
if err := doc.Decode("database", &cfg); err != nil {
    return err
}
```

这段代码用于数据库命令；`path` 是配置文件路径，`database` 来自框架的同名包。它不会展开 `auth` 和 `cache` 段中的变量。整个文件的结构仍会被检查，例如未知顶层段和显式 null 仍会报错。

完整配置检查应解码所有应用段。可以用 `errors.Join` 聚合各段返回的错误，向调用方一次报告各个配置段的问题。

<a id="secrets"></a>

## 保护敏感值

将密码、token、DSN 等字段声明为 `config.Secret`：

```go
type Credentials struct {
    Token config.Secret `yaml:"token" json:"token"`
}
```

非空 Secret 在 fmt、slog、JSON 和 YAML 输出中显示为 `******`；空值仍显示为空。只有在调用客户端或校验凭据时，使用 `Token.Reveal()` 取得原值。Secret 提供输出脱敏，不提供加密存储。

配置解码错误保留字段路径和位置，不回显错误值。应用自己编写的 `Validate` 也应只描述字段与约束，避免把凭据拼入错误消息。

<a id="inspect"></a>

## 校验与输出配置

配置输出是应用命令的职责。校验完成后，可以直接把配置交给 JSON 或 YAML 编码器；Secret 字段会通过其序列化方法脱敏。

例如，在应用的 Cobra 回调中输出 `cfg`：

```go
return json.NewEncoder(cmd.OutOrStdout()).Encode(cfg)
```

这里使用标准库 `encoding/json`。`cmd` 是当前命令，`cfg` 是应用自己的完整配置结构。若允许用户选择格式，应由回调读取并验证对应标志。

离线检查只做解码和结构校验。数据库 Ping 等网络探测应放进独立命令，使用有 deadline 的 context。命令组合方式见[应用命令](commands.md)。

---

[上一篇：安装与快速开始](installation.md) · [下一篇：目录结构与依赖](directory-structure.md)
