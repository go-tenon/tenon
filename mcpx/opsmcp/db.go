package opsmcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/mcpx"
)

const (
	// docOpsMCP 是 opsmcp 文档的锚点路径，用于 doctor 提示链接。
	docOpsMCP = "docs/ai/mcp.md#ops-mcp"
	// readOnlyRoleFixSQL 是创建 PostgreSQL 通用只读角色的两行标准 SQL。
	readOnlyRoleFixSQL = "CREATE ROLE app_ro LOGIN PASSWORD '<password>';\nGRANT pg_read_all_data TO app_ro;"
)

// allowedSQLStarts 是 ops_db_query 放行的只读语句首关键字。
var allowedSQLStarts = map[string]bool{
	"SELECT":  true,
	"WITH":    true,
	"SHOW":    true,
	"EXPLAIN": true,
	"VALUES":  true,
	"TABLE":   true,
}

// dbRoleAudit 是检查 PostgreSQL 当前连接角色权限的结果。
type dbRoleAudit struct {
	CurrentUser       string
	Superuser         bool
	DefaultReadOnly   bool
	HasTableWritePriv bool
}

// ReadOnly 报告该连接角色是否满足数据库内核级只读：非超级用户，且（会话默认只读或对用户表无写权限）。
func (a dbRoleAudit) ReadOnly() bool {
	if a.Superuser {
		return false
	}
	return !a.HasTableWritePriv
}

// Doctor 返回检查诊断数据库连接的一组自检，组名为 ops_db：
//
//   - connection：连接可用；
//   - read_only：检查当前 PostgreSQL 角色是否仅有只读权限；若使用了超级用户或具备写权限的账号，
//     返回警告并给出两行创建 pg_read_all_data 只读角色的 SQL，不阻塞服务启动。
func Doctor(db *bun.DB) doctor.Group {
	return doctor.Group{Name: "ops_db", Checks: []doctor.Check{
		{Name: "connection", Run: func(ctx context.Context) error { return db.PingContext(ctx) }},
		{Name: "read_only", Run: func(ctx context.Context) error {
			audit, err := inspectDBRole(ctx, db)
			if err != nil {
				return err
			}
			if audit.ReadOnly() {
				return nil
			}
			reason := fmt.Sprintf("ops_db role %q has write privileges on user tables", audit.CurrentUser)
			if audit.Superuser {
				reason = fmt.Sprintf("ops_db role %q is a PostgreSQL superuser", audit.CurrentUser)
			}
			return doctor.Warn(
				reason+"; create a dedicated read-only role for ops_db_query",
				readOnlyRoleFixSQL,
			).See(docOpsMCP)
		}},
	}}
}

// inspectDBRole 查询当前数据库连接的 PostgreSQL 角色属性及对用户表的写权限。
func inspectDBRole(ctx context.Context, db *bun.DB) (dbRoleAudit, error) {
	var row struct {
		CurrentUser       string `bun:"current_user"`
		Superuser         bool   `bun:"rolsuper"`
		DefaultReadOnly   string `bun:"default_ro"`
		HasTableWritePriv bool   `bun:"has_write"`
	}
	err := db.NewRaw(`
SELECT
    current_user,
    r.rolsuper,
    current_setting('default_transaction_read_only') AS default_ro,
    EXISTS (
        SELECT 1
        FROM information_schema.tables t
        WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema')
          AND t.table_type = 'BASE TABLE'
          AND (
              has_table_privilege(current_user, format('%I.%I', t.table_schema, t.table_name), 'INSERT')
              OR has_table_privilege(current_user, format('%I.%I', t.table_schema, t.table_name), 'UPDATE')
              OR has_table_privilege(current_user, format('%I.%I', t.table_schema, t.table_name), 'DELETE')
              OR has_table_privilege(current_user, format('%I.%I', t.table_schema, t.table_name), 'TRUNCATE')
          )
    ) AS has_write
FROM pg_roles r
WHERE r.rolname = current_user`).Scan(ctx, &row)
	if err != nil {
		return dbRoleAudit{}, err
	}
	return dbRoleAudit{
		CurrentUser:       row.CurrentUser,
		Superuser:         row.Superuser,
		DefaultReadOnly:   row.DefaultReadOnly == "on",
		HasTableWritePriv: row.HasTableWritePriv,
	}, nil
}

