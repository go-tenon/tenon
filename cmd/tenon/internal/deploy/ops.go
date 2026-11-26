package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"golang.org/x/sync/errgroup"
)

// RollbackReport 是 tenon deploy rollback 的结构化结果。
type RollbackReport struct {
	App       string           `json:"app"`        // 应用名
	Env       string           `json:"env"`        // 环境名
	ReleaseID int              `json:"release_id"` // 回退后的发布序号
	Version   string           `json:"version"`    // 回退后的构建版本号
	Waves     []Wave           `json:"waves"`      // 执行回退的波次
	Hosts     []HostSwitchInfo `json:"hosts"`      // 各主机切换详情
}

// ReleasesReport 是 tenon deploy releases 的结构化结果。
type ReleasesReport struct {
	App            string     `json:"app"`             // 应用名
	Env            string     `json:"env"`             // 环境名
	CurrentRelease int        `json:"current_release"` // 当前激活的发布序号
	Releases       []Manifest `json:"releases"`        // 按发布序号倒序排列的留存发布列表
}

// ListReleases 读取主节点留存的全部发布清单与当前激活发布序号。
func ListReleases(ctx context.Context, r Runner, t Target) (*ReleasesReport, error) {
	primary := t.MigrateHost()
	manifests, curID, err := readHostReleases(ctx, r, t, primary)
	if err != nil {
		return nil, err
	}
	return &ReleasesReport{
		App:            t.App,
		Env:            t.EnvName,
		CurrentRelease: curID,
		Releases:       manifests,
	}, nil
}

// FormatReleases 将发布历史列表格式化为对齐的表格输出到 out。
func FormatReleases(out io.Writer, report *ReleasesReport) {
	fmt.Fprintf(out, "环境: %s (应用: %s)\n", report.Env, report.App)
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CUR\tRELEASE\tVERSION\tKIND\tBIN\tCONFIG\tDEPLOYED AT\tBY")
	for _, m := range report.Releases {
		cur := ""
		if m.ReleaseID == report.CurrentRelease {
			cur = "*"
		}
		cfg := "-"
		if m.ConfigSHA256 != "" {
			cfg = ShortSHA(m.ConfigSHA256)
		}
		fmt.Fprintf(tw, "%s\t#%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			cur, m.ReleaseID, m.Version, m.Kind, ShortSHA(m.BinSHA256), cfg,
			m.DeployedAt.UTC().Format("2006-01-02 15:04:05Z"), m.DeployedBy)
	}
	_ = tw.Flush()
}

// readHostReleases 读取指定主机上全部 releases/*/manifest.json（按 release_id 倒序）及当前激活序号。
func readHostReleases(ctx context.Context, r Runner, t Target, h Host) ([]Manifest, int, error) {
	curPath := path.Join(t.Dir, "current", "manifest.json")
	releasesDir := path.Join(t.Dir, "releases")
	script := fmt.Sprintf(`if test -f %s; then
  echo "---current"
  cat %s
  echo ""
fi
for f in %s/*/manifest.json; do
  [ -f "$f" ] || continue
  echo "---manifest"
  cat "$f"
  echo ""
done`, shellQuote(curPath), shellQuote(curPath), shellQuote(releasesDir))
	raw, err := RunCapture(ctx, r, h, script)
	if err != nil {
		return nil, 0, fmt.Errorf("[%s] 读取发布列表失败: %w", h.Addr, err)
	}
	var currentID int
	var manifests []Manifest
	var section string
	var buf strings.Builder
	flush := func() error {
		text := strings.TrimSpace(buf.String())
		buf.Reset()
		if text == "" {
			return nil
		}
		var m Manifest
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			return err
		}
		switch section {
		case "current":
			currentID = m.ReleaseID
		case "manifest":
			manifests = append(manifests, m)
		}
		return nil
	}
	for line := range strings.Lines(raw) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---current" || trimmed == "---manifest" {
			if err := flush(); err != nil {
				return nil, 0, fmt.Errorf("[%s] 解析发布清单失败: %w", h.Addr, err)
			}
			section = strings.TrimPrefix(trimmed, "---")
			continue
		}
		buf.WriteString(line)
	}
	if err := flush(); err != nil {
		return nil, 0, fmt.Errorf("[%s] 解析发布清单失败: %w", h.Addr, err)
	}
	slices.SortFunc(manifests, func(a, b Manifest) int { return b.ReleaseID - a.ReleaseID })
	return manifests, currentID, nil
}

