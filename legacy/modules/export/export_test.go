package export_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/xuri/excelize/v2"

	"github.com/ryan053857/go-kit/modules/export"
)

type row struct {
	Name  string
	Count int
	At    time.Time
}

var table = export.Table[row]{
	Name: "测试/表",
	Columns: []export.Column[row]{
		{Header: "名称", Value: func(r row) any { return r.Name }},
		{Header: "数量", Value: func(r row) any { return r.Count }},
		{Header: "时间", Value: func(r row) any { return r.At }},
	},
}

var rows = []row{
	{Name: "普通", Count: 3, At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)},
	{Name: "=HYPERLINK(\"x\")", Count: -1},
}

func TestCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := export.Write(&buf, export.CSV, table, export.SliceRows(rows)); err != nil {
		t.Fatal(err)
	}
	want := "\xef\xbb\xbf名称,数量,时间\n普通,3,2026-01-02 03:04:05\n\"'=HYPERLINK(\"\"x\"\")\",'-1,\n"
	if buf.String() != want {
		t.Errorf("csv =\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestXLSX(t *testing.T) {
	var buf bytes.Buffer
	if err := export.Write(&buf, export.XLSX, table, export.SliceRows(rows)); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.GetRows("测试表")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0][0] != "名称" || got[1][1] != "3" || got[2][0] != `=HYPERLINK("x")` || got[1][2] != "2026-01-02 03:04:05" {
		t.Errorf("rows = %q", got)
	}
}

func TestErrorsAndFormat(t *testing.T) {
	failing := func(yield func(row, error) bool) { yield(row{}, errors.New("db down")) }
	if err := export.Write(&bytes.Buffer{}, export.CSV, table, failing); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Errorf("err = %v", err)
	}
	if f, err := export.ParseFormat("XLSX"); err != nil || f != export.XLSX {
		t.Errorf("ParseFormat = %v, %v", f, err)
	}
	if _, err := export.ParseFormat("pdf"); err == nil {
		t.Error("pdf accepted")
	}
}
