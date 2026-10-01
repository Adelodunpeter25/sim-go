package scrcpy

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Meta describes the device video stream, read from the handshake.
type Meta struct {
	Name   string
	Width  int
	Height int
}

// Session is one live scrcpy device session: an adb-pushed server, a video
// socket (H.264 in) and a control socket (input out). One session per device;
// future WS viewers multiplex on top.
type Session struct {
	Serial string
	Meta   Meta

	adb        string
	port       int
	process    *exec.Cmd
	errLog     *os.File
	errLogPath string
	video      net.Conn
	control    net.Conn
	ctrlMu     sync.Mutex
	closed     bool
	closeMu    sync.Mutex
}

// adbPath resolves via ANDROID_HOME, well-known SDK homes, then PATH.
// (Mirrors android.adbPath without importing that package;
// scrcpy stays a leaf dependency.)
func adbPath() string {
	for _, base := range sdkCandidates() {
		if cand := base + "/platform-tools/adb"; isExecFile(cand) {
			return cand
		}
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p
	}
	return "adb"
}

func sdkCandidates() []string {
	var out []string
	if p := os.Getenv("ANDROID_HOME"); p != "" {
		out = append(out, p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			home+"/Library/Android/sdk",
			home+"/Android/Sdk",
		)
	}
	return out
}

func start(ctx context.Context, serial string) (*Session, error) {
	adb := adbPath()
	server, err := EnsureServer(ctx)
	if err != nil {
		return nil, err
	}
	remote := "/data/local/tmp/sim-go-scrcpy-" + Version + ".jar"
	if out, err := runAdb(ctx, adb, "-s", serial, "shell", "ls", remote); err != nil ||
		!strings.Contains(string(out), remote) {
		if out, err := runAdb(ctx, adb, "-s", serial, "push", server, remote); err != nil {
			return nil, fmt.Errorf("push scrcpy server: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	// scid is parsed as HEX and must fit int32 (see Options.parse):
	// random 31-bit value, hex-encoded, like the official client.
	var scidBytes [4]byte
	if _, err := rand.Read(scidBytes[:]); err != nil {
		return nil, err
	}
	scid := strconv.FormatUint(uint64(binary.BigEndian.Uint32(scidBytes[:])&0x7fffffff), 16)
	out, err := runAdb(ctx, adb, "-s", serial, "forward", "tcp:0", "localabstract:scrcpy_"+scid)
	if err != nil {
		return nil, fmt.Errorf("adb forward: %w: %s", err, strings.TrimSpace(string(out)))
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || port == 0 {
		return nil, fmt.Errorf("adb forward: no port: %s", strings.TrimSpace(string(out)))
	}
	proc := exec.Command(adb, "-s", serial, "shell",
		"CLASSPATH="+remote, "app_process", "/",
		"com.genymobile.scrcpy.Server", Version,
		"scid="+scid, "log_level=warn", "tunnel_forward=true",
		"audio=false", "control=true", "video_codec=h264",
		"max_size=1280", "max_fps=60", "video_bit_rate=6000000",
		"clipboard_autosync=false", "power_on=true", "cleanup=true",
	)
	// Server stderr is the only witness when the device side dies early.
	errLog, err := os.CreateTemp("", "sim-go-scrcpy-"+scid+"-*.log")
	if err != nil {
		return nil, fmt.Errorf("scrcpy errlog: %w", err)
	}
	errLogPath := errLog.Name()
	proc.Stderr = errLog
	if err := proc.Start(); err != nil {
		_ = errLog.Close()
		return nil, fmt.Errorf("start scrcpy server: %w", err)
	}
	s := &Session{Serial: serial, adb: adb, port: port, process: proc, errLog: errLog, errLogPath: errLogPath}
	video, err := dialVideo(port)
	if err != nil {
		serr := s.ServerError()
		s.Close()
		if serr != "" {
			return nil, fmt.Errorf("scrcpy server never listened: %s", serr)
		}
		return nil, err
	}
	s.video = video
	// Control must connect BEFORE the handshake completes: the server only
	// sends device/codec meta after both sockets are accepted. Connecting
	// control after reading meta deadlocks both sides.
	control, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 10*time.Second)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("scrcpy control socket: %w", err)
	}
	s.control = control
	go drain(control) // device messages (clipboard, ...) are unused
	_ = s.video.SetReadDeadline(time.Now().Add(30 * time.Second))
	meta, err := readHandshake(video)
	_ = s.video.SetReadDeadline(time.Time{})
	if err != nil {
		serr := s.ServerError()
		s.Close()
		if serr != "" {
			return nil, fmt.Errorf("scrcpy handshake: %w: %s", err, serr)
		}
		return nil, fmt.Errorf("scrcpy handshake: %w", err)
	}
	s.Meta = meta
	return s, nil
}

// Start opens a session against a connected emulator serial.
func Start(ctx context.Context, serial string) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	type result struct {
		s   *Session
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := start(ctx, serial)
		ch <- result{s, err}
	}()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("scrcpy start timed out: %w", ctx.Err())
	}
}

// dialVideo connects and waits for the dummy byte, which proves the device
// server (not just the adb forward) accepted the connection.
func dialVideo(port int) (net.Conn, error) {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			time.Sleep(150 * time.Millisecond)
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var dummy [1]byte
		if _, err := readFull(conn, dummy[:]); err != nil {
			_ = conn.Close()
			time.Sleep(150 * time.Millisecond)
			continue
		}
		_ = conn.SetReadDeadline(time.Time{})
		return conn, nil
	}
	return nil, fmt.Errorf("scrcpy server did not accept a connection")
}

// Close kills the server process and removes the adb forward. Idempotent.
func (s *Session) Close() {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.video != nil {
		_ = s.video.Close()
	}
	if s.control != nil {
		_ = s.control.Close()
	}
	if s.process != nil && s.process.Process != nil {
		_ = s.process.Process.Kill()
		_ = s.process.Wait()
	}
	if s.errLog != nil {
		_ = s.errLog.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = runAdb(ctx, s.adb, "-s", s.Serial, "forward", "--remove", "tcp:"+strconv.Itoa(s.port))
}

// ServerError returns the device server's stderr (empty when it never spoke).
func (s *Session) ServerError() string {
	if s.errLogPath == "" {
		return ""
	}
	out, err := os.ReadFile(s.errLogPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func runAdb(ctx context.Context, adb string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, adb, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("adb %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