// Rollback 将环境内所有主机回退到指定目标（targetRef 可为发布序号如 41/#41、构建版本/Tag 如 v1.1.0，
// 留空时按 history.jsonl 追溯当前版本之前最近一次激活的不同发布）；切换前在所有主机执行 config check。
// 数据库迁移不会自动撤销。
func Rollback(ctx context.Context, r Runner, t Target, targetRef string, out, errOut io.Writer) (*RollbackReport, error) {
	primary := t.MigrateHost()
	operator := CurrentOperator()
	releaseLock, err := acquireLock(ctx, r, t, primary, fmt.Sprintf("rollback (%s)", operator))
	if err != nil {
		return nil, err
	}
	defer releaseLock()

	targetManifest, err := resolveRollbackTarget(ctx, r, t, primary, strings.TrimSpace(targetRef))
	if err != nil {
		return nil, err
	}
	relName := FormatReleaseID(targetManifest.ReleaseID)
	releaseDir := path.Join(t.Dir, "releases", relName)
	releaseBin := path.Join(releaseDir, t.App)
	releaseCfg := path.Join(releaseDir, "config")

	// 1. 确认全部主机均保留目标发布目录，避免滚动到一半缺文件。
	hasConfig := false
	for i, h := range t.Hosts {
		check := fmt.Sprintf("test -x %s || { echo 'release #%d not found' >&2; exit 1; }; if test -d %s; then echo cfg; fi",
			shellQuote(releaseBin), targetManifest.ReleaseID, shellQuote(releaseCfg))
		res, err := RunCapture(ctx, r, h, check)
		if err != nil {
			return nil, fmt.Errorf("[%s] 检查回退发布 #%d 失败: %w", h.Addr, targetManifest.ReleaseID, err)
		}
		if i == 0 && strings.TrimSpace(res) == "cfg" {
			hasConfig = true
		}
	}

	// 2. 全机并行运行目标发布的 config check，确保历史配置与当前环境变量兼容。
	fmt.Fprintf(out, "回退前校验 %d 台主机配置（#%d %s）...\n", len(t.Hosts), targetManifest.ReleaseID, targetManifest.Version)
	if err := configCheckAll(ctx, r, t, releaseDir, releaseBin); err != nil {
		return nil, err
	}

	// 3. 按波次滚动回退并记录历史。
	waves := t.RolloutWaves()
	histEntry := HistoryEntry{
		ReleaseID: targetManifest.ReleaseID,
		Version:   targetManifest.Version,
		Kind:      ReleaseKindRollback,
		At:        time.Now().UTC(),
		By:        operator,
	}
	results, _, err := rolloutWaves(ctx, r, t, waves, targetManifest.ReleaseID, hasConfig, histEntry, out, errOut)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "已回退到 #%d (%s)。注意：数据库迁移不会自动撤销。\n", targetManifest.ReleaseID, targetManifest.Version)
	return &RollbackReport{
		App:       t.App,
		Env:       t.EnvName,
		ReleaseID: targetManifest.ReleaseID,
		Version:   targetManifest.Version,
		Waves:     waves,
		Hosts:     results,
	}, nil
}

