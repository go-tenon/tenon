//go:build !linux

package ssr

import (
	"context"
	"net"
)

type process struct{}

func (h *Host) prepare() (*process, error) { return nil, ErrUnsupported }

func (h *Host) supervise(context.Context, *process) error { return ErrUnsupported }

func listenPeer(string) (net.Listener, error) { return nil, ErrUnsupported }
