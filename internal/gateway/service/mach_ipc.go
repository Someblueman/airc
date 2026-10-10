package service

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/machipc"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type machIPCOwner struct {
	listener *machipc.Listener
	driver   *adapter.MachIPC
}

func newMachIPCOwner(ctx context.Context, c config.Config, l config.Listener, d *dispatch.Dispatcher, requests chan struct{}) (*machIPCOwner, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.Protocol != "mach_ipc" || l.MachIPC == nil || !config.MachIPCAvailable() {
		return nil, store.ErrInvalid
	}
	protection, err := loadConnectionResponseProtection(c, l)
	if err != nil {
		return nil, err
	}
	callers := make([]adapter.MachCaller, 0, len(l.MachIPC.Callers))
	for _, caller := range l.MachIPC.Callers {
		if caller.UID == nil {
			return nil, store.ErrInvalid
		}
		callers = append(callers, adapter.MachCaller{UID: *caller.UID, Principal: caller.Principal})
	}
	driver, err := adapter.NewMachIPC(d, requests, protection, callers)
	if err != nil {
		return nil, err
	}
	listener, err := machipc.Listen(l.Address)
	if err != nil {
		return nil, err
	}
	return &machIPCOwner{listener: listener, driver: driver}, nil
}

func (o *machIPCOwner) close() error { return o.listener.Close() }
func (o *machIPCOwner) serve(ctx context.Context) error {
	return o.driver.Serve(ctx, o.listener)
}
