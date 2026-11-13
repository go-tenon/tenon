package mailjob

import (
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/queue"
)

// Send 把邮件交给 worker 发送。
var Send = queue.Topic[mail.Message]("mail.send")
