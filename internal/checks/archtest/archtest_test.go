// Package archtest 检查框架包的分层、依赖方向与 HTTP API 交付范围。
package archtest

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const module = "github.com/go-tenon/tenon"

// layer 是框架包所在的层。包只能引用同层或更低层的包，并且引用必须列在 rules 中。
type layer int

const (
	// 基础：错误、身份、配置、日志等，只依赖同层的包。
	layerFoundation layer = iota + 1
	// 基础设施：与入口无关的能力，包括数据库、队列、锁、文件、邮件等外部资源，以及各入口共用的契约和格式。
	layerInfrastructure
	// 入口：HTTP API、页面、实时频道和命令行。
	layerEntry
	// 功能：建立在以上各层之上的应用功能。入口不引用它们。
	layerFeature
)

// rule 是一棵包树的依赖规则。
type rule struct {
	layer   layer
	imports []string // 它可以引用的其他包树（rules 的键）。只列出实际用到的引用，TestDependencyRulesAreUsed 检查没有多余的条目。
}

// rules 的键是相对模块根的包路径，规则对这个包和它的子包生效；子包另有规则时用子包的（最长的键优先）。
// 同一条规则内的包可以互相引用。internal 中是测试辅助包，可以引用任何框架包。
var rules = map[string]rule{
	"acquire":   {layerFoundation, nil},
	"cleanup":   {layerFoundation, nil},
	"config":    {layerFoundation, nil},
	"errorx":    {layerFoundation, nil},
	"i18n":      {layerFoundation, []string{"errorx"}},
	"enum":      {layerFoundation, []string{"i18n"}},
	"hashing":   {layerFoundation, nil},
	"health":    {layerFoundation, []string{"logging"}},
	"doctor":    {layerFoundation, []string{"errorx", "logging"}},
	"identity":  {layerFoundation, []string{"errorx"}},
	"logging":   {layerFoundation, []string{"errorx"}},
	"page":      {layerFoundation, []string{"errorx"}},
	"period":    {layerFoundation, []string{"errorx"}},
	"crypt":     {layerFoundation, []string{"errorx"}},
	"telemetry": {layerFoundation, nil},
	// 设计令牌：解析 theme/tokens.json，生成三套界面的主题。

	"lock":        {layerInfrastructure, nil},
	"outbound":    {layerInfrastructure, nil},
	"captcha":     {layerInfrastructure, []string{"errorx", "outbound"}},
	"imgproxy":    {layerInfrastructure, nil},
	"database":    {layerInfrastructure, []string{"acquire", "doctor", "errorx", "logging"}},
	"ratelimit":   {layerInfrastructure, []string{"errorx"}},
	"document":    {layerInfrastructure, []string{"errorx"}},
	"pdf":         {layerInfrastructure, []string{"doctor", "errorx"}},
	"mail":        {layerInfrastructure, []string{"doctor", "i18n", "logging"}},
	"sms":         {layerInfrastructure, []string{"errorx", "logging"}},
	"sms/aliyun":  {layerInfrastructure, []string{"internal/aliyunapi", "outbound", "sms"}},
	"sms/tencent": {layerInfrastructure, []string{"outbound", "sms"}},
	// 阿里云 OpenAPI 的签名和调用，push/aliyun 和 sms/aliyun 共用。
	"internal/aliyunapi":    {layerInfrastructure, nil},
	"internal/indexversion": {layerInfrastructure, []string{"errorx"}},
	"natsx":                 {layerInfrastructure, []string{"logging"}},
	"queue":                 {layerInfrastructure, []string{"database", "doctor", "errorx", "logging", "natsx"}},
	"cache":                 {layerInfrastructure, []string{"doctor", "logging"}},
	"schedule":              {layerInfrastructure, []string{"lock", "logging"}},
	"broadcast":             {layerInfrastructure, []string{"identity", "logging"}},
	"filesystem":            {layerInfrastructure, []string{"database", "doctor", "errorx", "identity"}},
	"sheet":                 {layerInfrastructure, []string{"errorx", "form"}},
	// 接口契约：Go 类型的 schema、校验、枚举和验证错误，HTTP、RPC、MCP 三个入口共用；Huma 的 schema 引擎在这一层。
	"contract": {layerInfrastructure, []string{"enum", "i18n"}},
	"form":     {layerInfrastructure, []string{"contract", "i18n"}},

	"httpx":           {layerEntry, []string{"acquire", "contract", "errorx", "health", "i18n", "identity", "logging", "ratelimit"}},
	"clientgen":       {layerEntry, []string{"command", "contract", "enum", "errorx", "httpx", "i18n", "identity", "page"}},
	"realtime":        {layerEntry, []string{"broadcast", "httpx", "identity", "logging"}},
	"frontend":        {layerEntry, []string{"logging"}},
	"mcpx":            {layerEntry, []string{"contract", "errorx", "httpx", "identity", "logging"}},
	"natsrpc":         {layerEntry, []string{"contract", "errorx", "i18n", "identity", "logging"}},
	"natsrpc/rpccmd":  {layerEntry, []string{"command", "natsrpc"}},
	"mcpx/mcpxtest":   {layerEntry, []string{"identity", "mcpx"}},
	"mcpx/mcpcmd":     {layerEntry, []string{"command", "identity", "mcpx"}},
	"command":         {layerEntry, []string{"cleanup", "config", "errorx", "i18n", "logging"}},
	"command/httpcmd": {layerEntry, []string{"clientgen", "command", "httpx"}},
	// tenon 命令行（go tool tenon）：开发与部署工具，不被框架的其他包引用。
	"cmd/tenon":                   {layerEntry, []string{"cmd/tenon/internal/agents", "cmd/tenon/internal/check", "cmd/tenon/internal/deploy", "cmd/tenon/internal/dev", "cmd/tenon/internal/devmcp", "cmd/tenon/internal/project", "cmd/tenon/internal/scaffold", "frontend"}},
	"cmd/tenon/internal/check":    {layerEntry, []string{"cmd/tenon/internal/dev", "cmd/tenon/internal/project"}},
	"cmd/tenon/internal/deploy":   {layerEntry, nil},
	"cmd/tenon/internal/devmcp":   {layerEntry, []string{"cmd/tenon/internal/agents", "cmd/tenon/internal/deploy", "cmd/tenon/internal/dev", "cmd/tenon/internal/project"}},
	"cmd/tenon/internal/agents":   {layerEntry, []string{"cmd/tenon/internal/project"}},
	"cmd/tenon/internal/dev":      {layerEntry, []string{"cmd/tenon/internal/agents", "cmd/tenon/internal/project", "frontend"}},
	"cmd/tenon/internal/project":  {layerEntry, []string{"config"}},
	"cmd/tenon/internal/scaffold": {layerEntry, nil},
	"command/migratecmd":          {layerEntry, []string{"command", "database", "doctor"}},
	"doctor/doctorcmd":            {layerEntry, []string{"command", "doctor"}},
	"queue/queuecmd":              {layerEntry, []string{"command", "queue"}},
	"queue/queueapi":              {layerEntry, []string{"errorx", "httpx", "i18n", "identity", "logging", "page", "queue"}},
	"schedule/schedulecmd":        {layerEntry, []string{"command", "schedule"}},
	"schedule/scheduleapi":        {layerEntry, []string{"database", "errorx", "httpx", "i18n", "identity", "logging", "page", "queue", "schedule"}},

	"apitoken":          {layerFeature, []string{"database", "errorx", "identity"}},
	"authz":             {layerFeature, []string{"cache", "database", "errorx", "identity", "logging"}},
	"notification":      {layerFeature, []string{"database", "errorx", "identity"}},
	"feature":           {layerFeature, []string{"errorx", "identity"}},
	"settings":          {layerFeature, []string{"database", "errorx"}},
	"savedview":         {layerFeature, []string{"contract", "database", "enum", "errorx", "identity"}},
	"activity":          {layerFeature, []string{"database", "identity", "logging", "page"}},
	"operation":         {layerFeature, []string{"database", "errorx", "i18n", "identity", "queue"}},
	"privacy":           {layerFeature, []string{"database", "errorx", "identity"}},
	"privacy/export":    {layerFeature, []string{"database", "errorx", "filesystem", "identity", "operation", "privacy"}},
	"webhook":           {layerFeature, []string{"contract", "database", "errorx", "identity", "outbound", "queue"}},
	"push":              {layerFeature, []string{"database", "errorx", "identity", "notification", "queue"}},
	"push/aliyun":       {layerFeature, []string{"internal/aliyunapi", "outbound", "push", "queue"}},
	"push/pushtest":     {layerFeature, []string{"push"}},
	"sheet/imports":     {layerFeature, []string{"database", "errorx", "filesystem", "i18n", "identity", "operation", "sheet"}},
	"sheet/exports":     {layerFeature, []string{"database", "filesystem", "identity", "operation", "sheet"}},
	"search":            {layerFeature, []string{"database", "errorx", "identity", "internal/indexversion", "operation", "page", "queue"}},
	"search/gse":        {layerFeature, []string{"search"}},
	"search/searchcmd":  {layerFeature, []string{"command", "database", "identity", "operation"}},
	"vector":            {layerFeature, []string{"ai", "database", "doctor", "errorx", "filesystem", "identity", "internal/indexversion", "operation", "queue", "search"}},
	"vector/vectortest": {layerFeature, []string{"vector"}},
	"ai":                {layerFeature, nil},
	"ai/chat":           {layerFeature, []string{"ai", "broadcast", "errorx", "httpx", "lock", "logging"}},
	"login":             {layerFeature, []string{"crypt", "database", "errorx", "hashing", "identity", "ratelimit"}},
	"login/oauth":       {layerFeature, []string{"errorx", "login"}},
	"oauthserver":       {layerFeature, []string{"apitoken", "database", "errorx", "httpx", "identity", "logging", "mcpx", "outbound"}},
	"login/passkey":     {layerFeature, []string{"crypt", "errorx", "identity", "login"}},

	"internal": {},
}