// resolveRollbackTarget 按用户输入或 history.jsonl 确定回退目标的 Manifest。
func resolveRollbackTarget(ctx context.Context, r Runner, t Target, primary Host, ref string) (Manifest, error) {
	manifests, curID, err := readHostReleases(ctx, r, t, primary)
	if err != nil {
		return Manifest{}, err
	}
	byID := make(map[int]Manifest, len(manifests))
	for _, m := range manifests {
		byID[m.ReleaseID] = m
	}

	// 1. 显式指定发布序号（如 41 / #41 / 0041）。
	if ref != "" {
		if id, ok := ParseReleaseID(ref); ok {
			m, exists := byID[id]
			if !exists {
				return Manifest{}, fmt.Errorf("deploy: release #%d not found", id)
			}
			return m, nil
		}
		// 2. 显式指定构建版本/Git Tag（如 v1.1.0）：取留存中最新匹配的一条。
		for _, m := range manifests {
			if m.Version == ref {
				return m, nil
			}
		}
		return Manifest{}, fmt.Errorf("deploy: no retained release matches version %q", ref)
	}

	// 3. 缺省目标：读取 history.jsonl，跳过连续指向 curID 的记录，找到之前最近一次激活的仍存在的不同发布。
	histRaw, err := RunCapture(ctx, r, primary, fmt.Sprintf("cat %s 2>/dev/null || true", shellQuote(path.Join(t.Dir, "history.jsonl"))))
	if err == nil {
		var entries []HistoryEntry
		for line := range strings.Lines(histRaw) {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var e HistoryEntry
			if json.Unmarshal([]byte(line), &e) == nil && e.ReleaseID > 0 {
				entries = append(entries, e)
			}
		}
		for i := len(entries) - 1; i >= 0; i-- {
			id := entries[i].ReleaseID
			if id == curID {
				continue
			}
			if m, exists := byID[id]; exists {
				return m, nil
			}
		}
	}

	// 4. 历史文件缺失或无可用记录时，退化为留存中序号最大的非当前发布。
	for _, m := range manifests {
		if m.ReleaseID != curID {
			return m, nil
		}
	}
	return Manifest{}, fmt.Errorf("deploy: no previous release available to roll back (current: #%d)", curID)
}

// StatusReport 是集群状态查询的结构化结果，供 CLI 表格输出、--format json 和 MCP tenon_deploy_status 共用。
type StatusReport struct {
	App   string       `json:"app"`   // 应用名
	Env   string       `json:"env"`   // 环境名
	Hosts []HostStatus `json:"hosts"` // 各主机状态
}

// HostStatus 是单台主机的发布版本与服务状态。
type HostStatus struct {
	Addr         string       `json:"addr"`                    // 主机地址
	Roles        []string     `json:"roles"`                   // 承担的角色
	Parts        []string     `json:"parts"`                   // 期望的 parts
	ReleaseID    int          `json:"release_id,omitempty"`    // 当前发布序号
	Version      string       `json:"version,omitempty"`       // 当前构建版本（如 v1.2.0）
	Commit       string       `json:"commit,omitempty"`        // 当前提交号
	ConfigSHA256 string       `json:"config_sha256,omitempty"` // 当前配置内容摘要
	Release      string       `json:"release"`                 // 综合摘要（如 #42 (v1.2.0)，未部署时为 none）
	Units        []UnitStatus `json:"units"`                   // 各 systemd unit 状态
}

// UnitStatus 是单个 systemd unit 的状态摘要。
type UnitStatus struct {
	Unit        string `json:"unit"`                  // unit 名，例如 blog@http.service
	ActiveState string `json:"active_state"`          // active / inactive / failed
	SubState    string `json:"sub_state"`             // running / dead / failed
	StatusText  string `json:"status_text,omitempty"` // systemd StatusText（升级失败时包含原因）
	Since       string `json:"since,omitempty"`       // ActiveEnterTimestamp
}

