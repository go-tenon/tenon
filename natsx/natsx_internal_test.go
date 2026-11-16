package natsx

import "testing"

func TestRedactURL(t *testing.T) {
	t.Parallel()
	got := redactURL("nats://user:secret@a:4222,nats://b:4222,tls://token@c:4222")
	if got != "nats://***@a:4222,nats://b:4222,tls://***@c:4222" {
		t.Error(got)
	}
}
