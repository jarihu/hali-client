package cmd

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"hali/internal/daemon"
)

func withInterruptContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func cancelLANJob(jobID string) {
	if strings.TrimSpace(jobID) == "" || !daemon.IsRunning() {
		return
	}
	_, _ = daemon.DefaultClient().Send(daemon.Request{Cmd: daemon.CmdCancelJob, JobID: jobID})
}
