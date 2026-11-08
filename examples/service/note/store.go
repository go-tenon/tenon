// Package note 是示例业务模块：领域逻辑、REST action 与页面 action 放在同一个包中，
// 每个 action 一个文件。
package note

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 领域错误。实际产品用 github.com/cockroachdb/errors 定义与包装，日志中会带调用栈。
var (
	ErrNotFound = errors.New("note not found")
	ErrEmpty    = errors.New("note text is empty")
)

// Note 是一条笔记。
type Note struct {
	ID        string
	Text      string
	Author    string
	CreatedAt time.Time
}

// Store 是内存中的笔记存储，实际产品通常基于 pgx 实现。
type Store struct {
	mu    sync.Mutex
	seq   int
	notes []Note
}

func NewStore() *Store { return &Store{} }

func (s *Store) Create(_ context.Context, author, text string) (Note, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Note{}, ErrEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	n := Note{ID: strconv.Itoa(s.seq), Text: text, Author: author, CreatedAt: time.Now()}
	s.notes = append(s.notes, n)
	return n, nil
}

func (s *Store) List(_ context.Context) []Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.notes)
}

func (s *Store) Get(_ context.Context, id string) (Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.notes {
		if n.ID == id {
			return n, nil
		}
	}
	return Note{}, ErrNotFound
}
