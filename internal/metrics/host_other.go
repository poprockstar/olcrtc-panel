//go:build !linux

package metrics

import "context"

type DefaultHostReader struct{}

func (DefaultHostReader) ReadHost(context.Context) (HostSnapshot, error) {
	return HostSnapshot{}, nil
}
