# 配置

应用的配置写在 `config` 目录的 YAML 文件中，加载到应用定义的根结构体，再传给需要它的组件。

包：`config`。

配置在部署时决定，修改后重启应用才生效。站点名称、是否开放注册这类由管理员在运行时修改的值用[设置](../features/settings.md)。

<a id="example"></a>
## 示例

```yaml
# config/app.yaml
http:
  addr: ${HTTP_ADDR:-:8080}
  public_url: ${APP_URL:-http://localhost:8080}

database:
  dsn: ${DATABASE_URL}
  max_conns: 20
```

```go
type Config struct {
    HTTP     httpx.Config    `yaml:"http"`
    Database database.Config `yaml:"database"`
}

cfg, err := config.Load[Config]("config/app.yaml")
```

`Load` 依次填充默认值、展开 `${DATABASE_URL}` 这样的环境变量、严格解码、校验。文件中没有写的字段（例如 HTTP 的超时）使用框架的默认值；字段名写错或缺少必填项时返回错误。

<a id="loading-configuration"></a>
## 用 `config.Load` 加载配置

根结构体放在 `internal/bootstrap` 中（新项目还没有 `bootstrap` 时在 `cmd/<程序名>/main.go` 中），每个字段用 `yaml` 标签对应一个配置段。框架组件的配置类型直接放进去：

