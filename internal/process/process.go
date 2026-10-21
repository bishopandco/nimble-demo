// Package process runs bounded subprocesses without a shell.
package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

var ErrOutputLimit = errors.New("process output exceeded its limit")

type buffer struct {
	bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *buffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := b.limit - b.Len(); len(p) > remaining {
		b.Buffer.Write(p[:remaining])
		b.overflow = true
		b.cancel()
	} else {
		b.Buffer.Write(p)
	}
	return n, nil
}

// Run kills the process group on cancellation, including CLI child processes.
func Run(ctx context.Context, executable string, args []string, input io.Reader, dir string, limit int, grace time.Duration) ([]byte, []byte, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Dir, cmd.Stdin = dir, input
	stdout := &buffer{limit: limit, cancel: cancel}
	stderr := &buffer{limit: limit, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = grace + time.Second
	cleanup := configure(cmd, grace)
	defer cleanup()
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		err = ErrOutputLimit
	} else if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.Bytes(), stderr.Bytes(), err
}
