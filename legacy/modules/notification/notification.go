// Package notification 是通知：产品定义通知类型，go-kit 负责分发、渠道与站内信。类似 Laravel 的 Notification。
//
// # 通知类型
//
// 通知类型由产品定义，字段会序列化到队列中（只放 ID、单号等简单数据）。实现哪个渠道的接口，就通过哪个渠道发送：
//
//	type OrderShipped struct {
//		OrderID uuid.UUID `json:"order_id"`
//		No      string    `json:"no"`
//	}
//
//	func (OrderShipped) Type() string { return "order.shipped" }
//
//	// 站内信
//	func (n OrderShipped) ToDatabase(ctx context.Context, r notification.Recipient) (notification.Content, error) {
//		return notification.Content{Title: i18n.T(ctx, "order.shipped", map[string]any{"No": n.No}), URL: "/orders/" + n.OrderID.String()}, nil
//	}
//
//	// 邮件（To 为空时发给 r.Email）
//	func (n OrderShipped) ToMail(ctx context.Context, r notification.Recipient) (mail.Message, error) { ... }
//
// 渲染时 ctx 的语言已经是收件人的语言（Recipient.Locale），i18n.T 直接得到对应语言的文字。
// 可选实现 Via(r Recipient) []string 进一步筛选渠道（如按用户的通知偏好）。
//
// # 发送
//
//	notes, err := notification.New(a.DB, a.Bus)
//	notes.AddChannel(notes.DatabaseChannel())
//	notes.AddChannel(notification.MailChannel(a.Mailer))
//	notification.Register[OrderShipped](notes)
//
//	err = notes.Send(ctx, user.Recipient(), OrderShipped{OrderID: o.ID, No: o.No})
//	err = notes.SendTx(ctx, tx, user.Recipient(), n) // 与业务数据同一事务
//
// 发送总是异步的：每个渠道一条消息，由 app 的消息总线处理，某个渠道失败重试时不影响其他渠道。
// 站内信保存发送时按收件人语言渲染好的标题、正文与链接，同时保存通知类型与原始数据。
//
// 迁移：把 notification.Migrations 加入 app.Options.Publish。
package notification

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
	"uuid"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
	"github.com/ryan053857/go-kit/httpx"
	"github.com/ryan053857/go-kit/i18n"
	"github.com/ryan053857/go-kit/messaging"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations 是本模块的迁移。
var Migrations = database.MigrationSource{Name: "notification", FS: mustSub(migrationsFS, "migrations")}

//go:embed locales/*.yaml
var locales embed.FS

func init() { i18n.MustLoad(locales) }

// ErrNotFound 表示站内信不存在或不属于该用户。
var ErrNotFound = httpx.DefineError(http.StatusNotFound, "notification.not_found")

// TopicDeliver 是投递通知的消息，由 New 注册的处理函数处理。
const TopicDeliver = "notification_deliver"

// Notification 是一种通知，由产品定义。Type 必须能在零值上调用（值接收者），用于在队列中还原通知。
type Notification interface {
	Type() string
}

// Router 可由通知实现，返回要使用的渠道名（在支持的渠道中再筛选）。
type Router interface {
	Via(r Recipient) []string
}

// Recipient 是通知的接收者，产品从自己的用户构造。
type Recipient struct {
	UserID uuid.UUID `json:"user_id"` // 站内信的归属
	Email  string    `json:"email,omitempty"`
	Phone  string    `json:"phone,omitempty"`
	Name   string    `json:"name,omitempty"`
	Locale string    `json:"locale,omitempty"` // 渲染通知使用的语言，空表示默认语言
}

// Channel 是发送通知的渠道。内置 DatabaseChannel（站内信）与 MailChannel（邮件），产品可以实现短信、App 推送等。
type Channel interface {
	// Name 是渠道名，如 "database"、"mail"。
	Name() string
	// Accepts 报告渠道能否发送 n（通常检查 n 是否实现了渠道要求的接口）。
	Accepts(n Notification) bool
	// Deliver 发送通知。返回错误时由消息总线重试。
	Deliver(ctx context.Context, r Recipient, n Notification) error
}