| 类型 | 配置段 | 字段说明 |
| --- | --- | --- |
| `httpx.Config` | `http` | [HTTP 配置](../http/middleware.md#http-configuration) |
| `logging.Config` | `logs` | [创建日志器](logging.md#creating-a-logger) |
| `database.Config` | `database` | [数据库配置](../database/database.md#configuration) |
| `filesystem.Config` | `filesystem` | [文件存储配置](../libraries/filesystem.md#configuration) |

应用自己的配置同样定义成结构体，作为新的字段加入根结构体，对应 YAML 中的 `storage` 段：

```go
type StorageConfig struct {
    Endpoint string `yaml:"endpoint"`
    Bucket   string `yaml:"bucket"`
}

type Config struct {
    HTTP    httpx.Config  `yaml:"http"`
    Storage StorageConfig `yaml:"storage"`
}
```

- 解码是严格的：文件中出现结构体里没有的字段（包括拼错的段名、缩进错误），或者值的类型不匹配时，`Load` 返回的错误指出文件和行号，例如 `line 2: field adr not found in type httpx.Config`。
- 使用[命令行](commands.md)时不用自己调用 `Load`：命令按 `--config` 加载并校验配置，把结果交给你的函数，见[叠加多个配置文件](#overriding-configuration)。
- `config.Default[Config]()` 返回只填充了默认值、跳过校验的配置，用于离线构造路由，例如 [openapi、routes 命令](commands.md#openapi-command)。

<a id="overriding-configuration"></a>
## 叠加多个配置文件：`--config`、`APP_CONFIG`、`config/local.yaml`

项目通常只有 `config/app.yaml`，各环境不同的值写成[环境变量](#environment-variables)。命令按这个顺序决定加载哪些文件，后面的文件覆盖前面的：

1. `--config`：可以传入多次，也可以用逗号分隔；
2. 没有 `--config` 时，环境变量 `APP_CONFIG`，写法与 `--config` 相同，可以写在 `.env` 中；
3. 两者都没有时，加载 `config/app.yaml`，`config/local.yaml` 存在时叠加在它之后。`tenon dev` 运行应用时同样如此。

```bash
./bin/blog serve --config config/app.yaml,config/local.yaml
./bin/blog serve   # config/local.yaml 存在时与上一行等价
```

- `config/local.yaml` 是本机的覆盖，只写和 `app.yaml` 不同的部分，例如 `logs.level: debug`。把它加入 `.gitignore`，部署的机器上只加载 `config/app.yaml`。
- 在代码中按顺序传入：`config.Load[Config]("config/app.yaml", "config/local.yaml")`。
- 后面的文件没有写的字段保留前面的值；map 按键合并；slice 整体替换，所以要在后面的文件中写出完整的列表。
- 每个文件单独检查未知字段；`Validate` 在全部文件合并之后执行一次，基础文件可以把必填的值留给后面的文件补齐。

<a id="environment-variables"></a>
## 在配置文件中引用环境变量

密码、密钥和各环境不同的值放在环境变量中，开发时由 [.env](#env-files) 提供，部署时由部署平台提供：

| 写法 | 行为 |
| --- | --- |
| `${NAME}` | 变量必须已设置，空字符串也算已设置 |
| `${NAME:-fallback}` | 变量未设置或为空时使用 `fallback` |
| `${NAME:-}` | 允许变量未设置，此时为空字符串 |
| `$${NAME}` | 转义，得到字面量 `${NAME}` |

- 变量在 YAML 解析之后展开，变量的内容只成为字段的值，不改变文件的结构。
- 没有设置的变量报错 `environment variable NAME is not set`，指出文件和行号。允许不设置时改为 `${NAME:-默认值}`。
- `${NAME}` 没有设置时这一项先跳过，由后面叠加的文件补齐。所以 `app.yaml` 中写 `key: ${APP_KEY}`，部署时由环境变量提供，本机把密钥直接写在 `config/local.yaml` 的同一项中。

<a id="env-files"></a>
### .env 文件

本机开发时把环境变量写在项目根目录的 `.env` 中：

```ini
# .env
LOG_LEVEL=debug
DATABASE_URL=postgres://app:secret@localhost:5432/app
```

- 命令在加载配置之前读取**工作目录**下的 `.env`，所以在项目根目录执行命令。`.env` 不提交。
- 进程中已经设置的变量（包括空字符串）优先于 `.env`。
- 文件可以不存在；语法有错时命令以 `ExitConfig` 退出。

不使用命令行时，先用 `config.LoadEnv` 读取 `.env`，再调用 `config.Load`：

```go
if err := config.LoadEnv(".env"); err != nil && !errors.Is(err, fs.ErrNotExist) {
    return err
}
cfg, err := config.Load[Config]("config/app.yaml")
```

> [!WARNING]
> `.env` 由 [godotenv](https://github.com/joho/godotenv) 解析。值中有 `$`（例如密码）时用单引号包起来，否则 `$` 后面的部分被当成变量引用：`DB_PASSWORD='s3cr$ET'`。

<a id="defaults-and-validation"></a>
## 默认值与校验：`SetDefaults`、`Validate`

配置类型实现 `SetDefaults()` 提供默认值，`Load` 在解码之前调用它；实现 `Validate() error` 校验，`Load` 在全部文件解码之后调用它。`StorageConfig` 是应用自己的配置段：

```go
func (c *StorageConfig) SetDefaults() {
    c.Bucket = "images"
}

func (c StorageConfig) Validate() error {
    if c.Endpoint == "" {
        return errors.New("endpoint is required")
    }
    return nil
}
```

```text
config: invalid configuration: storage: endpoint is required
```

- 文件中写了的字段覆盖默认值，包括显式写出的 `false`、`0` 和 `""`。
- `SetDefaults` 先调用嵌套字段的，再调用外层的。调整框架的默认值时在根结构体上实现它，例如 `c.HTTP.Addr = ":3000"`。
- `Validate` 子字段先于父结构体，所有错误汇总返回。跨段的检查写在根结构体的 `Validate` 中。
- 框架的配置类型都实现了这两个方法。

<a id="inline-fields"></a>
### 在框架的配置段中追加字段

用 `yaml:",inline"` 内嵌框架的配置类型，自己的字段与框架的字段写在同一段中：

```go
type HTTPConfig struct {
    httpx.Config `yaml:",inline"`
    PublicHost   string `yaml:"public_host"`
}
```

内嵌类型的 `SetDefaults` 和 `Validate` 只调用一次；外层结构体定义了同名的方法时只调用外层的。

<a id="inspecting-configuration"></a>
## 检查配置：`config check`、`config show`

在[命令行](commands.md#declaring-the-cli)中装配 `command.Config[Config]()` 之后，`config show` 输出默认值、多个文件和环境变量合并之后最终生效的配置，`config check` 只加载并校验，发布前用它发现缺失的环境变量或写错的字段，见 [config 命令](commands.md#config-command)。

```bash
./bin/blog config check
./bin/blog config show --format json
```
