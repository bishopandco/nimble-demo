// Package monitor samples bot processes and streams persisted call snapshots.
package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go-api/internal/model"
	"go-api/internal/store"
)

type processInfo struct {
	pid, parent int
	memory      int64
	cpu         float64
	command     string
}

func processes(ctx context.Context) (map[int]processInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,rss=,pcpu=,comm=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("read process statistics: %w", err)
	}
	result := make(map[int]processInfo)
	for _, line := range strings.Split(output.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, e1 := strconv.Atoi(fields[0])
		parent, e2 := strconv.Atoi(fields[1])
		rss, e3 := strconv.ParseInt(fields[2], 10, 64)
		cpu, e4 := strconv.ParseFloat(fields[3], 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		result[pid] = processInfo{pid: pid, parent: parent, memory: max(0, rss) * 1024, cpu: max(0, cpu), command: strings.Join(fields[4:], " ")}
	}
	return result, nil
}

func sample(call model.BotCall, table map[int]processInfo) model.BotCall {
	call.MemoryBytes, call.CPUPercent = nil, nil
	call.ProcessCount, call.ProviderPID = 0, 0
	root, found := table[call.PID]
	// Parent matching avoids sampling a recycled PID belonging to another process.
	if !found || root.parent != call.OwnerPID {
		return call
	}
	seen := map[int]bool{call.PID: true}
	for changed := true; changed; {
		changed = false
		for pid, process := range table {
			if !seen[pid] && seen[process.parent] {
				seen[pid] = true
				changed = true
			}
		}
	}
	var memory int64
	var cpu float64
	for pid := range seen {
		process := table[pid]
		memory += process.memory
		cpu += process.cpu
		if process.parent == call.PID && (call.ProviderPID == 0 || pid < call.ProviderPID) {
			call.ProviderPID = pid
		}
		name := strings.ToLower(filepath.Base(process.command))
		if name == "codex" || name == "claude" {
			call.Provider = name
		}
	}
	call.MemoryBytes, call.CPUPercent, call.ProcessCount = &memory, &cpu, len(seen)
	return call
}

func snapshot(ctx context.Context, db *store.Store, source, message string) (model.MonitorSnapshot, error) {
	calls, err := db.BotCalls(ctx, 50)
	if err != nil {
		return model.MonitorSnapshot{}, err
	}
	events, err := db.BotCallEvents(ctx, 100)
	if err != nil {
		return model.MonitorSnapshot{}, err
	}
	return model.MonitorSnapshot{Type: "monitor_snapshot", Source: source, Timestamp: time.Now().UTC(), Calls: calls, Events: events, Error: message}, nil
}

// Run is the monitor sidecar's loop. Samples and lifecycle events share SQLite.
func Run(ctx context.Context, db *store.Store, output io.Writer, interval time.Duration, parentPID int) error {
	if interval < 250*time.Millisecond {
		return fmt.Errorf("monitor interval must be at least 250ms")
	}
	encoder := json.NewEncoder(output)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		table, sampleErr := processes(ctx)
		calls, err := db.BotCalls(ctx, 200)
		if err != nil {
			return err
		}
		parentGone := false
		if sampleErr == nil {
			_, parentPresent := table[parentPID]
			parentGone = parentPID > 0 && !parentPresent
			for _, call := range calls {
				if !call.Active() {
					continue
				}
				if _, alive := table[call.OwnerPID]; !alive {
					if err := db.FinishBotCall(ctx, call.ID, "interrupted", "", "API process exited"); err != nil {
						return err
					}
				} else if call.Status == "running" && call.PID > 0 {
					if err := db.SampleBotCall(ctx, sample(call, table)); err != nil {
						return err
					}
				}
			}
		}
		message := ""
		if sampleErr != nil {
			message = "Process statistics are unavailable on this host."
		}
		current, err := snapshot(ctx, db, "live", message)
		if err != nil {
			return err
		}
		if err := encoder.Encode(current); err != nil {
			return err
		}
		if parentGone {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
