//go:build !unix

package process

import (
	"os/exec"
	"time"
)

func configure(cmd *exec.Cmd, grace time.Duration) func() { return func() {} }
