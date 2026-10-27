package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// maxLine bounds one event line on the socket.
	maxLine = 64 << 10
	// connDeadline bounds reading the event line and, separately, writing the
	// answer: the write deadline is reset after a turn start's hold.
	connDeadline = 2 * time.Second
	// sendTimeout bounds how long a full out channel holds an event.
	sendTimeout = time.Second
	ack         = "ok"
)

// Server receives hook events on a Unix socket and forwards them to a channel.
// Each connection carries one JSON line and is answered with "ok" once the
// event is on the channel; a turn start is answered after its Done is called
// or Hold passes.
type Server struct {
	// Log receives dropped events; nil discards them.
	Log *slog.Logger
	// Hold is how long a turn-start event's answer waits for its Done; 0 never
	// holds. Set before Serve.
	Hold time.Duration

	ln   net.Listener
	path string

	mu     sync.Mutex
	closed bool
	quit   chan struct{} // closed on shutdown
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
}

// Listen binds a socket at path with mode 0600, replacing a stale socket file.
func Listen(path string) (*Server, error) {
	switch fi, err := os.Lstat(path); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("harness: inspect %s: %w", path, err)
	case fi.Mode().Type() != os.ModeSocket:
		return nil, fmt.Errorf("harness: %s exists and is not a socket", path)
	default:
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("harness: remove stale socket: %w", err)
		}
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("harness: listen: %w", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()

		return nil, fmt.Errorf("harness: chmod socket: %w", err)
	}

	return &Server{ln: ln, path: path, quit: make(chan struct{}), conns: make(map[net.Conn]struct{})}, nil
}

// Serve accepts connections until ctx ends or Close is called, then waits for
// the connection goroutines.
func (s *Server) Serve(ctx context.Context, out chan<- Event) error {
	stop := context.AfterFunc(ctx, s.shutdown)
	defer stop()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()

			if closed || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				s.wg.Wait()

				return nil
			}

			return fmt.Errorf("harness: accept: %w", err)
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()

			continue
		}

		s.conns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()

		go s.handle(ctx, conn, out)
	}
}

// Close stops accepting, ends open connections, waits for their goroutines
// and removes the socket file.
func (s *Server) Close() error {
	s.shutdown()
	s.wg.Wait()

	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("harness: remove socket: %w", err)
	}

	return nil
}

func (s *Server) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.closed = true
	close(s.quit)
	_ = s.ln.Close()

	for c := range s.conns {
		_ = c.Close()
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn, out chan<- Event) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()

		_ = conn.Close()
	}()

	_ = conn.SetDeadline(time.Now().Add(connDeadline))

	ev, err := readEvent(conn)
	if err != nil {
		s.drop("bad event", err)

		return
	}

	var held chan struct{}

	if ev.TurnStart && s.Hold > 0 {
		held = make(chan struct{})
		ev.Done = sync.OnceFunc(func() { close(held) })
	}

	timer := time.NewTimer(sendTimeout)
	defer timer.Stop()

	select {
	case out <- ev:
	case <-timer.C:
		s.drop("event channel full", nil)

		return
	case <-ctx.Done():
		s.drop("shutting down", nil)

		return
	}

	if held != nil && !s.hold(ctx, held) {
		return
	}

	_ = conn.SetWriteDeadline(time.Now().Add(sendTimeout))
	_, _ = io.WriteString(conn, ack+"\n")
}

// hold waits for done, the hold time, shutdown or ctx; it reports whether the
// connection should still be answered.
func (s *Server) hold(ctx context.Context, done <-chan struct{}) bool {
	timer := time.NewTimer(s.Hold)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
	case <-s.quit:
		return false
	case <-ctx.Done():
		return false
	}

	return true
}

func (s *Server) drop(msg string, err error) {
	if s.Log == nil {
		return
	}

	if err != nil {
		s.Log.Warn("hook event dropped", "reason", msg, "err", err)

		return
	}

	s.Log.Warn("hook event dropped", "reason", msg)
}

// readEvent reads one JSON line of at most maxLine bytes.
func readEvent(r io.Reader) (Event, error) {
	br := bufio.NewReader(io.LimitReader(r, maxLine+1))

	line, err := br.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) > maxLine {
			return Event{}, errors.New("line too long")
		}

		return Event{}, fmt.Errorf("read line: %w", err)
	}

	var ev Event
	if err := json.Unmarshal(line, &ev); err != nil {
		return Event{}, fmt.Errorf("decode: %w", err)
	}

	return ev, nil
}

// Send delivers ev to the server at path and waits for its answer.
func Send(ctx context.Context, path string, ev Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("harness: encode event: %w", err)
	}

	var d net.Dialer

	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("harness: dial %s: %w", path, err)
	}
	defer func() { _ = conn.Close() }()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	// Unblocks a read or write when ctx ends without a deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("harness: send event: %w", err)
	}

	reply, err := bufio.NewReader(io.LimitReader(conn, 16)).ReadString('\n')
	if err != nil {
		return fmt.Errorf("harness: await answer: %w", err)
	}

	if strings.TrimSpace(reply) != ack {
		return fmt.Errorf("harness: unexpected answer %q", reply)
	}

	return nil
}
