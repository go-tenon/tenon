# 配置

Tenon 应用的配置保存在 `config` 目录的 YAML 文件中，加载到你定义的一个 Go 结构体里。加载完成后，你拿到的是一个类型明确、已经校验过的普通结构体，把它传给需要的组件即可。

配置在部署时决定，修改后需要重启应用。站点名称、是否开放注册这类由管理员在运行时修改的值不属于配置，请使用[设置](../features/settings.md)。

- [示例用法](#example-usage)
- [加载配置](#loading-configuration)
- [按环境覆盖配置](#overriding-configuration)
- [环境变量](#environment-variables)
    - [.env 文件](#env-files)
- [默认值与校验](#defaults-and-validation)
- [检查配置](#inspecting-configuration)
- [API 参考](#api-reference)

<a id="example-usage"></a>
## 示例用法

一个典型的配置文件是这样的：

```yaml
# config/app.yaml
http:
  addr: ${HTTP_ADDR:-:8080}

database:
  dsn: ${DATABASE_URL}
  max_conns: 20
```

为它定义一个结构体，然后加载：

```go
type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Database database.Config `yaml:"database"`
}

cfg, err := config.Load[Config]("config/app.yaml")
```

就这么简单。`${DATABASE_URL}` 会从环境变量中读取；文件中没有写的字段（例如 HTTP 的超时）使用框架的默认值；如果写错了字段名，或者缺少必填项，加载时就会报错，应用不会带着错误的配置启动。

<a id="loading-configuration"></a>
## 加载配置

首先，为整个配置文件定义一个根结构体，用 `yaml` 标签声明每个字段对应的配置段。框架为常用的组件预定义了配置类型，直接放进根结构体即可：

```go
type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Logs     logging.Config  `yaml:"logs"`
    Database database.Config `yaml:"database"`
}
```

这些类型和它们对应的配置段如下：

| 类型 | 配置段 | 字段说明 |
| --- | --- | --- |
| `httpx.Config` | `http` | [HTTP 配置](../http/middleware.md#http-configuration) |
| `logging.Config` | `logs` | [创建日志器](logging.md#creating-a-logger) |
| `database.Config` | `database` | [数据库配置](../database/quick-start.md#configuration) |
| `filesystem.Config` | `filesystem` | [文件存储配置](../libraries/filesystem.md#configuration) |

然后用 `config.Load` 加载它：

```go
cfg, err := config.Load[Config]("config/app.yaml")
```

> [!TIP]
> 使用[命令行](commands.md)时，你不需要自己调用 `config.Load`：命令会按 `--config` 选项加载配置，默认是 `config/app.yaml`。

应用自己的配置，例如第三方服务的地址，同样定义成结构体，作为新的字段加入 `Config`。假设你要把图片上传到对象存储：

```go
type StorageConfig struct {
    Endpoint string `yaml:"endpoint"`
    Bucket   string `yaml:"bucket"`
}

type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Logs     logging.Config  `yaml:"logs"`
    Database database.Config `yaml:"database"`
    Storage  StorageConfig   `yaml:"storage"`
}
```

配置文件中加上对应的一段：

```yaml
storage:
  endpoint: ${STORAGE_ENDPOINT}
  bucket: images
```

`config.Load` 会依次完成四件事：填充默认值、展开环境变量、把文件内容解码到结构体、调用 `Validate` 校验。后面几节分别介绍它们。

> [!NOTE]
> 解码是严格的。文件中出现结构体里没有的字段（包括拼错的段名），或者值的类型不匹配时，加载会直接报错，并指出是哪个文件的哪一行。

<a id="overriding-configuration"></a>
## 按环境覆盖配置

开发、测试和生产环境的配置大部分是相同的，只有少数几个值不同。所以，把共同的部分写在 `config/app.yaml` 中，每个环境的差异写在单独的文件中：

```yaml
# config/prod.yaml：只写和 app.yaml 不同的部分
logs:
  level: warn
database:
  max_conns: 50
```

加载时依次传入多个文件，**后面的文件覆盖前面的**：

```bash
./bin/api serve --config config/app.yaml --config config/prod.yaml
./bin/api serve --config config/app.yaml,config/prod.yaml   # 与上一行等价
```

在代码中加载时，同样按顺序传入：

```go
cfg, err := config.Load[Config]("config/app.yaml", "config/prod.yaml")
```

后面的文件中没有写的字段，保留前面的值；写了的字段，覆盖前面的值。map 按键合并，slice 则整体替换。

每个文件都会单独检查未知字段，而 `Validate` 只在全部文件合并之后执行一次。所以，基础文件可以把某个必填的值留给环境文件补齐。

文件的数量和名称都由你决定。例如在本地临时调试时，可以再叠加一个不提交到版本库的文件：

```bash
./bin/api serve --config config/app.yaml --config config/local.yaml
```

<a id="environment-variables"></a>
## 环境变量

密码、密钥这类值不应该写在配置文件中。配置文件中的值可以引用进程的环境变量，部署时由部署平台提供：

| 写法 | 行为 |
| --- | --- |
| `${NAME}` | 变量必须已设置，否则报错；设置为空字符串也算已设置 |
| `${NAME:-fallback}` | 变量未设置或为空时，使用 `fallback` |
| `${NAME:-}` | 允许变量未设置，此时使用空字符串 |
| `$${NAME}` | 转义，得到字面量 `${NAME}` |

变量在 YAML 解析之后才展开，所以变量的内容只会成为字段的值，不会改变文件的结构。

<a id="env-files"></a>
### .env 文件

本地开发时，每次启动都手动设置环境变量很麻烦。你可以把它们写在项目根目录的 `.env` 文件中：

```ini
# .env
LOG_LEVEL=debug
DATABASE_URL=postgres://app:secret@localhost:5432/app
```

命令行在加载配置之前，会读取**工作目录**下的 `.env`，把其中的变量写入进程环境，配置文件中的 `${DATABASE_URL}` 就能读到它。所以请在项目根目录执行命令，并把 `.env` 加入 `.gitignore`。

读取 `.env` 时有两条规则：

- **进程环境优先**：进程中已经设置的变量（包括空字符串）不会被 `.env` 覆盖。部署时直接用环境变量覆盖即可。
- **文件可以不存在**：生产环境通常没有 `.env`，变量全部由部署平台提供。但如果文件存在而语法有错，命令会以 `ExitConfig` 退出。

不使用命令行时，先用 `config.LoadEnv` 读取 `.env`，再调用 `config.Load`：

```go
if err := config.LoadEnv(".env"); err != nil && !errors.Is(err, fs.ErrNotExist) {
    return err
}
cfg, err := config.Load[Config]("config/app.yaml")
```

> [!WARNING]
> `.env` 由 [godotenv](https://github.com/joho/godotenv) 解析。如果值中有 `$`（例如密码），请用单引号把它包起来，否则 `$` 后面的部分会被当成变量引用：`DB_PASSWORD='s3cr$ET'`。

<a id="defaults-and-validation"></a>
## 默认值与校验

配置类型实现 `SetDefaults()` 方法，就可以提供默认值。`Load` 会在解码之前调用它：

```go
func (c *StorageConfig) SetDefaults() {
    c.Bucket = "images"
}
```

文件中没有写的字段保持默认值；写了的字段覆盖默认值，包括显式写出的 `false`、`0` 和 `""`。

`SetDefaults` 是从内到外调用的：先调用嵌套字段的，再调用外层结构体的。因此，如果你想调整框架的默认值，在根结构体上实现 `SetDefaults` 即可：

```go
func (c *Config) SetDefaults() {
    c.HTTP.Addr = ":3000"
}
```

同样，任何配置类型实现了 `Validate() error` 方法，`Load` 都会在解码之后调用它。根结构体和所有嵌套的字段都会被检查，子字段先于父结构体，所有的错误一次性汇总返回：

```go
func (c StorageConfig) Validate() error {
    if c.Endpoint == "" {
        return errors.New("endpoint is required")
    }
    return nil
}
```

现在，如果部署时忘了设置 `STORAGE_ENDPOINT`，`${STORAGE_ENDPOINT}` 会直接报错；如果把它设置成了空字符串，`Validate` 会拒绝它：

```text
config: invalid configuration: storage: endpoint is required
```

框架预定义的配置类型都已经实现了 `SetDefaults` 和 `Validate`，放进根结构体后，会自动填充默认值并校验。需要跨段检查时（例如两个段的值必须一致），在根结构体上实现 `Validate`。

有时你想在框架配置的同一段下追加自己的字段，这时可以用 `yaml:",inline"` 内嵌它：

```go
type HTTPConfig struct {
    httpx.Config `yaml:",inline"`
    PublicHost   string `yaml:"public_host"` // 与 http 段的框架字段写在一起
}
```

内嵌类型的 `SetDefaults` 和 `Validate` 仍然只调用一次。如果外层结构体自己也定义了同名的方法，则只调用外层的，这与 Go 的方法覆盖一致。

<a id="inspecting-configuration"></a>
## 检查配置

多个文件、默认值和环境变量叠加之后，最终生效的配置是什么样的？在[命令行](commands.md#declaring-the-cli)中装配 `command.Config` 之后，应用就有了两个检查配置的命令：

```bash
./bin/api config check --config config/app.yaml,config/prod.yaml   # 只加载并校验，不访问网络
./bin/api config show --config config/app.yaml,config/prod.yaml    # 输出合并后的配置
```

`config show` 的输出包括默认值、多个文件合并的结果和展开后的环境变量，`--format json` 输出 JSON。发布之前运行 `config check`，可以尽早发现缺失的环境变量或写错的字段。配置文件缺失或校验失败时，这两个命令都以 `ExitConfig` 退出，见 [config 命令](commands.md#config-command)。

<a id="api-reference"></a>
## API 参考

包 `github.com/go-tenon/tenon/config`。

### Load

```go
func Load[T any](files ...string) (T, error)
```

- **参数**
    - `files`：要加载的配置文件，后面的覆盖前面的
- **返回**：加载并校验后的配置；文件缺失、格式错误或校验失败时返回错误

先对各字段调用 `SetDefaults`，再逐个文件展开环境变量并严格解码，最后调用 `Validate`。

### Default

```go
func Default[T any]() T
```

- **返回**：只填充了默认值的 `T`，与读取一个空文件得到的值相同，但不做校验

用于不读取配置文件、又需要一份可用配置的场合，例如 [openapi、routes 命令](commands.md#openapi-command)构造应用的路由。

### LoadEnv

```go
func LoadEnv(path string) error
```

- **参数**
    - `path`：dotenv 文件的路径
- **返回**：文件不存在时，返回的错误满足 `errors.Is(err, fs.ErrNotExist)`

把文件中的变量写入进程环境，已经设置的变量不会被覆盖。

### Defaulter

```go
type Defaulter interface{ SetDefaults() }
```

配置类型实现它来提供默认值，从内到外调用。

### Validator

```go
type Validator interface{ Validate() error }
```

配置类型实现它来校验，子字段先于父结构体，所有错误汇总返回。

### command.Config

```go
func Config[C any]() *cobra.Command
```

- **返回**：`config` 命令，包含 `check` 和 `show` 两个子命令

包 `github.com/go-tenon/tenon/command`。`C` 是应用的根配置结构体。
