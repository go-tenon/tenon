package database_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/uptrace/bun"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/dbtest"
)

// withParam 返回在 dsn 上设置查询参数 key=value 之后的连接串。
func withParam(t *testing.T, dsn, key, value string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

// note 是测试用的表。
type note struct {
	bun.BaseModel `bun:"table:notes"`
	Body          string `bun:"body"`
}

// 事务之外的 SELECT 在读库之间轮流执行，写操作、原始 SQL、事务和 Primary 的 ctx 中的语句在主库执行。
func TestReplicas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := dbtest.Open(t)
	if _, err := base.ExecContext(ctx, "CREATE TABLE notes (body text)"); err != nil {
		t.Fatal(err)
	}
	dsn := dbtest.DSN(t, base)
	db, err := database.Open(ctx, database.Config{
		DSN:      withParam(t, dsn, "application_name", "primary"),
		Replicas: []string{withParam(t, dsn, "application_name", "r1"), withParam(t, dsn, "application_name", "r2")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// server 返回 Bun 的 SELECT 在哪个库执行。
	server := func(ctx context.Context, idb bun.IDB) string {
		t.Helper()
		var name string
		if err := idb.NewSelect().ColumnExpr("current_setting('application_name')").Scan(ctx, &name); err != nil {
			t.Fatal(err)
		}
		return name
	}
	if a, b, c := server(ctx, db), server(ctx, db), server(ctx, db); a == b || a != c || a == "primary" || b == "primary" {
		t.Fatalf("selects ran on %s, %s, %s", a, b, c)
	}
	if got := server(database.Primary(ctx), db); got != "primary" {
		t.Fatalf("select with Primary ran on %s", got)
	}
	var raw string
	if err := db.NewRaw("SELECT current_setting('application_name')").Scan(ctx, &raw); err != nil || raw != "primary" {
		t.Fatalf("raw select ran on %q: %v", raw, err)
	}
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if got := server(ctx, tx); got != "primary" {
			t.Errorf("select in transaction ran on %s", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 写操作在主库执行；读库的会话只读，写到读库会失败。
	if _, err := db.NewInsert().Model(&note{Body: "x"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := db.NewSelect().Model((*note)(nil)).Count(ctx); err != nil || n != 1 {
		t.Fatalf("count %d: %v", n, err)
	}
	// 事务之外的 FOR UPDATE 也分到读库，读库拒绝它。
	err = db.NewSelect().Model(&note{}).For("UPDATE").Limit(1).Scan(ctx)
	if !database.IsReadOnly(err) {
		t.Fatalf("select for update outside transaction: %v", err)
	}
}

// 没有读库时全部语句在主库执行；读库的连接串不能为空。
func TestReplicasConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{DSN: withParam(t, dbtest.URL(t), "application_name", "primary")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.NewSelect().ColumnExpr("current_setting('application_name')").Scan(ctx, &name); err != nil || name != "primary" {
		t.Fatalf("select ran on %q: %v", name, err)
	}
	cfg := database.DefaultConfig()
	cfg.DSN, cfg.Replicas = dbtest.URL(t), []string{""}
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty replica dsn should fail")
	}
}

// 链路和指标的 server.address 是语句实际执行的库，读库的连接池指标带着各自的池名。
func TestReplicaTelemetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	primary := dbtest.URL(t)
	u, err := url.Parse(primary)
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "127.0.0.1" {
		t.Skipf("POSTGRES_TEST_URL host is %s, want 127.0.0.1", u.Hostname())
	}
	u.Host = "localhost:" + u.Port()
	reader := sdkmetric.NewManualReader()
	spans := tracetest.NewSpanRecorder()
	db, err := database.Open(ctx, database.Config{DSN: primary, Replicas: []string{u.String()}}, database.WithTelemetry(
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, parent := sdktrace.NewTracerProvider().Tracer("test").Start(ctx, "request")
	defer parent.End()
	var n int
	if err := db.NewSelect().ColumnExpr("1").Scan(ctx, &n); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw("SELECT 1").Scan(ctx, &n); err != nil {
		t.Fatal(err)
	}
	ended := spans.Ended()
	if len(ended) != 2 {
		t.Fatalf("%d spans", len(ended))
	}
	if got := spanAttrs(ended[0])["server.address"].AsString(); got != "localhost" {
		t.Fatalf("select on replica reported %s", got)
	}
	if got := spanAttrs(ended[1])["server.address"].AsString(); got != "127.0.0.1" {
		t.Fatalf("raw select on primary reported %s", got)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	pools := map[string]bool{}
	for _, dp := range findMetric(t, rm, "db.client.connection.max").Data.(metricdata.Sum[int64]).DataPoints {
		v, _ := dp.Attributes.Value("db.client.connection.pool.name")
		pools[v.AsString()] = true
	}
	if !pools["primary"] || !pools["primary-replica-1"] {
		t.Fatalf("pools %v", pools)
	}
}