// dbTablesArgs 是 ops_db_tables 的参数。
type dbTablesArgs struct {
	Schema string `json:"schema,omitempty" doc:"可选：按 schema 筛选，例如 public；留空返回全部非系统 schema"`
	Table  string `json:"table,omitempty" doc:"可选：指定表名（如 articles 或 public.articles）时同时返回该表的列定义与索引"`
}

// dbTableSummary 是一张表的元数据摘要。
type dbTableSummary struct {
	Schema        string `json:"schema"`
	Name          string `json:"name"`
	EstimatedRows int64  `json:"estimated_rows"`
	TotalSize     string `json:"total_size"`
	Comment       string `json:"comment,omitempty"`
}

// dbColumnInfo 是表的单列定义。
type dbColumnInfo struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// dbIndexInfo 是表的单个索引定义。
type dbIndexInfo struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// dbTablesResult 是 ops_db_tables 的返回值。
type dbTablesResult struct {
	Tables  []dbTableSummary `json:"tables"`
	Columns []dbColumnInfo   `json:"columns,omitempty"`
	Indexes []dbIndexInfo    `json:"indexes,omitempty"`
}

// dbQueryArgs 是 ops_db_query 的参数。
type dbQueryArgs struct {
	SQL   string `json:"sql" doc:"要执行的单条只读 PostgreSQL 查询（仅允许 SELECT/WITH/EXPLAIN/SHOW/VALUES/TABLE，严禁多语句）"`
	Limit int    `json:"limit,omitempty" doc:"最多返回的行数，默认 50，上限 200"`
}

// dbQueryResult 是 ops_db_query 的返回值。
type dbQueryResult struct {
	Columns         []string `json:"columns"`
	Rows            [][]any  `json:"rows"`
	RowCount        int      `json:"row_count"`
	Truncated       bool     `json:"truncated"`
	RedactedColumns []string `json:"redacted_columns,omitempty"`
	DurationMS      int64    `json:"duration_ms"`
}

// registerDB 登记只读数据库表结构与只读 SQL 查询工具。
func (k *kit) registerDB(srv *mcpx.Server) {
	mcpx.Register(srv, k.tool(
		"ops_db_tables",
		"查看数据库表结构",
		"从 PostgreSQL 系统目录列出用户表、估算行数和磁盘占用（不扫全表）。传入 table 时同时返回该表的全部列定义、注释与索引，便于编写精确的 ops_db_query。",
	), k.handleDBTables)

	mcpx.Register(srv, k.tool(
		"ops_db_query",
		"执行只读 SQL 查询",
		"在强制只读事务（READ ONLY + 语句超时 + 锁超时 + 结束回滚）中执行单条只读 PostgreSQL 查询，自动截断行数与长文本并对常见敏感列脱敏。",
	), k.handleDBQuery)
}

