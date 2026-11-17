//go:build !linux

package ssr

import (
	"context"
	"net"
)

// process 在不支持的平台上不存在。
type process struct{}

// close 什么也不做。
func (*process) close() error { return nil }

// prepare 返回 ErrUnsupported。
func (h *Host) prepare() (*process, error) { return nil, ErrUnsupported }

// load 返回 ErrUnsupported。
func (r *Runtime) load() (string, error) { return "", ErrUnsupported }

// supervise 返回 ErrUnsupported。
func (h *Host) supervise(context.Context, *process) error { return ErrUnsupported }

// listenPeer 返回 ErrUnsupported。
func listenPeer(string) (net.Listener, error) { return nil, ErrUnsupported }
