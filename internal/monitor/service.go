package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"go-api/internal/model"
	"go-api/internal/process"
	"go-api/internal/store"
)

// Service owns the monitor subprocess and its independent WebSocket subscribers.
type Service struct {
	db             *store.Store
	path, database string
	interval       time.Duration
	logger         *slog.Logger
	mu             sync.Mutex
	latest         model.MonitorSnapshot
	clients        map[*subscriber]struct{}
	closed         bool
	cancel         context.CancelFunc
	worker         sync.WaitGroup
	connections    sync.WaitGroup
	once           sync.Once
}

func New(db *store.Store, database, path string, interval time.Duration, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, database: database, path: path, interval: interval, logger: logger.With("component", "bot_monitor"), clients: make(map[*subscriber]struct{}),
		latest: model.MonitorSnapshot{Type: "monitor_snapshot", Source: "starting", Timestamp: time.Now().UTC(), Calls: []model.BotCall{}, Events: []model.BotCallEvent{}}}
}

func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.worker.Add(1)
	go func() {
		defer s.worker.Done()
		for ctx.Err() == nil {
			err := s.runChild(ctx)
			if ctx.Err() != nil {
				return
			}
			s.logger.Error("monitor sidecar stopped", "error", err)
			refreshCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			current, loadErr := snapshot(refreshCtx, s.db, "offline", "Monitor unavailable. Check BOT_MONITOR_PATH and its database.")
			stop()
			if loadErr == nil {
				s.publish(current)
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (s *Service) runChild(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	decoded := make(chan error, 1)
	go func() {
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var current model.MonitorSnapshot
			if err := json.Unmarshal(scanner.Bytes(), &current); err != nil {
				cancel()
				decoded <- err
				return
			}
			if current.Type == "monitor_snapshot" {
				s.publish(current)
			}
		}
		if err := scanner.Err(); err != nil {
			cancel()
			decoded <- err
			return
		}
		decoded <- nil
	}()
	args := []string{"-database", s.database, "-interval", s.interval.String(), "-parent-pid", strconv.Itoa(os.Getpid())}
	err := process.Stream(ctx, s.path, args, writer)
	writer.CloseWithError(err)
	decodeErr := <-decoded
	if err != nil {
		return err
	}
	return decodeErr
}

func (s *Service) publish(current model.MonitorSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.latest = current
	for client := range s.clients {
		select {
		case client.send <- current:
		default:
			client.close()
		}
	}
}

func (s *Service) Queue(ctx context.Context, id int64, provider string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.db.QueueBotCall(ctx, id, os.Getpid(), provider)
}

func (s *Service) Begin(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.db.StartBotCall(ctx, id)
}

func (s *Service) Started(id int64, pid int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.db.SetBotCallPID(ctx, id, pid); err != nil {
		s.logger.Error("record bot PID", "call_id", id, "error", err)
	}
}

func (s *Service) Finished(id int64, status, provider, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.db.FinishBotCall(ctx, id, status, provider, message); err != nil {
		s.logger.Error("record bot completion", "call_id", id, "error", err)
	}
}

func (s *Service) Close() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.worker.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		calls, err := s.db.BotCalls(ctx, 200)
		if err != nil {
			s.logger.Error("load unfinished bot calls on shutdown", "error", err)
		} else {
			for _, call := range calls {
				if call.OwnerPID == os.Getpid() && call.Active() {
					if err := s.db.FinishBotCall(ctx, call.ID, "canceled", "", "Server stopped"); err != nil {
						s.logger.Error("cancel bot call on shutdown", "call_id", call.ID, "error", err)
					}
				}
			}
		}
		cancel()
		s.mu.Lock()
		s.closed = true
		clients := make([]*subscriber, 0, len(s.clients))
		for client := range s.clients {
			clients = append(clients, client)
		}
		s.mu.Unlock()
		deadline := time.Now().Add(time.Second)
		for _, client := range clients {
			client.shutdown(deadline)
		}
		s.connections.Wait()
	})
}
