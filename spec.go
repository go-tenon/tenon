//go:build ignore

// 本文件是新框架的设计规格与实施计划，不参与编译。
// 状态：第一期实现与回归验收规格。更新：2026-11-10。
// 框架暂定名：mygoravel。目标 module：github.com/ryan053857/mygoravel。
// 最新范围调整：删除仓库内参考应用，用户文档只提供独立 API 示例。
// 验收以框架契约测试、真实数据库测试和独立 module 消费为准。
// 十个 PR 按当前交付范围同步；可选缓存由应用直接集成并验证。
package spec

/*
一、目标与决策

定位：mygoravel 是约定驱动、显式装配、面向多种应用形态的 Go 应用框架。

框架负责给出经过验证的默认组合，统一配置、错误、迁移、测试、运行和运维体验。
开发者负责业务模型、业务规则及应用装配。依赖关系必须能从普通 Go 代码中读出来。
交付物包括库、应用命令工厂、独立 API 文档、约定与测试工具；不以功能包数量衡量成熟度。

本设计不兼容现有 API，不以保留旧目录或降低首次重构成本为前提。
已有实现只作为需求和场景参考，可整体替换。第一期不实现代码生成、脚手架生成、
自动改写装配代码或源码升级器；应用通过手写装配和独立 API 文档接入。
长期目标像 Laravel 一样为多种应用提供统一基础能力，包括 HTTP API、控制台程序、
服务端 Web 页面、RPC、WebSocket 和后台任务；不照搬 Laravel 的容器或 facade。
第一期只交付常规 HTTP API 应用：配置、错误、日志、路由、校验、鉴权、数据库、
迁移、按需缓存、测试及运行管理形成完整路径。
控制台业务应用、页面与会话、RPC、WebSocket、独立 worker/scheduler 均为后期目标。
第一期的 serve/config/migrate/openapi 等命令是 API 应用的开发运维工具，
不等于交付独立控制台应用框架；不实现 console profile 或业务命令体系。
多形态扩展通过中立核心和独立入口适配实现，不提前建立空适配器、全能 ApplicationType
枚举、跨协议请求上下文或待实现目录。HTTP 能力不能成为所有应用的必需依赖。
默认数据库为 PostgreSQL；默认应用组织为模块化单体。
第一期不建设 DI 容器、插件扫描系统、通用 ORM、通用 Repository、分布式事务、
任意组件互换平台或微服务治理平台。

优雅的判定标准：
  1. 默认路径明确，常见任务没有重复选择，也没有隐含副作用。
  2. 配置有类型，依赖有类型，错误可判断，资源有所有者。
  3. 可以局部使用；没有数据库的 HTTP 应用不需要数据库配置。
  4. 两个应用可以在同一个进程并行构建、测试、关闭，策略互不污染。
  5. 应用能直接使用社区组件的原生能力，框架适配层只承担新增语义。
  6. 文档承诺都有可执行的契约测试；每个发布版本有独立 module 消费验证。

二、总体结构与依赖方向

建议使用一个仓库、一个主要 Go module；独立消费测试在临时外部 module 中运行。
不要把全部库聚合在根包，不引入包含所有依赖的 Framework/Container 类型。

  cleanup/              资源清理栈；不管理应用、依赖解析或服务运行
  config/               类型化配置加载、校验、脱敏输出
  fault/                与传输无关的错误语义
  logging/                 slog 构建、上下文字段、错误记录约定
  i18n/                 可选的 go-i18n 目录构建与校验辅助
  identity/             已认证主体、请求内身份传递
  database/             PostgreSQL 连接、事务辅助、数据库错误识别
  database/migrate/     goose Provider 的默认装配与源码校验辅助
  database/dbtest/      真实数据库隔离与清理
  httpx/                net/http 生命周期与中间件
  httpx/rest/           Huma 适配、接口声明、错误转换、OpenAPI
  command/              API 应用的启动、检查、迁移和文档命令组合
  internal/archtest/     可执行的依赖方向和副作用规则
  internal/consumetest/  通过模块代理消费公开 API 的独立 module 验收

上面是职责划分，不是必须一次创建的目录清单；只在存在实际代码时建包。
业务放在使用者应用的 internal/<业务>；有真实复用需求后再提取公共包及其 HTTP 子包。
框架仓库不交付业务应用。缓存直接使用 eko/gocache，不新增缓存门面。
后期按真实场景加入页面/会话、消息、调度、RPC、WebSocket 和外部 I/O 集成。
这些能力可能只需要官方组件配置和装配示例，不预先承诺每项都创建框架包。

依赖规则：
  - fault、identity 不依赖 HTTP、数据库或运行层。
  - config、logging、i18n 不依赖业务模块和运行层。
  - cleanup 不依赖数据库、HTTP、邮件、存储、消息等具体驱动。
  - database 和后续消息等基础能力不依赖 httpx；关联信息通过中立日志字段传递。
  - 业务核心不依赖 cleanup、command 或 HTTP 端点；业务间的依赖遵守下述显式无环规则。
  - 模块 HTTP 及未来其他入口子包依赖模块核心；核心绝不反向引用子包。
  - 业务包可以依赖稳定的其他业务能力，要求依赖显式、无环，不能用分层规则强迫拆接口。
    只有需要解耦实际实现或隔离测试时才由消费方定义最小接口，不为每次调用创建契约包。
  - command 可以组合基础设施提供的命令工厂，但不固定安装业务模块。
  - 应用 bootstrap 可以连接全部层；业务服务不可接收整个应用或资源集合。
  - 新增框架包装必须说明它新增的策略或语义，单纯改名转发不予引入。

第一期默认组合：net/http + chi、Huma、slog、Bun + pgx、goose、Cobra、go-i18n、
cockroachdb/errors；需要缓存时直接使用 eko/gocache 及其官方 store。
templ、scs、Watermill、gocron 是后期对应场景的候选，不因未来可能使用而加入核心依赖。
具体版本在对应实施 PR 中核实并固定，通过组合测试升级。

组件集成的优先级是：直接使用原生 API → 提供配置/装配示例 → 少量连接代码 →
确有不可替代的框架语义时才定义新类型。不能仅为了统一方法名或隐藏依赖而封装。
可直接暴露 *bun.DB、chi.Router、huma.API、*slog.Logger、gocache 原生类型及选项。
框架不复制 CRUD、路由、缓存 Get/Set/Delete、错误 Wrap/Is/As 或驱动注册体系。
消费方为真实业务需要定义的窄接口可以存在，但不能扩张成覆盖整个上游库的影子 API。
适配代码必须说明新增的约定（例如错误对外映射、资源清理），并保持原生能力可用。
如果上游已完成多后端抽象，如 eko/gocache，就直接使用该抽象，不再建立框架 CacheManager。

错误基础默认采用 github.com/cockroachdb/errors，直接使用 New/Wrap/Wrapf/WithStack
等能力提供上下文与堆栈；保持与标准库 errors.Is/As 和 %w 错误链互操作。
fault 只补充业务 Code/Kind 和允许公开的数据，不重复实现堆栈、包装、匹配或报告设施。
第三方/应用返回标准 error 均可接入；不以禁用 fmt.Errorf 来建立兼容边界。
参考：https://github.com/cockroachdb/errors 、https://github.com/eko/gocache 。

命名约定（新增 API 按调用处评审，不仅看声明处）：

包名与边界：
  - cleanup.Stack 替代 app.Scope：实际能力是按后进先出清理资源，没有应用容器或依赖作用域。
    Stack 的零值可使用，Add 登记具名清理函数，Close 执行一次逆序清理；不叫 App/Manager。
  - logging 替代 logx：只做 slog 的配置、构建和上下文字段辅助；logging.New 返回 *slog.Logger。
    不定义 logging.Logger，也不提供另一套 Info/Error 门面。
  - 保留 httpx：与标准 net/http 区分，表示 HTTP 运行与中间件补充，不泛化成 transport/server。
  - 保留 httpx/rest：HTTP API 的 Huma 集成，调用处使用 rest；不与 huma.API 的局部变量 api 重名。
  - 保留 fault：明确是业务失败语义，不与标准库或 cockroachdb/errors 同名。
    使用 fault.Error、fault.Kind，避免 fault.FaultError、fault.ErrorKind 的重复上下文。
  - 保留 identity.Subject：已认证的主体，可以是人或服务；不是完整 User，也不是 JWT claims 容器。
    OAuth scope 指授权范围，cleanup.Stack 指资源清理，二者不再共用 Scope 作为框架类型名。
  - 保留 database、database/migrate、database/dbtest：日常调用分别是 database.Open、
    migrate.NewProvider（仅在确有默认装配语义时）、dbtest.New；不混用 db/database 两个框架包。
    database 是既定领域目录，第一期 PostgreSQL 限制写入包文档，不暗示已支持其他数据库。
  - 保留 config、i18n、command：直接描述职责；不创建 utils/common/base/support 等泛用收纳包。
  - 目录最后一段与包名一致，不用目录 http 却声明 package transport 的隐式别名。
    业务的 HTTP 子包使用 orderhttp 等名字，例如 internal/order/orderhttp；核心包为 order。
    避免与 net/http 冲突，且 order 与 orderhttp 的依赖方向清楚；这是结构示例，不要求建空包。

函数名与副作用：
  - New 只构造当前包主要对象；同包有多个构造目标时用 NewXxx，第三方原生名称保持原样。
  - Open 表示获取外部资源，ReadFile 表示文件 I/O，Parse 表示解析已有数据；不统称 Init/Setup。
  - config.ReadFile(path) / config.Parse(data) 返回 config.Document；doc.Decode(section, dst)
    表示解码、环境展开和校验指定段，默认值在 Decode 前由应用的 DefaultConfig 设置。
    env lookup 等选项按现有配置契约注入，具体参数签名由原型固定；不引入隐式全局配置。
  - httpx.Serve 表示运行已构造的 *http.Server 和 listener，并协调停机；
    不叫 Start（容易暗示异步立即返回），也不叫 NewServer（容易暗示另一个服务器实现）。
  - Register 表示登记端点；Mount 仅用于真正挂载子路由；Bind 保留给真实绑定操作。
    延迟创建 handler 的普通回调命名 newHandler，不创造 Binder/Factory 类型体系。
  - command.NewServe / NewMigrate / NewOpenAPI 等只返回 *cobra.Command；执行沿用 Cobra。
    这组名字是存在相应工厂时的规则，不要求为了名称齐全增加公开函数。
  - DefaultConfig 返回值，Validate 校验值；不混用 Defaults/Default/Normalize 表示同一行为。
    Validate 不修改配置。配置文件字段用 snake_case，Go 字段遵循原生类型习惯。

业务与传输命名：
  - 每个业务包按领域命名，如 order；有业务编排时使用 order.Service 和 order.New，
    不强迫只有简单数据结构的包也创建 Service、Repository 或 Controller。
  - 缺省依赖集合叫 Dependencies（确实需要时），不在不同包混用 Deps/Services/Container。
  - API 输入/输出用 CreateOrderInput、CreateOrderOutput，避免混用 Request/Input/Params；
    业务参数使用 CreateParams 等描述用途的名字，不把业务类型命名成 HTTPRequest。
    只有真实不同的边界才拆分类型，不因这条约定机械复制结构体。
  - 业务错误哨兵按 Go 习惯命名 ErrNotFound、ErrInsufficientStock；
    错误类型为 fault.Error，分类为 fault.InvalidArgument/fault.PermissionDenied 等。
    对外业务错误码保持 order.insufficient_stock，常量名的改动不自动改变外部错误码。
  - OperationID 使用 order-create、order-list；JSON 和错误码片段用 snake_case。
    路径资源名使用复数，如 /orders；Go 业务包使用单数 order。这里只约定新 API，
    不为了改名改写上游库的既有字段、类型或操作标识。
  - 缩写统一使用 ID、URL、HTTP、API、DB、DSN、TTL；复合词使用 OpenAPI、WebSocket、Postgres。
    使用 UserID、BaseURL、HTTPAddr、OpenAPI，不使用 UserId、BaseUrl、HttpAddr、OpenApi。

命令与概念词：
  - serve = 启动 HTTP 服务；migrate up/status/down 沿用 goose 的含义。
  - config check = 离线配置校验；doctor = 主动探测外部依赖，两者不共用含糊的 check 命令。
  - config show / openapi = 输出结果；保留当前简洁命令，不添加只有一个动作的子命令层。
  - bootstrap 是应用装配过程的说明用语，可作为应用内部包名；不是框架公开 Boot/Setup 生命周期。
  - 业务包指领域代码，Go module 指 go.mod 管理的模块，部署进程指运行实例；
    文档不把业务模块、Go module、HTTP 服务和 order.Service 混称为 service/module。

公开名称与具体签名分开冻结：上述规则确定名称表达的职责，具体函数是否需要存在、
参数和返回值以原生组件集成原型为准；不为了完成命名表而增加包装类型。
参考：https://go.dev/blog/package-names 、https://go.dev/doc/effective_go#names 、
https://go.dev/wiki/CodeReviewComments#initialisms 。

三、显式装配与资源生命周期

按动作区分配置读取、资源打开、对象构造和运行；不强迫所有命令执行统一四阶段协议。
serve：读取运行配置 → 校验 → 打开资源 → 构造业务和路由 → 监听 → 停机清理。
migrate：只读取数据库/迁移配置 → 打开数据库 → 执行 goose → 关闭。
openapi：只读取非秘密的接口描述选项 → 注册 schema → 输出；不进入资源构建路径。

mygoravel 自己的 New 不做外部 I/O、不启动 goroutine、不修改进程级状态。
第三方构造函数遵循真实契约；若打开资源或启动后台任务，就在资源打开阶段调用并登记清理。
不为统一命名包装第三方构造函数。业务通过普通参数或小型具体 Dependencies 结构接收依赖。
context 只携带取消、截止时间和请求元信息，不携带数据库、Mailer 或依赖容器。

清理使用一个小型 cleanup.Stack，只保存具名清理函数。
bootstrap 打开成功一个资源，就立即登记其关闭函数；不保存可检索的服务实例。
Stack 从创建到清理一直由调用入口持有，通过 defer 覆盖构建失败和正常退出，
不在“构建完成”时引入所有权移交协议。直接的少量 defer 同样允许，不强制使用 Stack。
清理按登记逆序执行，用独立的有限上下文；汇总全部关闭错误，不覆盖原始业务/运行错误。
登记和关闭由同一所有者串行调用；不支持关闭时继续登记，不承诺任意并发使用。
重复关闭不重复释放资源；不需要为单所有者场景设计并发关闭状态机。

第一期直接使用 *http.Server、net.Listener、Serve、Shutdown 和 Close。
httpx 只提供一处共享的 HTTP 运行函数，封装监听/退出协调和错误归并；
不引入通用 Service.Start/Wait/Stop、服务注册表、启动依赖图或多服务监督器。
HTTP 运行函数接收已构造的 server/listener，函数名和参数由端到端原型验证后固定。

默认运行顺序：
  1. 配置与业务装配成功后创建 listener，避免启动失败却短暂接单。
  2. 开始 Serve；就绪状态在必要依赖可用且服务已启动后开放。
  3. 收到退出信号先撤销 ready，再用独立截止时间调用 Shutdown，停止接单并排空。
  4. 等待 Shutdown 完成；不能因 Serve 已返回 ErrServerClosed 就提前关闭数据库。
  5. 排空超时则取消剩余请求并 Close；随后清理数据库、缓存等资源，汇总错误。

退出信号只触发退出流程，不直接取消全部请求上下文；请求根上下文在排空结束或超时后取消。
意外 Serve 失败也必须执行退出和资源释放；只在预期关闭路径忽略 ErrServerClosed。
超时后的 handler 必须遵守 context；Go 无法强杀任意 goroutine，协议不应声称能保证这一点。
live 只说明进程可响应；ready 只探测服务实际需要的依赖，并有短超时。
普通加速缓存故障默认不影响 ready；应用显式声明为必要能力时例外。
关闭语义与预算优先沿用原生组件，后期出现第二种真实运行器再评估可共享协议。
控制台一次性任务只需业务调用加 defer；核心对 HTTP 无依赖本身即可支持未来扩展。
只有 main/命令最外层处理信号和 os.Exit，库返回 error。
参考：https://pkg.go.dev/net/http#Server.Shutdown 。

四、配置契约

mygoravel 管理的配置提供 Config、DefaultConfig、Validate；产品自行组合，不强制内嵌统一的框架配置类型。
第三方组件沿用其配置和 option API，只把确实需要从配置文件读取的字段映射进去，
不复制整个上游配置结构，也不强迫外部组件实现框架配置接口。
默认值只应用一次：DefaultConfig → 一个 YAML 文件覆盖 → Validate。
环境变量仅通过 YAML 标量内的 ${VAR}、${VAR:-default} 引用；没有隐式环境扫描覆盖。
环境扩展发生在 YAML 解析之后，变量值不能改变文档结构。
拒绝未知字段、多文档输入、当前加载范围内缺失的必需环境变量及错误类型。
map 合并、slice 替换；第一期拒绝显式 YAML null，需要空集合使用 {} 或 []，
缺省保留默认值。避免让具体解码库对 null 的偶然行为成为框架契约。
false、0、空字符串和空 slice 都是合法的显式值，New 不再按零值二次补默认值。
确需区分缺省与显式零值的单个字段使用指针或局部 Optional 类型，不把所有字段 Optional 化。
配置加载后视为不可变；第一期不支持热重载。

校验按命令需求组合：serve 校验 HTTP 及所启用依赖，migrate 只校验数据库和迁移配置，
openapi 不要求数据库、缓存或认证密钥。无数据库应用不包含数据库配置。
为避免无关 ${VAR} 在全文件展开时提前失败，配置解析必须区分文档与具体段的加载：
  - config.ReadFile 从文件读取，config.Parse 从已有字节解析，均返回 config.Document；
    这一步不展开变量，也不打印值。Document.Decode 解码指定配置段时才展开变量和校验。
  - 应用显式声明允许的顶层段；未知段直接报错，不通过反射猜测启用能力。
  - 每个命令用具体配置类型解码需要的段，段内展开变量、拒绝未知字段并校验。
  - serve 加载启用的全部运行段；migrate 只加载 database/migrations。
  - config check 对完整应用执行严格校验；未被选中段的字段错误由它发现。
  - openapi 默认不读取运行配置；可选的 api 描述段只包含非秘密开关，无外部依赖。
这只是 YAML 文档的按段解码，不做依赖解析、通用配置图或隐式延迟 getter。
命令入口用普通代码写出加载哪些段；所有解码仍返回具体 Go 类型。
结构性校验、外部连通性检查分离；config check 默认离线，doctor 显式执行探测。
错误报告完整字段路径，可以聚合多个校验失败；错误文本不得泄漏秘密字段值。
Secret 必须在 slog、fmt、JSON/YAML 诊断输出中脱敏；真实值只通过显式方法获取。
config show 只输出脱敏的最终配置；默认日志不输出整份配置。
框架包不得自行调用 os.Getenv；环境查找由 config 边界注入，测试不改进程环境即可隔离。
第三方 SDK 的环境发现行为由装配时的显式配置控制，不改写上游实现。

五、错误、日志与语言

fault 定义稳定的 Code 和 Kind，不包含 HTTP 状态、翻译文本、日志器或重试次数。
Kind 至少包括 InvalidArgument、Unauthenticated、PermissionDenied、NotFound、Conflict、
Unavailable、Internal。Kind 不是业务错误码，客户端分支判断使用 Code。
业务错误模板不可变；每次失败产生独立实例，带 cause 和显式允许公开的参数。
支持 errors.Is/As；实例匹配明确的模板身份，不以 Error() 字符串比较。
业务失败实例在发生处用 cockroachdb/errors 附加堆栈，不能只记录包级模板初始化的位置。
自定义 fault 类型遵循 Unwrap/Is 及库的格式化扩展协议，测试经过多次 Wrap 后仍可匹配，
并保留原始堆栈；不在 fault 包重新导出 New/Wrap/Is 等通用错误库函数。
Code 按业务命名，例如 order.insufficient_stock；业务包测试检查自己的稳定错误定义。
错误类型自带 Code/Kind，HTTP 映射直接读取错误链，不要求把每个错误登记到中央目录。
不建设全局注册表、自动扫描所有错误的分析器或统一模块描述对象。
需要完整错误清单的公开业务 API 可显式导出清单，文档检查复用该清单，不强制所有包都提供。

HTTP 默认映射：InvalidArgument=422，Unauthenticated=401，PermissionDenied=403，NotFound=404，
Conflict=409，Unavailable=503，Internal=500；语法解码失败为 400。
限流为 HTTP 边界的 429，不强迫成为领域 Kind；特定端点可以显式覆盖默认映射。
REST 使用 Problem Details 风格响应，增加稳定 code、request_id 和字段错误列表。
不泄漏 cause、SQL、堆栈、内部参数或原始密码/token；字段校验详情也不得回显秘密值。
第一期只实现 HTTP 响应转换和运维命令退出码；后续页面、RPC 等分别实现自己的出口映射。
未知错误只对外返回通用提示；重试策略由消费边界按错误与任务策略决定，不从 HTTP 状态推导。

业务层只补上下文并返回错误；最终拥有处理决定的边界记录错误。
HTTP access log 记录请求结果，错误日志记录诊断内容，二者共享 request_id。
后期 worker 再定义 attempt 与最终失败记录；第一期不为此建设任务日志框架。
错误诊断直接使用 cockroachdb/errors 的堆栈/格式化能力；%+v 不等于安全脱敏，
不得直接写入 HTTP 响应，也不得把含秘密的参数放入包装消息或原始诊断日志。
Logger 是显式 *slog.Logger，不修改 slog.Default；日志上下文只存关联字段。
标准字段包括 service、version、component、request_id、trace_id、message_id、attempt。
Trace 设施通过标准上下文/适配器接入，不自建 tracing SDK；request_id 不冒充 trace_id。

默认错误文案由 HTTP 呈现层提供，可选开启中英文等本地化；不强迫每个业务错误同时交付两种语言。
直接构建 go-i18n.Bundle/Localizer，构建完成后约定不再修改；不另建每次翻译的 Catalog 门面。
覆盖顺序固定：框架默认 → 模块 → 产品；相同优先级重复 ID 直接报错。
对应用明确列出的消息清单检查默认语言；未登记的动态 ID 回退通用消息并记录诊断。
不声称可以在启动时发现任意运行时拼接的消息 ID；其他语言按已声明的 fallback 链回退。
中文和英文为内置目录，默认语言由应用配置，不写成不可更改的包级常量。
第一期请求语言取 Accept-Language 和默认语言；后期 Web 再增加 cookie 等策略。
翻译能力通过构造函数传入，context 只带语言标记。
错误码可作为默认翻译 ID，但业务错误语义不依赖翻译目录是否加载。
后期异步内容携带所需 locale，需保留历史原文的内容保存快照；不作为第一期模块要求。
时间保存为 UTC 时刻；展示使用明确配置的时区，不能隐式依赖服务器本地时区。

六、HTTP 与可离线读取的接口声明

使用标准 http.Handler 和 chi 路由，不引入新的全能 Context 类型。
REST 直接使用 Huma 的类型化输入/输出、校验和 OpenAPI 能力；第一期不实现页面层。
业务层不接收 Huma/chi 对象；数据库模型不直接作为传输 DTO。
默认 JSON 字段 snake_case，时间 RFC3339，响应集合为空时输出空数组。
分页默认游标，必须有确定且唯一的排序（例如 created_at,id）；游标包含版本与查询约束，
不把 UUIDv7 顺序当作严格提交顺序。有限小集合可以不分页。
限制请求体大小、上传大小、超时；可信代理列表显式配置，不默认相信外部转发头。

端点声明与处理器绑定分离，但只维护一份路由清单。
直接使用 huma.Operation、原生输入/输出和 handler 签名，不另建完整 Endpoint/Router DSL。
仅在离线声明确实需要时添加一个小型延迟构造辅助函数，其 newHandler 回调返回原生 handler；
它捕获应用内具体类型的依赖，禁止字符串查找依赖，不镜像 Huma 全部注册 API。
此辅助层只允许增加“schema 模式不调用运行时绑定”这一项语义，返回和暴露 huma.API。
不建立端点注册表、独立 schema 引擎、专用 Handler 接口或路由生命周期。
同一份显式路由登记函数支持两种消费方式：
  - 文档模式：读取 Operation 和 I/O 类型，生成 schema；绝不调用 newHandler。
  - 运行模式：资源和业务服务构造完成后调用 newHandler，注册实际处理器。
两者是行为描述，不预先定义 Describe/Mount 两套公开 API；端点登记统一称为 Register。
泛型注册使用普通泛型函数，避免 Go 不支持的方法级独立类型参数。
端点路径、类型和 OperationID 不能依赖数据库查询或运行时服务返回值。
必要的功能开关从非秘密的描述选项传入；这些选项同时决定运行时路由和文档。
不允许通过 nil 数据库、假 Mailer 或 recover 掩盖构建错误来生成文档。

文档模式复用 Huma 的 schema 生成器；若上游库要求回调，安装永不执行的内部占位函数，
不得构造业务服务。该适配必须有“newHandler 被调用即失败”的契约测试。
OpenAPI 与运行时使用相同端点定义生成，校验重复 OperationID、路由冲突和安全声明。
第一期每个端点显式声明 public 或 bearer/scope 要求；一个小型绑定函数同时设置
huma.Operation.Security 和认证中间件。没有声明则登记失败，避免文档/运行授权漂移。
资源所有权仍在业务中校验；直接使用原生 huma.Register 的应用自行显式提供安全声明和中间件，
不承诺框架能自动推断任意第三方路由的授权意图。
Huma 等库的全局可变 hook 不得用于应用专属策略；必要时在适配边界转换错误，
或选择满足实例隔离的上游扩展方式，禁止靠 sync.Once 规避隔离问题。
领域错误在 handler 边界转换；Huma 自动生成的参数错误优先通过实例级 transformer 处理。
必须删除错误详情中可能回显输入的 value；同时更新 OpenAPI 错误 schema，不能只修改实际 JSON。
PR 05 先用最小原型证明校验错误、领域错误和两个 API 实例的策略能正确隔离。
若所锁定版本缺少必要扩展点，先明确上游限制并评审最小解决方案，不能默默改全局 hook、
重写整套请求解码流程或声称原生 Huma 所有路径已自动符合框架格式。
引用：https://huma.rocks/features/openapi-generation/ 、
https://huma.rocks/features/response-errors/ 、https://huma.rocks/features/response-transformers/ 。

HTTP 中间件的相对顺序写成契约并测试：可信代理 → 请求关联 → 请求结果日志 →
panic 恢复 → 安全头/限额 → 语言 → 按需 Bearer 认证 → 路由与授权。
第一期默认纯 Bearer API，不依赖会话表；Cookie 会话及相应 CSRF 策略随 Web 阶段实现。
翻译器、错误映射等均是实例字段，没有运行时全局可变配置。

七、身份与授权

identity.Subject 表达主体 ID、主体类型、认证方式及已验证 scope，不绑定 account 模型。
主体支持用户和服务身份；第一期业务模块可使用 UUID，但基础身份契约使用不透明标识。
认证适配器验证凭据后把主体写入请求 context；业务可以显式接收主体或使用 identity 读取。
授权使用业务服务中的普通函数或消费方的小接口，决定主体是否可对资源执行动作。
第一期不建立 authz.Gate、字符串能力注册表、策略引擎或通用 RBAC/ABAC 模型。
没有凭据/没有满足明确要求的权限即拒绝；资源级检查必须靠近实际读写操作。
HTTP 声明端点要求，服务层仍负责资源所有权与业务级授权，防止 worker/CLI 绕过保护。
角色、权限存储是后期可选模块；第一期 identity 不依赖 account/role 数据库。
没有认证方式时不创建 session、refresh token 表；敏感操作可以要求最近认证或特定方式。

八、数据库、事务和迁移

直接暴露 *bun.DB / bun.IDB，不建立通用 CRUD Repository 或隐藏 SQL 的查询 DSL。
需要替换的持久化能力在业务消费方定义小接口；一个简单服务也可以直接使用 Bun。
事务由业务用例拥有，显式把事务句柄传给参与操作的函数，禁止从 context 偷取事务。
嵌套事务语义必须明确：默认不自动开启第二层，确需 savepoint 时显式调用。
已知约束违规转换成对应业务错误；未知数据库错误保留 cause 并按内部错误处理。
事务超时和查询取消遵循调用方 context；可重试事务必须明确整个用例是否可以重放。

默认表名 snake_case；模块拥有自己的表名空间。约束、索引必须具名。
不强制所有表具有相同 BaseModel；UUID、时间戳、软删除按实际语义选用。
跨模块外键由产品迁移决定：同库且需要完整性时可以使用，不因 Go 包隔离而一律禁用。
金额、状态、时间等类型在具体领域定义，框架不替业务选择所有建模方式。

迁移执行直接使用实例级 goose.Provider，应用提交一个线性 SQL 迁移目录并通过 embed 加载。
直接使用其事务、版本表、PostgreSQL session locker 等能力；禁用不需要的全局注册机制。
第一期只有应用自有迁移，不建设模块导入命令、来源锁文件、迁移依赖图或第二套执行器。
未来真正交付可复用业务模块时，再设计将其 SQL 纳入应用迁移序列的显式工作流。

沿用 goose 的 SQL 格式和命名习惯，默认有效 UTC 时间戳；同一秒冲突则生成下一个有效时刻，
不能简单对日期字符串做算术递增。CI 检查重复版本和顺序冲突；未发布迁移可整理，
已发布文件固定不变，修复使用追加迁移。
源码 CI 与明确发布基线比较已发布文件，不另外维护每份文件的重复摘要清单。
这个检查不等于数据库侧历史内容校验；不得宣称防止任意数据库历史篡改。
首次发布用测试 fixture 的旧迁移版本演练升级，不要求尚不存在的实际旧发布版本。

数据库执行锁、取消、超时、回滚和版本状态沿用 Provider；不重新维护状态机。
非事务迁移失败后按实际数据库检查人工恢复，不盲目重跑。
生产 serve 默认不执行迁移；release 步骤显式执行，变更遵循 expand/contract 支持滚动升级。
运维命令直接连接 goose up/status/down；新建迁移使用 goose 自带 create 工具或手写 SQL。
第一期不定义 import/verify 自有命令；源码不变性校验是 CI 检查，不是迁移引擎。
参考：https://pressly.github.io/goose/documentation/provider/ 。

九、缓存及成熟组件的直接集成

缓存直接使用 eko/gocache 的 cache、store、chain/loadable 等原生能力与对应版本 API。
框架不实现 Cache 接口、CacheManager、统一 Get/Set 门面、store 注册器或另一层驱动配置 DSL。
应用在 bootstrap 选择并构造需要的缓存，直接传给使用者；不默认启用 Redis 或全局缓存。
第一期提供内存和 Redis 的装配示例，验证官方 store 的接入路径；业务不需要缓存时可完全省略。
配置只暴露应用实际需要的连接、容量和 TTL 参数；复杂能力直接使用上游选项。

mygoravel 负责约定：缓存键命名空间、序列化版本、默认 TTL 的位置、失效责任、错误策略、
日志/指标接入以及资源释放。业务决定缓存是否为可丢弃的加速数据。
缓存 miss、后端失败和反序列化失败分别处理；不能把所有错误都当 miss 吞掉。
普通查询缓存默认可回源；身份、限流等安全相关用途必须显式决定后端失败时的策略。
写数据库后的缓存失效发生在事务提交之后；并发读写可能造成的旧值回填需按业务一致性处理，
不能承诺简单“提交后 Delete”即可实现强一致。只有具体用例要求时才增加版本键等策略。
不同 store 的值类型、操作和 TTL 能力依照上游真实支持范围，不伪造统一保证。

资源所有权遵循所选库版本的契约：拥有后台任务的 chain/loadable 等实例应及时 Close，
共享底层客户端只由其所有者关闭一次；按照依赖逆序清理，不能假设所有 cache 都无需释放。
框架测试验证自己的装配、隔离、错误策略和清理，不复制上游完整测试套件。

同一原则适用于其他组件：
  - HTTP 直接使用 chi/Huma，不复制其路由、参数解析和 schema 系统。
  - 数据库直接使用 Bun/pgx，迁移直接使用 goose，不复制查询/执行引擎。
  - 错误直接使用 cockroachdb/errors，不重复包装成 mygoravel/errors。
  - 日志直接注入 slog.Logger，logging 只负责共享字段、配置和边界记录约定。
  - 语言目录优先使用 go-i18n 的 Bundle/Localizer；框架只约束目录合并、缺失检查和冻结，
    不再造一套消息查询与模板语言，不强制新的 Catalog 运行时类型。
  - 后期邮件、对象存储、消息和调度也先选择成熟接口；只有具体业务消费者需要时才定义窄接口。

十、多应用形态与后续能力边界

第一期的应用业务以手写订单/笔记等普通 API 场景验证，不把现有七个业务模块整体重写
作为第一期完成条件。先稳定通用开发体验，再按场景加入可复用业务能力。
第一期实现中立身份类型、端点安全绑定和业务级权限检查，不要求完整账号产品功能。
示例可通过显式 token 校验器演示认证，不能把不验证签名/有效期的 token 解码当认证实现。
真实凭据来源由应用选择；后期 account 作为可选实现接入相同身份契约。
role/file/audit/notification/setting/export 等逐项验证后加入，核心不硬依赖这些模块。

扩展矩阵（均非第一期验收项）：
  - 控制台应用：复用配置、资源作用域、业务服务和错误；使用 Cobra 的原生命令，
    一次性业务执行完成后释放资源，不强迫有限任务实现长驻服务协议。
  - Web 页面：在 HTTP 之上按需组合 templ、scs、表单、静态资源、会话认证和 CSRF；
    复用业务服务，增加自己的错误呈现，不要求 API 应用携带页面依赖。
  - RPC：使用选定 RPC 组件的原生服务器、拦截器、请求/响应和协议工具；
    把中立业务错误映射到 RPC 状态，不要求所有业务处理器服从 HTTP DTO。
  - WebSocket：复用 HTTP 升级握手所需能力，单独定义连接生命周期、心跳、背压、
    消息授权和断连清理；不把 HTTP 请求完成视为 WS 会话结束。
  - worker/scheduler：复用资源作用域和日志，直接组合 Watermill/gocron 等组件，
    独立定义消息幂等、事务发布、重试、失败观察和排空约定。

长期允许一个产品选择单一入口或组合多个入口，但每种形态都有自己的原生 API 和适配。
共享的是业务、配置、错误与资源管理；不建立跨 HTTP/RPC/WS 的万能请求/响应模型。
HTTP 和未来 RPC 各自沿用原生生命周期；出现真实重复后再提取共性。控制台任务直接运行。
第一期通过包依赖检查和无 HTTP 的资源作用域测试验证中立性，不编写未来入口或通用服务协议。
后期顺序按真实应用需求确定，不把上表当作自动实施授权或本期 PR 的隐藏工作。

后期消息与调度应遵守的边界：声明/注册不隐式建表；数据库修改与可靠消息需要同事务；
至少一次投递要求幂等；外部副作用不能因使用队列就宣称 exactly-once；选主不能替代幂等。
这些是后续设计的前提，本期不实现队列、重投命令或任务执行框架。
后期模块继续遵守核心/入口分离、显式构造和显式目录声明，不统一要求 Module.Register/Boot。

十一、HTTP API 的开发运维命令

第一期保留服务于 HTTP API 的必要命令，直接组合 Cobra，不建设独立控制台应用框架。
command 包只提供默认命令的连接代码；应用可使用原生 *cobra.Command 添加自己的内容，
不定义第二套 Command 接口、参数 DSL 或运行器；没有“导入包即出现子命令”。

  orders serve              启动 HTTP API
  orders config check       离线校验 API 应用所需配置
  orders config show        输出脱敏后的配置
  orders doctor             显式探测所启用的外部依赖
  orders migrate up/status/down
  orders openapi            离线输出接口定义

未使用数据库的应用不提供迁移命令；CLI 帮助来自实际安装的能力。
命令有独立依赖需求：migrate 只打开迁移连接，openapi 不打开任何运行资源。
stdout 输出结果或机器可读数据，stderr 输出诊断；提供稳定退出码和必要的 json 输出选项。
命令构造函数返回 *cobra.Command；RunE/ExecuteContext 返回错误，
main 统一处理信号、退出码和最终关闭，不能把“构造命令”和“执行命令”混称为 Command。
版本包含应用版本、mygoravel 版本和构建信息；配置与命令帮助对齐。
开发热重载、容器环境使用已有工具，应用自行选择任务工具。
测试仍以 go test 为入口，不建立框架自己的测试语言或测试运行器。
本期没有 new/generate/scaffold、console、worker、scheduler、jobs 命令。

十二、测试、发布和长期维护

单元测试不依赖外部服务；核心契约测试使用外部测试包，验证公开行为。
时间与随机性在确需确定性的消费方注入小型 Clock/函数，不用全局替换 time.Now。
HTTP 使用 httptest；数据库测试使用真实 PostgreSQL 与每测试隔离数据库。
使用缓存的应用应测试所选 store、故障策略与失效行为；框架 CI 不要求 Redis。
集成测试采用显式 integration build tag；该测试任务缺少已声明依赖必须失败，不能静默跳过。
CI 至少执行：格式检查、go vet、单元测试、关键并发路径 race、真实数据库集成测试、
HTTP 契约测试和独立 module 消费测试。缓存与业务策略由使用它们的应用验证。
测试工具只提供资源创建、清理和观察能力，不复制业务断言或封装 testing.T 的完整 API。

框架与应用分别在拥有对应行为的范围内验证以下失败路径：
  - 第 N 个资源打开失败，前 N-1 个资源均被关闭且错误保留。
  - HTTP 监听失败、意外退出、排空超时；Serve 返回后等待 Shutdown 再关闭数据库。
  - 两个实例的语言、错误映射、日志器、缓存和身份策略互不影响。
  - OpenAPI 导出完全离线，延迟绑定和运行资源工厂一次也不执行。
  - 未设置认证/缓存环境变量时迁移仍可运行，OpenAPI 完全不读运行密钥。
  - config check 检出全部配置错误；零值保留、null 拒绝与框架诊断秘密脱敏。
  - cockroachdb/errors 与标准 error 链混用后的 Is/As、堆栈和 HTTP 映射正确。
  - 迁移从空库安装、fixture/发布基线升级、并发执行、已发布源码变化及非事务失败恢复。
  - 事务回滚、唯一约束、取消与错误映射符合约定。
  - 缓存 miss/故障/失效/关闭符合应用策略；无缓存时 API 正常工作。
  - 端点文档与实际鉴权一致；资源级授权在直接调用业务服务时同样有效。

独立消费测试复用 HTTP、配置、错误与命令的公开 API 契约测试，不维护业务应用。
测试文件导出到临时独立 module，使用测试构建的模块代理或实际发布版本消费框架，
不得依靠源仓库内部路径、内部包或长期 replace 隐藏发布问题。
同时验证一个不使用数据库或缓存的最小 HTTP 应用，证明可选组件没有变成隐式依赖。

框架拥有并版本化自己的约定、连接代码和命令行为，不承诺隐藏上游全部变化。
直接暴露的第三方类型意味着第三方兼容变化也是框架发布评估的一部分。
维护一个经过验证的依赖组合与 Go/PostgreSQL/可选缓存后端支持矩阵，不声称任意版本均可组合。
依赖升级运行对应契约测试，模块迁移和命令输出也纳入版本变更说明。
未来增加应用形态时增加自己的独立消费测试，不把所有后期依赖放进第一期验收环境。

十三、第一期的十个 PR

以下十个 PR 仅服务于常规 HTTP API，覆盖从基础约定到实际应用的完整路径。
每个 PR 自带其能力的测试和包文档，不把测试留到最后。
不保留旧 API，也不以兼容桥接污染最终设计；旧代码只按明确范围退出目标构建。
现有页面、任务和业务模块可以在实施时移出第一期主构建或归档，后期另行整合，
不为消除旧代码编译错误而暗中完成所有后期功能；归档和移除范围在实施 PR 中列明。
新增包装必须列出上游原生 API、缺失的具体语义及为何仅靠装配不足，由评审检查。
所有 PR 保持可构建、可验证；下面的依赖是交付依赖，不是日历工期。

PR 01 — 明确 mygoravel 定位、范围和包依赖
  依赖：无。
  交付：统一目标 module/import 为 github.com/ryan053857/mygoravel；目标包图、
        架构约束测试、基础 CI；编写用户文档；列明旧功能退出本期构建的方式。
        建立直接使用成熟组件的评审标准、命名约定、HTTP 契约测试需求和后期形态清单。
  验收：包名与调用处读法一致，无重复前缀及含义混用；核心不依赖 HTTP；基础 CI 可运行；
        第一期不引入页面、RPC、WS、队列等后期依赖或空包。
  不做：修改远端仓库设置、兼容容器、代码生成、提前实现多种入口。

PR 02 — 类型化配置、日志装配与可选本地化
  依赖：01。
  交付：config 的文档解析/按段解码、slog 装配和上下文字段约定、可选 go-i18n 目录组合；
        默认值、环境注入、未知字段拒绝、配置校验和脱敏契约。
  验收：零值覆盖正确；两个实例配置/语言/日志互不污染；环境值不能改变 YAML 结构；
        完整配置检查严格；迁移不展开无关变量；格式化与序列化诊断脱敏；无可变全局目录。
  不做：日志门面、第二套翻译 API、热重载、复制所有第三方配置结构。

PR 03 — cockroachdb/errors 与业务错误语义
  依赖：01、02。
  交付：直接依赖 cockroachdb/errors；fault 的 Code/Kind/公开参数；
        包装、堆栈、唯一日志责任及 HTTP 错误映射所需的中立规则。
  验收：标准 error 和库包装混用后仍可 Is/As；业务实例堆栈指向实际失败处；
        自定义 fault 不截断格式化错误链；内部原因与可公开信息分离。
  不做：mygoravel/errors 通用门面、重写堆栈实现、将 HTTP 状态放入业务错误。

PR 04 — 资源清理栈与所有权约定
  依赖：02、03。
  交付：小型 cleanup.Stack、单所有者清理契约、错误汇总及构建失败清理示例。
        同一作用域贯穿资源打开与退出，无所有权移交；HTTP 运行逻辑在 05 落地。
  验收：部分资源打开失败也完整清理；逆序、重复关闭和原始错误保留正确；
        清理上下文不受已取消请求影响；无 HTTP 依赖。
  不做：DI、通用 Service 协议、并发关闭状态机、服务监督器或未来运行角色。

PR 05 — 原生 HTTP API 组合与 OpenAPI
  依赖：02、03、04。
  交付：先验证 Huma 原生扩展点的最小原型，再交付 chi/Huma 装配与原生 HTTP 运行函数；
        原生 Operation 与 I/O 声明复用，必要的最小延迟绑定连接代码及离线文档 API。
  验收：离线文档不执行绑定/打开资源；校验与领域错误 schema/响应一致且不回显秘密；
        两个实例策略隔离；最小 /ping API 已能真实监听并排空，不等到 09 才首次运行；
        Shutdown 完成前数据库不会关闭；应用仍可直接注册原生 Huma 端点。
  不做：框架 Router/Endpoint DSL、页面渲染、Cookie 会话、全局 Huma hook。

PR 06 — 数据库、事务与 goose 迁移集成
  依赖：02、03、04。
  交付：Bun/pgx 连接装配、事务约定、数据库错误识别、dbtest；
        goose.Provider 原生执行与锁、应用 SQL 目录和 embed；迁移源码基线比较工具与测试。
  验收：真实 PostgreSQL 空库安装和 fixture/发布基线升级；已发布文件变化检查；并发迁移；
        事务回滚/约束/取消；非事务失败的恢复演练；集成环境缺失明确失败。
  不做：通用 Repository、模块迁移供应链、第二套执行器/历史表、多数据库适配。

PR 07 — eko/gocache 直接集成与缓存约定
  依赖：02、03、04、06。
  交付：直接使用 gocache 的独立 API 文档、后端选择与资源所有权约定；
        说明 cache-aside、错误策略、事务提交后失效和并发一致性限制。
  验收：文档片段可编译，直接使用上游类型和选项；框架不安装可选缓存依赖；
        无缓存 HTTP 应用可运行。后端、失效与清理行为由选择缓存的应用集成测试验证。
  不做：mygoravel/cache、CacheManager、驱动注册器、复制 chain/loadable 实现。

PR 08 — API 身份授权与边界契约
  依赖：03、05、06。
  交付：identity.Subject、可注入的凭据校验、HTTP Bearer 与端点安全绑定；
        DTO、校验、分页结构、资源权限和事务的独立文档片段。
  验收：公开/认证/越权路径；文档与实际安全策略一致；
        输入校验、领域错误与数据库错误映射有契约测试。资源级授权由应用业务测试验证。
  不做：通用授权注册表/策略引擎、完整账号系统、MFA/OAuth、七个旧业务模块整体重写。

PR 09 — API 开发运维命令与用户文档
  依赖：04—08。
  交付：原生 Cobra 的 serve/config/doctor/migrate/openapi 命令组合；
        独立的配置与命令片段，以及从空 Go module 到运行 HTTP API 的开发指南。
  验收：命令只打开所需资源；OpenAPI 离线且无需运行密钥；不启用数据库时没有迁移命令；
        stdout/stderr、退出码、信号和资源释放有命令级测试；标准 go test 可直接使用。
  不做：console 业务框架、web/worker 示例、RPC/WS、生成器或自定义命令 DSL。

PR 10 — 独立消费与第一期发布验收
  依赖：01—09。
  交付：外部 module 的公开 API 契约测试、发布候选消费测试、依赖版本说明、
        fixture/已发布版本迁移演练、生产启动/排空说明、公开 API 和包装必要性审查。
        清理本期目标中的旧命名与旧约定，明确归档能力的后期路线。
  验收：CI 完整运行单元/race/数据库集成/HTTP 契约/独立消费测试；
        两应用隔离、无数据库/缓存 HTTP 应用、离线文档、独立迁移、请求排空均通过；
        没有关键测试被静默跳过，也没有重复封装成熟组件的门面。
  不做：为“多应用框架”名义提前实现后期入口、恢复兼容层或生成器。

审核结论与设计冻结条件：
  - 本规格定义方向和可验证契约，不声称存在脱离实际用例的“唯一最优”架构。
  - 已移除第一期没有真实消费者的通用运行协议、授权注册表和模块迁移供应链。
  - Huma 的离线注册/错误转换和 HTTP 排空先做最小原型，验证后才冻结公开 API。
  - 一个需要数据库、鉴权、缓存的 API 与一个不需要这些资源的 HTTP 应用，
    必须都能用普通装配代码完成；不能用最短 main 行数掩盖隐式依赖。
  - 约定由默认组合和示例兑现；不要把每条约定都变成一个公共接口、注册器或生成器。

第一期完成定义：开发者使用 mygoravel 和所选社区组件的原生 API，手写少量装配代码，
即可完成常规 HTTP API 的配置、路由、错误、授权、数据库、迁移、可选缓存、测试和运行。
未来的控制台、Web、RPC、WebSocket 和任务应用能够复用中立核心，但不属于本期交付。
*/