// 传输、数据库和命令组件的包前缀，基础层不引用它们。
var components = []string{
	"net/http", "database/sql", "github.com/danielgtaylor/huma",
	"github.com/go-chi/chi", "github.com/uptrace/bun", "github.com/jackc/pgx",
	"github.com/pressly/goose", "github.com/spf13/cobra",
	"github.com/nats-io/nats.go", "github.com/redis/go-redis",
}

func under(pkg, prefix string) bool {
	return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
}

// ruleOf 返回 pkg 适用的规则的键：rules 中是 pkg 或其上级目录的最长的键。pkg 不在本模块中，或没有规则时 ok 为 false。
func ruleOf(pkg string) (key string, ok bool) {
	rel, internal := strings.CutPrefix(pkg, module+"/")
	if !internal {
		return "", false
	}
	for k := range rules {
		if under(rel, k) && len(k) > len(key) {
			key, ok = k, true
		}
	}
	return key, ok
}

func importViolation(from, to string) string {
	if !under(from, module) {
		return "source package is outside the framework module"
	}
	fromKey, known := ruleOf(from)
	if !known {
		return "source package has no dependency rule"
	}
	fromRule := rules[fromKey]
	if under(to, module) {
		toKey, known := ruleOf(to)
		if !known {
			return "target package has no dependency rule"
		}
		if toKey != fromKey && fromKey != "internal" {
			if rules[toKey].layer > fromRule.layer {
				return "import goes up a layer"
			}
			if !slices.Contains(fromRule.imports, toKey) {
				return "import reverses the framework dependency direction"
			}
		}
	}
	if fromRule.layer == layerFoundation {
		for _, prefix := range components {
			if under(to, prefix) {
				return "foundation package imports an infrastructure component"
			}
		}
	}
	return ""
}

