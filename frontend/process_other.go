//go:build !linux

package frontend

import (
	"context"
	"net"
)

// executable 在不支持的平台上不存在。
type executable struct{}

// close 什么也不做。
func (*executable) close() error { return nil }

// load 返回 ErrUnsupported：有 SSR 前端时 Run 失败，只在浏览器中渲染的前端照常提供。
func (h *Host) load() (*executable, error) { return nil, ErrUnsupported }

// supervise 返回 ErrUnsupported。
func (h *Host) supervise(context.Context, *app, *executable) error { return ErrUnsupported }

// listenPeer 返回 ErrUnsupported。
func listenPeer(string) (net.Listener, error) { return nil, ErrUnsupported }
