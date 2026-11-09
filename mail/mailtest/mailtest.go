// Package mailtest 提供测试用的 Mailer。
package mailtest

import (
	"context"
	"sync"

	"github.com/ryan053857/go-kit/mail"
)

// Recorder 记录发出的邮件而不真正发送，可以并发使用。
type Recorder struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (r *Recorder) Send(_ context.Context, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return nil
}

// Sent 返回已发送邮件的副本。
func (r *Recorder) Sent() []mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mail.Message(nil), r.sent...)
}