// handleDBTables 读取表清单及指定表的列与索引元数据。
func (k *kit) handleDBTables(ctx context.Context, in dbTablesArgs) (dbTablesResult, error) {
	k.audit(ctx, "ops_db_tables", "schema", in.Schema, "table", in.Table)
	schemaFilter := strings.TrimSpace(in.Schema)
	tableFilter := strings.TrimSpace(in.Table)
	if dotSchema, dotTable, ok := strings.Cut(tableFilter, "."); ok {
		if schemaFilter == "" {
			schemaFilter = strings.TrimSpace(dotSchema)
		}
		tableFilter = strings.TrimSpace(dotTable)
	}

	var rows []struct {
		Schema        string         `bun:"schema_name"`
		Name          string         `bun:"table_name"`
		EstimatedRows int64          `bun:"estimated_rows"`
		TotalSize     string         `bun:"total_size"`
		Comment       sql.NullString `bun:"table_comment"`
	}
	q := k.db.NewRaw(`
SELECT
    n.nspname AS schema_name,
    c.relname AS table_name,
    GREATEST(c.reltuples, 0)::bigint AS estimated_rows,
    pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size,
    obj_description(c.oid, 'pg_class') AS table_comment
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
  AND (? = '' OR n.nspname = ?)
  AND (? = '' OR c.relname = ?)
ORDER BY n.nspname, c.relname`,
		schemaFilter, schemaFilter, tableFilter, tableFilter)
	if err := q.Scan(ctx, &rows); err != nil {
		return dbTablesResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}

	res := dbTablesResult{Tables: make([]dbTableSummary, 0, len(rows))}
	for _, r := range rows {
		res.Tables = append(res.Tables, dbTableSummary{
			Schema:        r.Schema,
			Name:          r.Name,
			EstimatedRows: r.EstimatedRows,
			TotalSize:     r.TotalSize,
			Comment:       r.Comment.String,
		})
	}
	if tableFilter == "" || len(res.Tables) == 0 {
		return res, nil
	}
	targetSchema := res.Tables[0].Schema
	targetTable := res.Tables[0].Name

	var colRows []struct {
		Name     string         `bun:"column_name"`
		DataType string         `bun:"data_type"`
		Nullable string         `bun:"is_nullable"`
		Default  sql.NullString `bun:"column_default"`
		Comment  sql.NullString `bun:"column_comment"`
	}
	err := k.db.NewRaw(`
SELECT
    c.column_name,
    c.udt_name AS data_type,
    c.is_nullable,
    c.column_default,
    col_description(cls.oid, c.ordinal_position) AS column_comment
FROM information_schema.columns c
JOIN pg_namespace ns ON ns.nspname = c.table_schema
JOIN pg_class cls ON cls.relnamespace = ns.oid AND cls.relname = c.table_name
WHERE c.table_schema = ? AND c.table_name = ?
ORDER BY c.ordinal_position`, targetSchema, targetTable).Scan(ctx, &colRows)
	if err != nil {
		return dbTablesResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	res.Columns = make([]dbColumnInfo, 0, len(colRows))
	for _, c := range colRows {
		res.Columns = append(res.Columns, dbColumnInfo{
			Name:     c.Name,
			DataType: c.DataType,
			Nullable: c.Nullable == "YES",
			Default:  c.Default.String,
			Comment:  c.Comment.String,
		})
	}

	var idxRows []struct {
		Name       string `bun:"indexname"`
		Definition string `bun:"indexdef"`
	}
	err = k.db.NewRaw(`
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = ? AND tablename = ?
ORDER BY indexname`, targetSchema, targetTable).Scan(ctx, &idxRows)
	if err != nil {
		return dbTablesResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	res.Indexes = make([]dbIndexInfo, 0, len(idxRows))
	for _, idx := range idxRows {
		res.Indexes = append(res.Indexes, dbIndexInfo{Name: idx.Name, Definition: idx.Definition})
	}
	return res, nil
}

// handleDBQuery 在强制只读事务与超时保护下执行单条只读 SQL。
func (k *kit) handleDBQuery(ctx context.Context, in dbQueryArgs) (dbQueryResult, error) {
	query, err := validateReadOnlySQL(in.SQL)
	if err != nil {
		return dbQueryResult{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = k.maxRows
	}
	if limit > hardMaxRows {
		limit = hardMaxRows
	}
	if k.requireReadOnlyRole {
		audit, err := inspectDBRole(ctx, k.db)
		if err != nil {
			return dbQueryResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		if !audit.ReadOnly() {
			return dbQueryResult{}, ErrQueryRejected.New(errorx.Public("reason",
				fmt.Sprintf("database role %q has write privileges; configure a read-only role (pg_read_all_data)", audit.CurrentUser)))
		}
	}

	start := time.Now()
	qctx, cancel := context.WithTimeout(ctx, k.queryTimeout+time.Second)
	defer cancel()

	tx, err := k.db.BeginTx(qctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return dbQueryResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	defer func() { _ = tx.Rollback() }()

	// 在事务本地强制设置语句超时、锁超时与只读属性，事务回滚后不污染连接池。
	stmtMS := max(k.queryTimeout.Milliseconds(), 1)
	lockMS := max(k.lockTimeout.Milliseconds(), 1)
	if _, err := tx.ExecContext(qctx, fmt.Sprintf(
		"SET LOCAL transaction_read_only = on; SET LOCAL statement_timeout = '%dms'; SET LOCAL lock_timeout = '%dms'",
		stmtMS, lockMS,
	)); err != nil {
		return dbQueryResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}

	rows, err := tx.QueryContext(qctx, query)
	if err != nil {
		return dbQueryResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return dbQueryResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}
	redactedMask := make([]bool, len(cols))
	var redactedCols []string
	for i, name := range cols {
		if matchesAny(name, k.redactColumns) {
			redactedMask[i] = true
			redactedCols = append(redactedCols, name)
		}
	}

	resultRows := make([][]any, 0, min(limit, 32))
	truncated := false
	rawVals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range rawVals {
		ptrs[i] = &rawVals[i]
	}
	for rows.Next() {
		if len(resultRows) >= limit {
			truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return dbQueryResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
		}
		record := make([]any, len(cols))
		for i, v := range rawVals {
			if redactedMask[i] {
				if v == nil {
					record[i] = nil
				} else {
					record[i] = redactedValue
				}
				continue
			}
			record[i] = formatSQLCell(v, k.maxCellLen)
		}
		resultRows = append(resultRows, record)
	}
	if err := rows.Err(); err != nil {
		return dbQueryResult{}, ErrQueryFailed.Wrap(err, errorx.Public("reason", err.Error()))
	}

	elapsed := time.Since(start).Milliseconds()
	k.audit(ctx, "ops_db_query", "sql", truncateString(query, 300), "rows", len(resultRows), "truncated", truncated, "duration_ms", elapsed)
	return dbQueryResult{
		Columns:         cols,
		Rows:            resultRows,
		RowCount:        len(resultRows),
		Truncated:       truncated,
		RedactedColumns: redactedCols,
		DurationMS:      elapsed,
	}, nil
}

// validateReadOnlySQL 检查 SQL 为非空单条语句且首关键字属于只读查询集合，并返回去掉末尾分号后的语句。
func validateReadOnlySQL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrQueryRejected.New(errorx.Public("reason", "SQL 不能为空"))
	}

	var firstWord string
	stmtEnd := -1
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case isASCIIWhitespace(ch):
			i++
		case ch == '-' && i+1 < len(s) && s[i+1] == '-':
			i += 2
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(s) && s[i+1] == '*':
			next, ok := skipBlockComment(s, i)
			if !ok {
				return "", ErrQueryRejected.New(errorx.Public("reason", "SQL 块注释未闭合"))
			}
			i = next
		case ch == '\'':
			if stmtEnd >= 0 {
				return "", ErrQueryRejected.New(errorx.Public("reason", "只允许执行单条 SQL 语句"))
			}
			next, ok := skipQuoted(s, i, '\'')
			if !ok {
				return "", ErrQueryRejected.New(errorx.Public("reason", "SQL 字符串字面量未闭合"))
			}
			i = next
		case ch == '"':
			if stmtEnd >= 0 {
				return "", ErrQueryRejected.New(errorx.Public("reason", "只允许执行单条 SQL 语句"))
			}
			next, ok := skipQuoted(s, i, '"')
			if !ok {
				return "", ErrQueryRejected.New(errorx.Public("reason", "SQL 引号标识符未闭合"))
			}
			i = next
		case ch == '$':
			if tagEnd, ok := matchDollarQuoteTag(s, i); ok {
				if stmtEnd >= 0 {
					return "", ErrQueryRejected.New(errorx.Public("reason", "只允许执行单条 SQL 语句"))
				}
				tag := s[i:tagEnd]
				closeIdx := strings.Index(s[tagEnd:], tag)
				if closeIdx < 0 {
					return "", ErrQueryRejected.New(errorx.Public("reason", "SQL 美元引号字符串未闭合"))
				}
				i = tagEnd + closeIdx + len(tag)
				continue
			}
			if stmtEnd >= 0 {
				return "", ErrQueryRejected.New(errorx.Public("reason", "只允许执行单条 SQL 语句"))
			}
			i++
		case ch == ';':
			if stmtEnd < 0 {
				stmtEnd = i
			}
			i++
		default:
			if stmtEnd >= 0 {
				return "", ErrQueryRejected.New(errorx.Public("reason", "只允许执行单条 SQL 语句"))
			}
			if firstWord == "" {
				start := i
				for i < len(s) && (unicode.IsLetter(rune(s[i])) || s[i] == '_') {
					i++
				}
				if i == start {
					return "", ErrQueryRejected.New(errorx.Public("reason", "只允许执行 SELECT/WITH/EXPLAIN/SHOW/VALUES/TABLE 只读查询"))
				}
				firstWord = strings.ToUpper(s[start:i])
				continue
			}
			i++
		}
	}
	if firstWord == "" {
		return "", ErrQueryRejected.New(errorx.Public("reason", "SQL 不能为空"))
	}
	if !allowedSQLStarts[firstWord] {
		return "", ErrQueryRejected.New(errorx.Public("reason",
			fmt.Sprintf("不支持 %s 语句，只允许 SELECT/WITH/EXPLAIN/SHOW/VALUES/TABLE 只读查询", firstWord)))
	}
	if stmtEnd >= 0 {
		s = strings.TrimSpace(s[:stmtEnd])
	}
	return s, nil
}

// isASCIIWhitespace 报告 b 是否为空白字符。
func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

// skipBlockComment 跳过支持嵌套的 PostgreSQL /* ... */ 注释，返回注释结束后的下标。
func skipBlockComment(s string, start int) (int, bool) {
	depth := 1
	for i := start + 2; i < len(s); {
		if s[i] == '/' && i+1 < len(s) && s[i+1] == '*' {
			depth++
			i += 2
			continue
		}
		if s[i] == '*' && i+1 < len(s) && s[i+1] == '/' {
			depth--
			i += 2
			if depth == 0 {
				return i, true
			}
			continue
		}
		i++
	}
	return 0, false
}

// skipQuoted 跳过以 quote 开头并支持双写转义的字符串或标识符。
func skipQuoted(s string, start int, quote byte) (int, bool) {
	for i := start + 1; i < len(s); i++ {
		if s[i] == quote {
			if i+1 < len(s) && s[i+1] == quote {
				i++
				continue
			}
			return i + 1, true
		}
	}
	return 0, false
}

// matchDollarQuoteTag 匹配 PostgreSQL 美元引号起始标签（$$ 或 $tag$），返回标签结束下标。
func matchDollarQuoteTag(s string, start int) (int, bool) {
	if start >= len(s) || s[start] != '$' {
		return 0, false
	}
	i := start + 1
	if i < len(s) && s[i] == '$' {
		return i + 1, true
	}
	if i >= len(s) || !(s[i] == '_' || (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
		return 0, false
	}
	for i++; i < len(s); i++ {
		c := s[i]
		if c == '$' {
			return i + 1, true
		}
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return 0, false
		}
	}
	return 0, false
}

// formatSQLCell 将扫描出的 SQL 值格式化为紧凑可序列化的 JSON 值并截断超长文本。
func formatSQLCell(v any, maxLen int) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool, int64, int32, int, float64, float32:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case []byte:
		if !utf8.Valid(x) {
			return fmt.Sprintf("<binary %d bytes>", len(x))
		}
		str := string(x)
		if len(str) <= maxLen && json.Valid(x) && (strings.HasPrefix(strings.TrimSpace(str), "{") || strings.HasPrefix(strings.TrimSpace(str), "[")) {
			var parsed any
			if json.Unmarshal(x, &parsed) == nil {
				return parsed
			}
		}
		return truncateString(str, maxLen)
	case string:
		return truncateString(x, maxLen)
	default:
		return truncateString(fmt.Sprint(x), maxLen)
	}
}
