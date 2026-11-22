package period_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/period"
)

func TestResolveDefaultsAndPartialDates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 22, 16, 30, 0, 0, time.UTC) // 上海时间 2026-11-23 00:30

	// 省略日期时按目标时区的“今天”往前推 DefaultDays-1 天
	w, err := period.Resolve(period.Params{}, period.Options{Now: now, DefaultZone: "Asia/Shanghai", DefaultDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if w.From != "2026-11-17" || w.To != "2026-11-23" || w.Days != 7 || w.TimeZone != "Asia/Shanghai" {
		t.Fatalf("unexpected window: %+v", w)
	}
	if w.Granularity != period.Day {
		t.Fatalf("granularity=%s, want day", w.Granularity)
	}
	if w.PrevFrom != "2026-11-10" || w.PrevTo != "2026-11-16" {
		t.Fatalf("prev=%s..%s", w.PrevFrom, w.PrevTo)
	}
	// 上海 2026-11-17 00:00 = UTC 2026-11-16 16:00
	wantStart := time.Date(2026, 11, 16, 16, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)
	if !w.Start.Equal(wantStart) || !w.End.Equal(wantEnd) || !w.PrevEnd.Equal(wantStart) {
		t.Fatalf("bounds start=%v end=%v prevEnd=%v", w.Start, w.End, w.PrevEnd)
	}

	// 只传 from 时 to 取当天
	w, err = period.Resolve(period.Params{From: "2026-11-20"}, period.Options{Now: now, DefaultZone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	if w.From != "2026-11-20" || w.To != "2026-11-23" || w.Days != 4 {
		t.Fatalf("only from: %+v", w)
	}

	// 只传 to 时 from 往前推 DefaultDays-1 天
	w, err = period.Resolve(period.Params{To: "2026-11-10"}, period.Options{Now: now, DefaultZone: "UTC", DefaultDays: 5})
	if err != nil {
		t.Fatal(err)
	}
	if w.From != "2026-11-06" || w.To != "2026-11-10" || w.Days != 5 {
		t.Fatalf("only to: %+v", w)
	}
}

func TestResolveDSTAndAutoGranularity(t *testing.T) {
	t.Parallel()
	// America/New_York 2026-03-08 为夏令时春跳（当天只有 23 小时），2026-11-01 为秋退（当天 25 小时）
	w, err := period.Resolve(period.Params{From: "2026-03-08", To: "2026-03-08", TimeZone: "America/New_York"}, period.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Days != 1 {
		t.Fatalf("spring forward days=%d, want 1", w.Days)
	}
	if got := w.End.Sub(w.Start); got != 23*time.Hour {
		t.Fatalf("spring forward duration=%v, want 23h", got)
	}

	w, err = period.Resolve(period.Params{From: "2026-11-01", To: "2026-11-01", TimeZone: "America/New_York", Granularity: period.Hour}, period.Options{AllowHour: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.End.Sub(w.Start); got != 25*time.Hour {
		t.Fatalf("fall back duration=%v, want 25h", got)
	}
	if buckets := w.Buckets(); len(buckets) != 25 {
		t.Fatalf("fall back hour buckets=%d, want 25", len(buckets))
	}

	// 跨度超过 62 天自动切换为 week
	w, err = period.Resolve(period.Params{From: "2026-01-01", To: "2026-03-10"}, period.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Granularity != period.Week {
		t.Fatalf("granularity=%s, want week", w.Granularity)
	}
}

func TestBucketsAndFillSeries(t *testing.T) {
	t.Parallel()
	type point struct {
		Bucket string
		Count  int
	}

	// 按天补零
	w, err := period.Resolve(period.Params{From: "2026-11-01", To: "2026-11-03"}, period.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := period.FillSeries(w, []point{{Bucket: "2026-11-02", Count: 5}}, func(p point) string { return p.Bucket }, func(b string) point { return point{Bucket: b} })
	want := []point{{Bucket: "2026-11-01", Count: 0}, {Bucket: "2026-11-02", Count: 5}, {Bucket: "2026-11-03", Count: 0}}
	if !slices.Equal(got, want) {
		t.Fatalf("day fill=%+v, want %+v", got, want)
	}

	// 按周对齐周一（2026-11-04 是周三，所在周周一为 2026-11-02）
	w, err = period.Resolve(period.Params{From: "2026-11-04", To: "2026-11-12", Granularity: period.Week}, period.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Buckets(); !slices.Equal(got, []string{"2026-11-02", "2026-11-09"}) {
		t.Fatalf("week buckets=%v", got)
	}

	// 按月对齐每月 1 日
	w, err = period.Resolve(period.Params{From: "2026-11-15", To: "2027-01-05", Granularity: period.Month}, period.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Buckets(); !slices.Equal(got, []string{"2026-11-01", "2026-12-01", "2027-01-01"}) {
		t.Fatalf("month buckets=%v", got)
	}
}

func TestResolveValidationErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		p     period.Params
		opts  period.Options
		want  error
		field string
	}{
		{"invalid tz", period.Params{TimeZone: "Mars/Olympus"}, period.Options{}, period.ErrInvalidTimeZone, "tz"},
		{"local tz rejected", period.Params{TimeZone: "Local"}, period.Options{}, period.ErrInvalidTimeZone, "tz"},
		{"bad from", period.Params{From: "2026/11/01"}, period.Options{}, period.ErrInvalidRange, "from"},
		{"bad to", period.Params{To: "not-a-date"}, period.Options{}, period.ErrInvalidRange, "to"},
		{"from after to", period.Params{From: "2026-11-10", To: "2026-11-01"}, period.Options{}, period.ErrInvalidRange, "from"},
		{"range too large", period.Params{From: "2025-01-01", To: "2026-11-01"}, period.Options{MaxDays: 90}, period.ErrRangeTooLarge, "to"},
		{"hour disallowed", period.Params{From: "2026-11-01", To: "2026-11-01", Granularity: period.Hour}, period.Options{}, period.ErrInvalidGranularity, "granularity"},
		{"hour range > 2 days", period.Params{From: "2026-11-01", To: "2026-11-03", Granularity: period.Hour}, period.Options{AllowHour: true}, period.ErrInvalidGranularity, "granularity"},
		{"unknown granularity", period.Params{Granularity: "quarter"}, period.Options{}, period.ErrInvalidGranularity, "granularity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := period.Resolve(tc.p, tc.opts)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
			var ex *errorx.Error
			if !errors.As(err, &ex) || ex.Field() != tc.field {
				t.Fatalf("field=%q, want %q", ex.Field(), tc.field)
			}
		})
	}
}
