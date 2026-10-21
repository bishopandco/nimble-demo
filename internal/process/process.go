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

type startedKey struct{}

// WithStarted reports a successfully started direct child before waiting on it.
func WithStarted(ctx context.Context, handler func(int)) context.Context {
	return context.WithValue(ctx, startedKey{}, handler)
}

// Stream forwards stdout without retaining an unbounded stream in memory.
func Stream(ctx context.Context, executable string, args []string, output io.Writer) error {
	_, _, err := run(ctx, executable, args, nil, "", 128<<10, 2*time.Second, output)
	return err
}

// Run kills the process group on cancellation, including CLI child processes.
func Run(ctx context.Context, executable string, args []string, input io.Reader, dir string, limit int, grace time.Duration) ([]byte, []byte, error) {
	return run(ctx, executable, args, input, dir, limit, grace, nil)
}

func run(ctx context.Context, executable string, args []string, input io.Reader, dir string, limit int, grace time.Duration, stream io.Writer) ([]byte, []byte, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Dir, cmd.Stdin = dir, input
	stdout := &buffer{limit: limit, cancel: cancel}
	stderr := &buffer{limit: limit, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if stream != nil {
		cmd.Stdout = stream
	}
	cmd.WaitDelay = grace + time.Second
	cleanup := configure(cmd, grace)
	defer cleanup()
	err := cmd.Start()
	if err == nil {
		if handler, ok := ctx.Value(startedKey{}).(func(int)); ok {
			handler(cmd.Process.Pid)
		}
		err = cmd.Wait()
	}
	if stdout.overflow || stderr.overflow {
		err = ErrOutputLimit
	} else if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.Bytes(), stderr.Bytes(), err
}