// Service 发送通知并管理站内信。
type Service struct {
	db       *bun.DB
	channels []Channel
	types    map[string]func(data []byte) (Notification, error)
	now      func() time.Time
}

// New 返回 Service，并在 bus 上注册投递通知的处理函数。
func New(db *bun.DB, bus *messaging.Bus) (*Service, error) {
	s := &Service{db: db, types: map[string]func([]byte) (Notification, error){}, now: time.Now}
	if err := messaging.Handle(bus, TopicDeliver, s.deliver); err != nil {
		return nil, err
	}
	return s, nil
}

// AddChannel 添加渠道。应在开始处理请求之前调用。
func (s *Service) AddChannel(ch Channel) { s.channels = append(s.channels, ch) }

// Register 登记通知类型 T，队列中的消息据此还原为 T。发送前必须登记。
func Register[T Notification](s *Service) {
	var zero T
	s.types[zero.Type()] = func(data []byte) (Notification, error) {
		var v T
		err := json.Unmarshal(data, &v)
		return v, err
	}
}

type delivery struct {
	Channel   string          `json:"channel"`
	Type      string          `json:"type"`
	Recipient Recipient       `json:"recipient"`
	Data      json.RawMessage `json:"data"`
}

// Send 发送通知：为每个适用的渠道发布一条消息，由消息总线异步投递。
func (s *Service) Send(ctx context.Context, r Recipient, n Notification) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error { return s.SendTx(ctx, tx, r, n) })
}

// SendTx 在 tx 中发布通知，与业务数据一起提交。通知类型未登记或没有适用的渠道时返回错误。
func (s *Service) SendTx(ctx context.Context, tx bun.Tx, r Recipient, n Notification) error {
	if _, ok := s.types[n.Type()]; !ok {
		return errors.Newf("notification: type %q not registered", n.Type())
	}
	data, err := json.Marshal(n)
	if err != nil {
		return errors.Wrapf(err, "notification: encode %s", n.Type())
	}
	sent := 0
	for _, ch := range s.via(r, n) {
		err := messaging.PublishTx(ctx, tx.Tx, TopicDeliver, delivery{Channel: ch.Name(), Type: n.Type(), Recipient: r, Data: data})
		if err != nil {
			return errors.Wrapf(err, "notification: send %s", n.Type())
		}
		sent++
	}
	if sent == 0 {
		return errors.Newf("notification: no channel for %s", n.Type())
	}
	return nil
}

func (s *Service) via(r Recipient, n Notification) []Channel {
	var want map[string]bool
	if rt, ok := n.(Router); ok {
		want = map[string]bool{}
		for _, name := range rt.Via(r) {
			want[name] = true
		}
	}
	var out []Channel
	for _, ch := range s.channels {
		if ch.Accepts(n) && (want == nil || want[ch.Name()]) {
			out = append(out, ch)
		}
	}
	return out
}

// deliver 处理 TopicDeliver：还原通知，以收件人的语言交给渠道发送。
func (s *Service) deliver(ctx context.Context, d delivery) error {
	decode, ok := s.types[d.Type]
	if !ok {
		slog.ErrorContext(ctx, "notification type not registered, dropped", "type", d.Type)
		return nil
	}
	n, err := decode(d.Data)
	if err != nil {
		slog.ErrorContext(ctx, "decode notification, dropped", "type", d.Type, "err", err)
		return nil
	}
	ctx = i18n.WithLocale(ctx, d.Recipient.Locale)
	for _, ch := range s.channels {
		if ch.Name() == d.Channel {
			return ch.Deliver(ctx, d.Recipient, n)
		}
	}
	slog.ErrorContext(ctx, "notification channel not found, dropped", "channel", d.Channel, "type", d.Type)
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
