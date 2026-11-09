// Package export 把表格数据导出为 CSV 或 Excel（XLSX）文件。
//
// 用列定义描述表格，用迭代器提供行，数据边读边写，不需要一次全部加载到内存：
//
//	var notesTable = export.Table[note.Note]{
//		Name: "笔记",
//		Columns: []export.Column[note.Note]{
//			{Header: "内容", Value: func(n note.Note) any { return n.Text }},
//			{Header: "创建时间", Value: func(n note.Note) any { return n.CreatedAt }},
//		},
//	}
//
//	func (a *ExportNotes) serve(w http.ResponseWriter, r *http.Request) error {
//		format, err := export.ParseFormat(r.URL.Query().Get("format"))
//		if err != nil {
//			return err // ErrUnknownFormat，已登记为 422
//		}
//		q := db.NewSelect().Model((*note.Note)(nil)).Where("user_id = ?", uid).Order("id")
//		return export.Serve(w, format, notesTable, export.QueryRows[note.Note](r.Context(), q))
//	}
//
// 单元格的值：
//
//	nil                      空
//	time.Time                本地时间 "2006-01-02 15:04:05"，零值为空
//	整数、浮点数、bool        XLSX 中保持数值/布尔类型
//	fmt.Stringer、其他类型    fmt.Sprint 的结果
//
// CSV 以 UTF-8 BOM 开头，Excel 打开不会乱码；以 = + - @ 开头的文本前加 '，防止被 Excel 当作公式执行。
package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"
	"github.com/xuri/excelize/v2"
)

// Format 是导出文件格式。
type Format string

const (
	CSV  Format = "csv"
	XLSX Format = "xlsx"
)

// ParseFormat 解析 "csv" 或 "xlsx"（不区分大小写），空串为 CSV。
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "", "csv":
		return CSV, nil
	case "xlsx", "excel":
		return XLSX, nil
	}
	return "", ErrUnknownFormat
}

// ContentType 返回格式对应的 MIME 类型。
func (f Format) ContentType() string {
	if f == XLSX {
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "text/csv; charset=utf-8"
}

// Column 是一列：表头与取值函数。
type Column[T any] struct {
	Header string
	Value  func(row T) any
}

// Table 描述要导出的表格。
type Table[T any] struct {
	// Name 用作 XLSX 的工作表名与 Serve 的文件名（再加上日期与扩展名）。
	Name    string
	Columns []Column[T]
}

// SliceRows 返回遍历 rows 的迭代器。
func SliceRows[T any](rows []T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, r := range rows {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// QueryRows 执行 bun 查询并逐行扫描为 T，用游标读取，不会把结果全部加载到内存。
// 查询或扫描出错时迭代器产出该错误并停止。
func QueryRows[T any](ctx context.Context, q *bun.SelectQuery) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		rows, err := q.Rows(ctx)
		if err != nil {
			yield(zero, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var row T
			if err := q.DB().ScanRow(ctx, rows, &row); err != nil {
				yield(zero, err)
				return
			}
			if !yield(row, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
			yield(zero, err)
		}
	}
}

// Write 把表头与 rows 按 format 写入 w。
func Write[T any](w io.Writer, format Format, t Table[T], rows iter.Seq2[T, error]) error {
	switch format {
	case CSV:
		return writeCSV(w, t, rows)
	case XLSX:
		return writeXLSX(w, t, rows)
	}
	return errors.Newf("export: unknown format %q", format)
}

// Serve 以附件形式把表格写入 HTTP 响应，文件名为 "<Name>-<日期>.<扩展名>"。
//
// 响应在写出第一行后才会出错（例如数据库查询中途失败）时，客户端会收到一个不完整的文件，
// 错误仍会返回给调用方记录日志。
func Serve[T any](w http.ResponseWriter, format Format, t Table[T], rows iter.Seq2[T, error]) error {
	name := t.Name
	if name == "" {
		name = "export"
	}
	filename := fmt.Sprintf("%s-%s.%s", name, time.Now().Format("20060102"), format)
	w.Header().Set("Content-Type", format.ContentType())
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "no-store")
	return Write(w, format, t, rows)
}

func writeCSV[T any](w io.Writer, t Table[T], rows iter.Seq2[T, error]) error {
	if _, err := io.WriteString(w, "\xef\xbb\xbf"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	record := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		record[i] = csvSafe(c.Header)
	}
	if err := cw.Write(record); err != nil {
		return err
	}
	for row, err := range rows {
		if err != nil {
			cw.Flush()
			return errors.Wrap(err, "export")
		}
		for i, c := range t.Columns {
			record[i] = csvSafe(text(c.Value(row)))
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeXLSX[T any](w io.Writer, t Table[T], rows iter.Seq2[T, error]) error {
	f := excelize.NewFile()
	defer f.Close()
	sheet := sheetName(t.Name)
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return errors.Wrap(err, "export")
	}
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return errors.Wrap(err, "export")
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return errors.Wrap(err, "export")
	}

	if err := sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return errors.Wrap(err, "export")
	}
	cells := make([]any, len(t.Columns))
	for i, c := range t.Columns {
		cells[i] = excelize.Cell{StyleID: bold, Value: c.Header}
	}
	if err := sw.SetRow("A1", cells); err != nil {
		return errors.Wrap(err, "export")
	}
	n := 1
	for row, err := range rows {
		if err != nil {
			return errors.Wrap(err, "export")
		}
		n++
		for i, c := range t.Columns {
			cells[i] = cellValue(c.Value(row))
		}
		cell, _ := excelize.CoordinatesToCellName(1, n)
		if err := sw.SetRow(cell, cells); err != nil {
			return errors.Wrap(err, "export")
		}
	}
	if err := sw.Flush(); err != nil {
		return errors.Wrap(err, "export")
	}
	if _, err := f.WriteTo(w); err != nil {
		return errors.Wrap(err, "export")
	}
	return nil
}

const timeLayout = "2006-01-02 15:04:05"

// text 把单元格的值转换为 CSV 中的文本。
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case time.Time:
		if x.IsZero() {
			return ""
		}
		return x.Local().Format(timeLayout)
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

// cellValue 把单元格的值转换为 XLSX 可以写入的值：数值与布尔保持类型，其余转为文本。
func cellValue(v any) any {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
		return v
	}
	return text(v)
}

// csvSafe 在以公式字符开头的文本前加 '，防止 CSV 被 Excel 打开时执行公式（CSV injection）。
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// sheetName 返回合法的工作表名：去掉 Excel 不允许的字符，最长 31 个字符。
func sheetName(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:\/?*[]`, r) {
			return -1
		}
		return r
	}, name)
	if r := []rune(name); len(r) > 31 {
		name = string(r[:31])
	}
	if name == "" {
		return "Sheet1"
	}
	return name
}
