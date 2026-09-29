//go:build windows

package main

import (
	"context"
	"github.com/xiaoliu-heng/homefleet/internal/agent"
	"golang.org/x/sys/windows/svc"
)

type handler struct{ engine *agent.Engine }

func (h *handler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.engine.Run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				return false, 0
			}
		}
	}
}
func runService(e *agent.Engine) (bool, error) {
	active, err := svc.IsWindowsService()
	if err != nil {
		return true, err
	}
	if !active {
		return false, nil
	}
	return true, svc.Run("HomeFleetAgent", &handler{engine: e})
}