// imports 返回模块中每个包直接引用的包。
func imports(t *testing.T) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}`, module+"/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	graph := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		graph[fields[0]] = fields[1:]
	}
	return graph
}

func TestPackageGraph(t *testing.T) {
	for pkg, imps := range imports(t) {
		if _, known := ruleOf(pkg); !known {
			t.Errorf("%s: package has no dependency rule", pkg)
			continue
		}
		for _, imp := range imps {
			if reason := importViolation(pkg, imp); reason != "" {
				t.Errorf("%s imports %s: %s", pkg, imp, reason)
			}
		}
	}
}

// TestRulesRespectLayers 确保 rules 本身符合分层：每条规则放行的引用都在同层或更低层，并且指向存在的规则。
func TestRulesRespectLayers(t *testing.T) {
	for key, r := range rules {
		if key != "internal" && (r.layer < layerFoundation || r.layer > layerFeature) {
			t.Errorf("%s: no layer", key)
		}
		for _, to := range r.imports {
			target, known := rules[to]
			switch {
			case !known:
				t.Errorf("%s may import %s, which has no rule", key, to)
			case target.layer > r.layer:
				t.Errorf("%s (layer %d) may import %s (layer %d): imports only go to the same or a lower layer", key, r.layer, to, target.layer)
			}
		}
	}
}

// TestDependencyRulesAreUsed 确保 rules 中没有用不到的条目：放行了却没有用到的引用，
// 以后被悄悄用上时，测试就发现不了它。
func TestDependencyRulesAreUsed(t *testing.T) {
	used := map[string]map[string]bool{}
	for pkg, imps := range imports(t) {
		from, _ := ruleOf(pkg)
		if used[from] == nil {
			used[from] = map[string]bool{}
		}
		for _, imp := range imps {
			if to, internal := ruleOf(imp); internal {
				used[from][to] = true
			}
		}
	}
	for from, r := range rules {
		if used[from] == nil {
			t.Errorf("%s: dependency rule for a package that does not exist", from)
		}
		for _, to := range r.imports {
			if !used[from][to] {
				t.Errorf("%s may import %s but does not: remove it from the rule", from, to)
			}
		}
	}
}

func TestDependencyRules(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		allowed  bool
	}{
		{module + "/httpx/rest", module + "/errorx", true},
		{module + "/database/dbtest", module + "/database/migrate", true},
		{module + "/database", module + "/config", false},
		{module + "/config", "go.yaml.in/yaml/v4", true},
		{module + "/errorx", module + "/httpx/rest", false},
		{module + "/httpx", module + "/command", false},
		{module + "/database", module + "/httpx", false},
		{module + "/filesystem", module + "/identity", true},
		{module + "/filesystem", module + "/httpx", false},
		{module + "/database", module + "/filesystem", false},
		{module + "/mail", module + "/i18n", true},
		{module + "/mail", module + "/database", false},
		{module + "/httpx", module + "/mail", false},
		{module + "/apitoken", module + "/httpx/rest", false},
		{module + "/apitoken", module + "/cache", false},
		{module + "/authz", module + "/httpx/rest", false},
		{module + "/lock", "github.com/uptrace/bun", true},
		{module + "/lock", module + "/database", false},
		{module + "/cleanup", "github.com/jackc/pgx/v5", false},
		{module + "/config", "net/http", false},
		{module + "/i18n", "github.com/spf13/cobra", false},
		{module + "/logging", module + "/identity", false},
		{module + "/identity", module + "/internal/checks/archtest", false},
		// 子包有自己的规则，不放宽所在的包树。
		{module + "/sheet", module + "/operation", false},
		{module + "/sheet/imports", module + "/operation", true},
		{module + "/sheet/imports", module + "/sheet", true},
		{module + "/sheet", module + "/sheet/imports", false},
		{module + "/command", module + "/httpx", false},
		{module + "/command/httpcmd", module + "/httpx/rest", true},
		{module + "/cmd/tenon/internal/dev", module + "/cmd/tenon/internal/project", true},
		{module + "/httpx", module + "/cmd/tenon", false},
		{module + "/ai/openai", module + "/ai", true},
		{module + "/ai", module + "/httpx", false},
		// 引用只能指向同层或更低层。
		{module + "/errorx", module + "/database", false},
		{module + "/queue", module + "/httpx", false},
		{module + "/httpx", module + "/apitoken", false},
		{module + "/contract", "github.com/danielgtaylor/huma/v2", true},
		{module + "/enum", "github.com/danielgtaylor/huma/v2", false},
		{module + "/enum", module + "/contract", false},
		{module + "/page", "net/http", false},
	} {
		t.Run(tc.from+"->"+tc.to, func(t *testing.T) {
			reason := importViolation(tc.from, tc.to)
			if (reason == "") != tc.allowed {
				t.Fatalf("allowed=%v reason=%q", tc.allowed, reason)
			}
		})
	}
}

// TestLocalFilesDoesNotLinkS3 确保仅使用本地文件服务的应用，依赖中排除了 S3 SDK。
func TestLocalFilesDoesNotLinkS3(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", module+"/filesystem/local").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if under(dep, "github.com/aws/aws-sdk-go-v2") {
			t.Errorf("local filesystem depends on %s", dep)
		}
	}
}

// TestOnlyRealtimeLinksCentrifuge 确保 Centrifuge 只出现在使用 WebSocket 实时频道的应用中：
// 广播、SSE 和 AI 的集群协作都只依赖 broadcast。
func TestOnlyRealtimeLinksCentrifuge(t *testing.T) {
	for _, pkg := range []string{"/broadcast", "/broadcast/natsbroadcast", "/httpx/rest", "/frontend", "/ai", "/ai/chat"} {
		out, err := exec.Command("go", "list", "-deps", module+pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			if under(dep, "github.com/centrifugal") {
				t.Errorf("%s depends on %s", pkg, dep)
			}
		}
	}
}

// TestDocumentCoreIsLight 确保只切分 Markdown 或只用文本转换器的应用，依赖中排除了 PDF、Excel 和 HTML 的依赖，
// 它们分别在 document/convert/pdf、xlsx 和 html 中；只用 PDF 转换器时也排除了 Excel 和 HTML 的依赖。
func TestDocumentCoreIsLight(t *testing.T) {
	heavy := []string{"github.com/klippa-app/go-pdfium", "github.com/tetratelabs/wazero", "github.com/xuri/excelize", "github.com/JohannesKaufmann/html-to-markdown"}
	for pkg, allowed := range map[string][]string{
		"/document/split":        nil,
		"/document/convert":      nil,
		"/document/convert/text": nil,
		"/document/convert/csv":  nil,
		"/document/convert/docx": nil,
		"/document/convert/pptx": nil,
		"/document/convert/pdf":  {"github.com/klippa-app/go-pdfium", "github.com/tetratelabs/wazero"},
		"/document/convert/xlsx": {"github.com/xuri/excelize"},
		"/document/convert/html": {"github.com/JohannesKaufmann/html-to-markdown"},
	} {
		out, err := exec.Command("go", "list", "-deps", module+pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			for _, h := range heavy {
				if under(dep, h) && !slices.Contains(allowed, h) {
					t.Errorf("%s depends on %s", pkg, dep)
				}
			}
		}
	}
}

// TestSearchCoreIsLight 确保 search 不链接 gse 和它嵌入的词典，也不链接 Cobra，它们只由 search/gse 和 search/searchcmd 引入。
func TestSearchCoreIsLight(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", module+"/search").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if under(dep, "github.com/go-ego/gse") || under(dep, "github.com/spf13/cobra") {
			t.Errorf("search depends on %s", dep)
		}
	}
}

// TestAICoreIsLight 确保只用到模型能力（图片、语音、向量、重排、决策）的应用，依赖中排除了对话式 agent 的依赖：
// trpc-agent-go 及其带来的 gRPC、Huma 和数据库组件都只由 ai/chat 引入。
func TestAICoreIsLight(t *testing.T) {
	heavy := []string{module + "/ai/chat", "trpc.group/trpc-go", "google.golang.org/grpc", "github.com/danielgtaylor/huma", "github.com/uptrace/bun", module + "/httpx", module + "/broadcast", module + "/lock"}
	for _, pkg := range []string{"/ai", "/ai/openai", "/ai/openrouter", "/ai/systemone", "/ai/aitest"} {
		out, err := exec.Command("go", "list", "-deps", module+pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			for _, h := range heavy {
				if under(dep, h) {
					t.Errorf("%s depends on %s", pkg, dep)
				}
			}
		}
	}
}

// TestMailCoreDoesNotLinkDrivers 确保只用 mail 核心包（例如在测试中用 mailtest）时，依赖中排除了各驱动的 SDK。
func TestMailCoreDoesNotLinkDrivers(t *testing.T) {
	for _, pkg := range []string{module + "/mail", module + "/mail/mailtest", module + "/mail/logmail"} {
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			if under(dep, "github.com/wneessen/go-mail") || under(dep, "github.com/resend/resend-go") {
				t.Errorf("%s depends on %s", pkg, dep)
			}
		}
	}
}

// TestOnlyCacheImportsRedis 确保只有 cache 直接引用 Redis 客户端：锁和限流保存在 PostgreSQL 中，
// 广播和在线成员在 NATS 中，其余包都不直接依赖 Redis。realtime 依赖的 Centrifuge 自带 Redis broker，
// 间接链接 rueidis，但运行时不连接 Redis。
func TestOnlyCacheImportsRedis(t *testing.T) {
	for pkg, imps := range imports(t) {
		if pkg == module+"/cache" {
			continue
		}
		for _, imp := range imps {
			if under(imp, "github.com/redis") {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
}

// TestCoreDoesNotLinkNATS 确保广播和实时频道的核心包不链接 NATS 客户端，测试可以用内存替身代替 NATS；
// NATS 实现分别在 broadcast/natsbroadcast 和 realtime/natspresence 中。
func TestCoreDoesNotLinkNATS(t *testing.T) {
	for _, pkg := range []string{module + "/broadcast", module + "/realtime", module + "/lock", module + "/ratelimit", module + "/httpx"} {
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			if under(dep, "github.com/nats-io/nats.go") {
				t.Errorf("%s depends on %s", pkg, dep)
			}
		}
	}
}

// TestCommandCoreIsLight 确保只用 command 核心包的应用（例如常驻 worker）
// 依赖中排除了 HTTP API 和数据库组件；它们分别在 command/httpcmd 和 command/migratecmd 中。
func TestCommandCoreIsLight(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", module+"/command").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, heavy := range []string{module + "/httpx", module + "/database", "github.com/danielgtaylor/huma", "github.com/jackc/pgx", "github.com/pressly/goose"} {
			if under(dep, heavy) {
				t.Errorf("command depends on %s", dep)
			}
		}
	}
}
