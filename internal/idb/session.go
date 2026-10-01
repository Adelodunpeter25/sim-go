package idb

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	gen "github.com/Adelodunpeter25/sim-go/internal/idb/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Session is one supervised companion per simulator: process + gRPC client.
// It mirrors scrcpy.Session's shape so serve can treat both backends alike.
type Session struct {
	UDID string
	Desc *gen.TargetDescriptionResponse

	conn    *grpc.ClientConn
	client  gen.CompanionServiceClient
	process *exec.Cmd
	errLog  *os.File
	logPath string
	port    int
	closed  bool
	mu      sync.Mutex
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Start boots nothing itself: the simulator must already be booted (use the
// ios driver). It supervises a companion bound to the UDID and waits for its
// gRPC port to accept.
func Start(ctx context.Context, udid string) (*Session, error) {
	bin, err := EnsureCompanion(ctx)
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	logF, err := os.CreateTemp("", "sim-go-idb-*.log")
	if err != nil {
		return nil, err
	}
	proc := exec.Command(bin, "--udid", udid, "--grpc-port", strconv.Itoa(port))
	proc.Stderr = logF
	proc.Stdout = logF
	if err := proc.Start(); err != nil {
		_ = logF.Close()
		return nil, fmt.Errorf("start idb_companion: %w", err)
	}
	s := &Session{UDID: udid, process: proc, errLog: logF, logPath: logF.Name(), port: port}
	if err := s.waitReady(30 * time.Second); err != nil {
		s.Close()
		return nil, err
	}
	conn, err := grpc.NewClient("127.0.0.1:"+strconv.Itoa(port),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("idb grpc dial: %w", err)
	}
	s.conn = conn
	s.client = gen.NewCompanionServiceClient(conn)
	desc, err := s.client.Describe(ctx, &gen.TargetDescriptionRequest{})
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("idb describe: %w", err)
	}
	s.Desc = desc
	return s, nil
}

func (s *Session) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(s.port), 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			// Port open, but the gRPC server may still be initializing; the
			// Describe call right after is the real readiness proof.
			return nil
		}
		if exited(s.process) {
			return fmt.Errorf("idb_companion exited early: %s", s.serverError())
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("idb_companion never listened: %s", s.serverError())
}

func exited(proc *exec.Cmd) bool {
	if proc == nil || proc.Process == nil {
		return true
	}
	// Signal 0 probes liveness without delivering anything.
	return syscall.Kill(proc.Process.Pid, 0) != nil
}

// Close kills the companion. Idempotent.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if s.process != nil && s.process.Process != nil {
		_ = s.process.Process.Kill()
		_ = s.process.Wait()
	}
	if s.errLog != nil {
		_ = s.errLog.Close()
	}
}

// serverError returns the companion's captured output for diagnostics.
func (s *Session) serverError() string {
	if s.logPath == "" {
		return ""
	}
	out, err := os.ReadFile(s.logPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	return strings.Join(lines, "\n")
}

// Points returns HID points scale: video pixels per HID point on each axis,
// derived from describe (pixels vs points). Input arrives in video pixels
// (the WS contract) and is mapped to points here.
func (s *Session) Points() (sx, sy float64) {
	d := s.Desc.GetTargetDescription().GetScreenDimensions()
	if d.GetWidthPoints() == 0 || d.GetHeightPoints() == 0 {
		return 3, 3 // 16e-class fallback; describe fills the real values
	}
	return float64(d.GetWidth()) / float64(d.GetWidthPoints()),
		float64(d.GetHeight()) / float64(d.GetHeightPoints())
}
