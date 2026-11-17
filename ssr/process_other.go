//go:build !linux

package ssr

import (
	"context"
	"net"
)

type process struct{}

func (*process) close() error { return nil }

func (h *Host) prepare() (*process, error) { return nil, ErrUnsupported }

func (r *Runtime) load() (string, error) { return "", ErrUnsupported }

func (h *Host) supervise(context.Context, *process) error { return ErrUnsupported }

func listenPeer(string) (net.Listener, error) { return nil, ErrUnsupported }
