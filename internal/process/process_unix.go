//go:build unix

package process

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

func configure(cmd *exec.Cmd, grace time.Duration) func() {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var mu sync.Mutex
	var timer *time.Timer
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		mu.Lock()
		timer = time.AfterFunc(grace, func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
		mu.Unlock()
		return err
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}
