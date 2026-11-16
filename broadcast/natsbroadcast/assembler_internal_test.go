package natsbroadcast

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func chunk(id string, i, total int, data string) *nats.Msg {
	m := nats.NewMsg("p.t")
	m.Header.Set(chunkHeader, fmt.Sprintf("%s %d %d", id, i, total))
	m.Data = []byte(data)
	return m
}

func TestAssembler(t *testing.T) {
	now := time.Now()
	a := assembler{}
	if data, ok := a.add(&nats.Msg{Data: []byte("plain")}, now); !ok || string(data) != "plain" {
		t.Fatalf("plain message: %q %v", data, ok)
	}

	// 两条消息交错到达，各自拼回。
	steps := []struct {
		msg  *nats.Msg
		want string
	}{
		{chunk("x", 0, 2, "ab"), ""},
		{chunk("y", 0, 2, "12"), ""},
		{chunk("x", 1, 2, "cd"), "abcd"},
		{chunk("y", 1, 2, "34"), "1234"},
	}
	for _, s := range steps {
		data, ok := a.add(s.msg, now)
		if (s.want != "") != ok || string(data) != s.want {
			t.Fatalf("add %s: got %q %v, want %q", s.msg.Header.Get(chunkHeader), data, ok, s.want)
		}
	}

	// 中间缺了一片、或者没有收到第一片：整条消息丢弃。
	a.add(chunk("z", 0, 3, "a"), now)
	if _, ok := a.add(chunk("z", 2, 3, "c"), now); ok || len(a) != 0 {
		t.Fatalf("message with a missing chunk was kept: %v", a)
	}
	if _, ok := a.add(chunk("late", 1, 2, "b"), now); ok || len(a) != 0 {
		t.Fatalf("message without its first chunk was kept: %v", a)
	}
	if _, ok := a.add(chunk("bad", 0, 0, "x"), now); ok || len(a) != 0 {
		t.Fatal("malformed header accepted")
	}

	// 过期的残片被清掉；残片最多 maxPending 条。
	a.add(chunk("old", 0, 2, "a"), now)
	a.add(chunk("new", 0, 2, "a"), now.Add(chunkTTL+time.Second))
	if _, ok := a["old"]; ok {
		t.Fatal("expired message was kept")
	}
	for i := range maxPending * 2 {
		a.add(chunk(fmt.Sprint("m", i), 0, 2, "a"), now.Add(chunkTTL+time.Second))
	}
	if len(a) > maxPending {
		t.Fatalf("%d pending messages, want at most %d", len(a), maxPending)
	}
}
