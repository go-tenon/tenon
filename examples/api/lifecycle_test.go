//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryan053857/mygoravel/command"
	"github.com/ryan053857/mygoravel/database/dbtest"
	"github.com/ryan053857/mygoravel/database/migrate"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("MYGORAVEL_TEST_PROCESS") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("MYGORAVEL_TEST_ARGS")), &args); err != nil {
		os.Exit(90)
	}
	os.Args = append([]string{"orders"}, args...)
	os.Exit(run())
}

func TestCommandsMigrateServeAndSignal(t *testing.T) {
	db := dbtest.Open(t, nil)
	var name string
	if err := db.QueryRow("select current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dbtest.RequireDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	migrationConfig := writeConfig(t, "database:\n  dsn: "+u.String()+"\nauth:\n  credentials: ${UNSET_AUTH}\ncache:\n  addr: ${UNSET_CACHE}\n")
	var out, diag bytes.Buffer
	for _, sub := range []string{"up", "status"} {
		cmd := newCommand(command.IO{Out: &out, Err: &diag})
		cmd.SetArgs([]string{"--config", migrationConfig, "migrate", sub})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	if !json.Valid(out.Bytes()) || diag.Len() != 0 {
		t.Fatalf("migrate streams: %s %s", out.String(), diag.String())
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	path := writeConfig(t, "http:\n  addr: "+addr+"\ndatabase:\n  dsn: "+u.String()+"\nauth:\n  credentials:\n    - token: integration-token\n      subject_id: alice\ncache:\n  driver: memory\n")
	args, _ := json.Marshal([]string{"--config", path, "serve"})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessHelper$")
	child.Env = append(os.Environ(), "MYGORAVEL_TEST_PROCESS=1", "MYGORAVEL_TEST_ARGS="+string(args))
	var stdout, stderr bytes.Buffer
	child.Stdout = &stdout
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	client := &http.Client{Timeout: time.Second}
	base := "http://" + addr
	deadline := time.Now().Add(8 * time.Second)
	for {
		resp, err := client.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 204 {
				break
			}
		}
		if time.Now().After(deadline) {
			child.Process.Kill()
			_ = child.Wait()
			t.Fatalf("server not ready: %s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	req, _ := http.NewRequest("POST", base+"/orders", strings.NewReader(`{"title":"command test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer integration-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, string(body))
	}
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("signal exit: %v %s", err, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "http server stopped") {
		t.Fatalf("stream/lifecycle mismatch: %s %s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "integration-token") {
		t.Fatal("logged credentials")
	}
}
func TestApplicationBaselineUpgrade(t *testing.T) {
	if err := migrate.CheckPublished("migrations", "testdata/baseline"); err != nil {
		t.Fatal(err)
	}
	db := dbtest.Open(t, os.DirFS("testdata/baseline"))
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "INSERT INTO orders (id,owner_id,title,created_at) VALUES ('01900000-0000-7000-8000-000000000001','alice','old',now())"); err != nil {
		t.Fatal(err)
	}
	fsys, err := migrateFS()
	if err != nil {
		t.Fatal(err)
	}
	p, err := migrate.NewProvider(db.DB, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, p); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := db.QueryRow("SELECT title FROM orders").Scan(&title); err != nil || title != "old" {
		t.Fatal(title, err)
	}
}
