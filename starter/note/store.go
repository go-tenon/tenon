// Package note 是示例业务模块：领域逻辑、REST action 与页面 action 放在同一个包中，
// 每个 action 一个文件。
package note

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"uuid"

	"github.com/uptrace/bun"

	"github.com/ryan053857/go-kit/database"
)

// 领域错误。实际产品用 github.com/cockroachdb/errors 定义与包装，日志中会带调用栈。
var (
	ErrNotFound = errors.New("note not found")
	ErrEmpty    = errors.New("note text is empty")
)

// Note 是一条笔记，对应 notes 表。
type Note struct {
	bun.BaseModel `bun:"table:notes"`
	database.Model

	Text   string `bun:"text,notnull"`
	Author string `bun:"author,notnull"`
}

// Store 读写笔记。
type Store struct {
	db bun.IDB
}

func NewStore(db bun.IDB) *Store { return &Store{db: db} }

func (s *Store) Create(ctx context.Context, author, text string) (*Note, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrEmpty
	}
	n := &Note{Text: text, Author: author}
	if _, err := s.db.NewInsert().Model(n).Exec(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Store) List(ctx context.Context) ([]Note, error) {
	notes := []Note{}
	err := s.db.NewSelect().Model(&notes).Order("id").Scan(ctx)
	return notes, err
}

func (s *Store) Get(ctx context.Context, id string) (*Note, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, ErrNotFound
	}
	n := new(Note)
	err = s.db.NewSelect().Model(n).Where("id = ?", uid).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}