// CollectStatus 并发查询筛选后各主机的当前发布清单与 systemd unit 状态。
func CollectStatus(ctx context.Context, r Runner, t Target, roleFilter, hostFilter string) (*StatusReport, error) {
	hosts, err := t.FilterHosts(roleFilter, hostFilter)
	if err != nil {
		return nil, err
	}
	manifestPath := path.Join(t.Dir, "current", "manifest.json")
	results := make([]HostStatus, len(hosts))
	g, gctx := errgroup.WithContext(ctx)
	for i, h := range hosts {
		g.Go(func() error {
			var script strings.Builder
			fmt.Fprintf(&script, "echo \"---manifest\"\nif test -f %s; then cat %s; echo \"\"; fi\n",
				shellQuote(manifestPath), shellQuote(manifestPath))
			for _, part := range h.Parts {
				unit := UnitName(t.App, part)
				fmt.Fprintf(&script, "echo \"---unit=%s\"\nsystemctl show -p ActiveState -p SubState -p StatusText -p ActiveEnterTimestamp %s\n",
					unit, shellQuote(unit))
			}
			raw, err := RunCapture(gctx, r, h, script.String())
			if err != nil {
				return fmt.Errorf("[%s] 查询状态失败: %w", h.Addr, err)
			}
			results[i] = parseHostStatus(h, raw)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return &StatusReport{App: t.App, Env: t.EnvName, Hosts: results}, nil
}

// parseHostStatus 解析单台主机状态查询脚本的输出。
func parseHostStatus(h Host, raw string) HostStatus {
	hs := HostStatus{
		Addr:    h.Addr,
		Roles:   h.Roles,
		Parts:   h.Parts,
		Release: "none",
		Units:   []UnitStatus{},
	}
	inManifest := false
	var manifestBuf strings.Builder
	var cur *UnitStatus
	flushManifest := func() {
		if !inManifest {
			return
		}
		inManifest = false
		text := strings.TrimSpace(manifestBuf.String())
		if text == "" {
			return
		}
		var m Manifest
		if json.Unmarshal([]byte(text), &m) == nil && m.ReleaseID > 0 {
			hs.ReleaseID = m.ReleaseID
			hs.Version = m.Version
			hs.Commit = m.Commit
			hs.ConfigSHA256 = m.ConfigSHA256
			hs.Release = fmt.Sprintf("#%d (%s)", m.ReleaseID, m.Version)
		}
	}
	for line := range strings.Lines(raw) {
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == "---manifest":
			inManifest = true
			cur = nil
		case strings.HasPrefix(trimmed, "---unit="):
			flushManifest()
			hs.Units = append(hs.Units, UnitStatus{Unit: strings.TrimPrefix(trimmed, "---unit=")})
			cur = &hs.Units[len(hs.Units)-1]
		case inManifest:
			manifestBuf.WriteString(line)
		case cur != nil:
			k, v, ok := strings.Cut(trimmed, "=")
			if !ok {
				continue
			}
			switch k {
			case "ActiveState":
				cur.ActiveState = v
			case "SubState":
				cur.SubState = v
			case "StatusText":
				cur.StatusText = v
			case "ActiveEnterTimestamp":
				cur.Since = v
			}
		}
	}
	flushManifest()
	return hs
}

// FormatStatus 将集群状态格式化为对齐的表格输出到 out。
func FormatStatus(out io.Writer, report *StatusReport) {
	fmt.Fprintf(out, "环境: %s (应用: %s)\n", report.Env, report.App)
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tROLES\tRELEASE\tVERSION\tCONFIG\tUNIT\tSTATE\tSTATUS")
	for _, h := range report.Hosts {
		roles := strings.Join(h.Roles, ",")
		relCol := "-"
		if h.ReleaseID > 0 {
			relCol = fmt.Sprintf("#%d", h.ReleaseID)
		}
		verCol := h.Version
		if verCol == "" {
			verCol = "none"
		}
		cfgCol := "-"
		if h.ConfigSHA256 != "" {
			cfgCol = ShortSHA(h.ConfigSHA256)
		}
		if len(h.Units) == 0 {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t-\t-\t-\n", h.Addr, roles, relCol, verCol, cfgCol)
			continue
		}
		for i, u := range h.Units {
			hostCol, roleCol, rCol, vCol, cCol := h.Addr, roles, relCol, verCol, cfgCol
			if i > 0 {
				hostCol, roleCol, rCol, vCol, cCol = "", "", "", "", ""
			}
			state := u.ActiveState
			if u.SubState != "" {
				state += " (" + u.SubState + ")"
			}
			note := u.StatusText
			if note == "" {
				note = u.Since
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", hostCol, roleCol, rCol, vCol, cCol, u.Unit, state, note)
		}
	}
	_ = tw.Flush()
}

// LogsOptions 控制 tenon deploy logs 与 MCP tenon_deploy_logs 的查询范围。
type LogsOptions struct {
	Role     string // 按角色筛选主机，留空表示全部角色
	Host     string // 指定主机，留空时并发聚合全部匹配主机的日志
	Part     string // 指定服务部分（如 http）；留空时匹配全部 <app>@*
	Lines    int    // 每台主机返回最近多少行，默认 100
	Since    string // journalctl --since 参数（例如 10m、1h、"2026-11-22 10:00:00"）
	Contains string // 仅保留包含该子串的日志行（例如 ERR 或 X-Request-ID）
	Follow   bool   // 持续跟踪输出（仅流式模式使用）
}

// LogsReport 是结构化日志查询结果，供 --format json 和 MCP tenon_deploy_logs 使用。
type LogsReport struct {
	App      string     `json:"app"`                // 应用名
	Env      string     `json:"env"`                // 环境名
	Role     string     `json:"role,omitempty"`     // 筛选的角色
	Contains string     `json:"contains,omitempty"` // 关键字过滤条件
	Hosts    []HostLogs `json:"hosts"`              // 各主机的日志行
}

// HostLogs 保存单台主机拉取并过滤后的日志行。
type HostLogs struct {
	Addr  string   `json:"addr"`  // 主机地址
	Lines []string `json:"lines"` // 过滤后的日志行
}

// buildJournalCmd 构造远端 journalctl 查询命令。
func buildJournalCmd(t Target, opts LogsOptions, follow bool) string {
	lines := opts.Lines
	if lines <= 0 {
		lines = 100
	}
	unit := t.App + "@*"
	if opts.Part != "" {
		unit = UnitName(t.App, opts.Part)
	}
	cmd := fmt.Sprintf("sudo journalctl -u %s -n %d --no-pager", shellQuote(unit), lines)
	if strings.TrimSpace(opts.Since) != "" {
		cmd += " --since " + shellQuote(strings.TrimSpace(opts.Since))
	}
	if follow {
		cmd += " -f"
	}
	return cmd
}

// CollectLogs 并发拉取匹配主机的 journald 日志（非跟踪模式），按 Contains 过滤并返回结构化结果。
func CollectLogs(ctx context.Context, r Runner, t Target, opts LogsOptions) (*LogsReport, error) {
	hosts, err := t.FilterHosts(opts.Role, opts.Host)
	if err != nil {
		return nil, err
	}
	cmd := buildJournalCmd(t, opts, false)
	items := make([]HostLogs, len(hosts))
	g, gctx := errgroup.WithContext(ctx)
	for i, h := range hosts {
		g.Go(func() error {
			raw, err := RunCapture(gctx, r, h, cmd)
			if err != nil {
				return fmt.Errorf("[%s] 拉取日志失败: %w", h.Addr, err)
			}
			var kept []string
			for line := range strings.Lines(raw) {
				line = strings.TrimRight(line, "\r\n")
				if opts.Contains == "" || strings.Contains(line, opts.Contains) {
					kept = append(kept, line)
				}
			}
			items[i] = HostLogs{Addr: h.Addr, Lines: kept}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return &LogsReport{
		App:      t.App,
		Env:      t.EnvName,
		Role:     opts.Role,
		Contains: opts.Contains,
		Hosts:    items,
	}, nil
}

// Logs 流式查看单台或多台主机的 journald 日志；匹配多台主机时并发汇流并在每行前加上 [host] 前缀。
func Logs(ctx context.Context, r Runner, t Target, opts LogsOptions, out, errOut io.Writer) error {
	hosts, err := t.FilterHosts(opts.Role, opts.Host)
	if err != nil {
		return err
	}
	cmd := buildJournalCmd(t, opts, opts.Follow)
	multi := len(hosts) > 1
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, h := range hosts {
		g.Go(func() error {
			prefix := ""
			if multi {
				prefix = "[" + h.Addr + "] "
			}
			pw := &prefixLineWriter{out: out, mu: &mu, prefix: prefix, contains: opts.Contains}
			err := r.Run(gctx, h, cmd, nil, pw, errOut)
			pw.flush()
			return err
		})
	}
	return g.Wait()
}

// prefixLineWriter 按行缓冲输出，为多机日志汇流添加主机前缀并按关键字过滤，保证并发写入不撕裂行。
type prefixLineWriter struct {
	out      io.Writer
	mu       *sync.Mutex
	prefix   string
	contains string
	buf      bytes.Buffer
}

// Write 接收流式输出并按换行逐行写入目标 writer。
func (w *prefixLineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := string(data[:idx])
		w.buf.Next(idx + 1)
		w.emit(strings.TrimRight(line, "\r"))
	}
	return len(p), nil
}

// flush 刷出末尾未以换行结尾的残留行。
func (w *prefixLineWriter) flush() {
	if w.buf.Len() == 0 {
		return
	}
	line := strings.TrimRight(w.buf.String(), "\r\n")
	w.buf.Reset()
	if line != "" {
		w.emit(line)
	}
}

// emit 在持有互斥锁的情况下输出过滤通过的单行日志。
func (w *prefixLineWriter) emit(line string) {
	if w.contains != "" && !strings.Contains(line, w.contains) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = fmt.Fprintf(w.out, "%s%s\n", w.prefix, line)
}

// RunRemote 在远端主机以真实服务用户、环境变量文件和工作目录运行一次性应用命令（例如 doctor、queue status）。
func RunRemote(ctx context.Context, r Runner, t Target, roleFilter, hostFilter string, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("deploy: command arguments are required")
	}
	hosts, err := t.FilterHosts(roleFilter, hostFilter)
	if err != nil {
		return err
	}
	h := hosts[0]
	if roleFilter == "" && hostFilter == "" {
		h = t.MigrateHost()
	}
	liveBin := path.Join(t.Dir, "bin", t.App)
	argv := append([]string{liveBin}, args...)
	return r.Run(ctx, h, systemdRunCommand(t, t.Dir, argv...), nil, out, errOut)
}

// EnvPushReport 是 tenon deploy env push 的结构化结果。
type EnvPushReport struct {
	App       string   `json:"app"`       // 应用名
	Env       string   `json:"env"`       // 环境名
	EnvFile   string   `json:"env_file"`  // 远端环境变量路径
	Restarted bool     `json:"restarted"` // 是否执行了服务重启
	Hosts     []string `json:"hosts"`     // 已更新的主机列表
}

// EnvPush 将本地环境变量文件通过标准输入安全写入目标主机的 env_file（权限 600，属主 root）；
// restart 为真时顺带重启目标主机的服务使新环境变量立即生效。
func EnvPush(ctx context.Context, r Runner, t Target, localFile, roleFilter, hostFilter string, restart bool, out, errOut io.Writer) (*EnvPushReport, error) {
	data, err := os.ReadFile(localFile)
	if err != nil {
		return nil, fmt.Errorf("deploy: read %s: %w", localFile, err)
	}
	hosts, err := t.FilterHosts(roleFilter, hostFilter)
	if err != nil {
		return nil, err
	}
	envDir := path.Dir(t.EnvFile)
	writeCmd := fmt.Sprintf("sudo install -d -m 750 -o root -g root %s && %s",
		shellQuote(envDir), installFromStdinCommand("600", t.EnvFile))
	report := &EnvPushReport{
		App:       t.App,
		Env:       t.EnvName,
		EnvFile:   t.EnvFile,
		Restarted: restart,
		Hosts:     make([]string, 0, len(hosts)),
	}
	for _, h := range hosts {
		if err := r.Run(ctx, h, writeCmd, bytes.NewReader(data), io.Discard, errOut); err != nil {
			return nil, fmt.Errorf("[%s] 写入 %s 失败: %w", h.Addr, t.EnvFile, err)
		}
		report.Hosts = append(report.Hosts, h.Addr)
		fmt.Fprintf(out, "[%s] 已更新 %s\n", h.Addr, t.EnvFile)
		if restart {
			for _, part := range h.Parts {
				unit := UnitName(t.App, part)
				if _, err := RunCapture(ctx, r, h, "sudo systemctl restart "+shellQuote(unit)); err != nil {
					return nil, fmt.Errorf("[%s] 重启 %s 失败: %w", h.Addr, unit, err)
				}
				fmt.Fprintf(out, "[%s] 已重启 %s\n", h.Addr, unit)
			}
		}
	}
	if !restart {
		fmt.Fprintln(out, "提示：热重载继承旧进程的环境变量；传入 --restart 或执行 systemctl restart 后生效。")
	}
	return report, nil
}

// EnvPull 读取远端主机的环境变量文件内容并输出到 out。
func EnvPull(ctx context.Context, r Runner, t Target, roleFilter, hostFilter string, out io.Writer) error {
	hosts, err := t.FilterHosts(roleFilter, hostFilter)
	if err != nil {
		return err
	}
	h := hosts[0]
	if roleFilter == "" && hostFilter == "" {
		h = t.MigrateHost()
	}
	content, err := RunCapture(ctx, r, h, "sudo cat "+shellQuote(t.EnvFile))
	if err != nil {
		return fmt.Errorf("[%s] 读取 %s 失败: %w", h.Addr, t.EnvFile, err)
	}
	_, err = io.WriteString(out, content)
	return err
}

// MaintenanceReport 是维护模式开关的结构化结果。
type MaintenanceReport struct {
	App   string   `json:"app"`   // 应用名
	Env   string   `json:"env"`   // 环境名
	On    bool     `json:"on"`    // 是否开启维护模式
	File  string   `json:"file"`  // 维护标记文件路径
	Hosts []string `json:"hosts"` // 已操作的主机列表
}

// Maintenance 创建或删除 maintenance_file，控制反向代理 503 维护页；
// 未指定筛选条件时默认仅操作运行入口服务（all/http）的主机。
func Maintenance(ctx context.Context, r Runner, t Target, roleFilter, hostFilter string, on bool, out io.Writer) (*MaintenanceReport, error) {
	if t.MaintenanceFile == "" {
		return nil, fmt.Errorf("deploy: maintenance_file is not configured in environment %q", t.EnvName)
	}
	var hosts []Host
	if roleFilter != "" || hostFilter != "" {
		var err error
		hosts, err = t.FilterHosts(roleFilter, hostFilter)
		if err != nil {
			return nil, err
		}
	} else {
		hosts = t.HTTPHosts()
	}
	report := &MaintenanceReport{
		App:   t.App,
		Env:   t.EnvName,
		On:    on,
		File:  t.MaintenanceFile,
		Hosts: make([]string, 0, len(hosts)),
	}
	for _, h := range hosts {
		var cmd string
		action := "关闭维护模式"
		if on {
			action = "开启维护模式"
			cmd = fmt.Sprintf("sudo install -d -m 755 %s && sudo touch %s",
				shellQuote(path.Dir(t.MaintenanceFile)), shellQuote(t.MaintenanceFile))
		} else {
			cmd = "sudo rm -f " + shellQuote(t.MaintenanceFile)
		}
		if _, err := RunCapture(ctx, r, h, cmd); err != nil {
			return nil, fmt.Errorf("[%s] %s失败: %w", h.Addr, action, err)
		}
		report.Hosts = append(report.Hosts, h.Addr)
		fmt.Fprintf(out, "[%s] 已%s（%s）\n", h.Addr, action, t.MaintenanceFile)
	}
	return report, nil
}
