// Command sim-serve previews sim-go's SDK in a browser.
//
// Thin HTTP skin over sdk (the product). Live H.264 for android via
// scrcpy sessions over websocket; no screenshots anywhere on the browser
// path. Single embedded page, no build step, no dependencies.
//
//	go run ./cmd/sim-serve [-addr 127.0.0.1:8790]
//	open http://127.0.0.1:8790
//
// Loopback only, no auth: anything running as your user can drive devices,
// exactly as it could with simctl/adb directly (same trust model as
// simfleet/t3code local servers).
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Adelodunpeter25/sim-go/sdk"
	"github.com/Adelodunpeter25/sim-go/sdk/streamws"
)

//go:embed web/index.html
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8790", "loopback listen address (do not expose to a network)")
	flag.Parse()
	client := sdk.New()
	defer client.Close()
	mux, err := newMux(client)
	if err != nil {
		fmt.Fprintln(os.Stderr, "web:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "sim-serve on http://%s\n", *addr)
	srv := &http.Server{Addr: *addr, Handler: mux}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		<-sigs
		// Close first so companions die even if open streams stall Shutdown.
		client.Close()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		close(done)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
	<-done
}

func newMux(c *sdk.Client) (*http.ServeMux, error) {
	mux := http.NewServeMux()

	web, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	mux.Handle("/", http.FileServer(http.FS(web)))

	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	ok := func(w http.ResponseWriter, extra map[string]any) {
		out := map[string]any{"ok": true}
		for k, v := range extra {
			out[k] = v
		}
		write(w, 200, out)
	}
	fail := func(w http.ResponseWriter, err error) {
		write(w, 500, map[string]any{"ok": false, "error": err.Error()})
	}
	decode := func(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, fmt.Errorf("bad JSON: %w", err))
			return nil, false
		}
		if r.Method != "POST" {
			fail(w, fmt.Errorf("want POST"))
			return nil, false
		}
		return body, true
	}
	str := func(body map[string]any, key string) string {
		s, _ := body[key].(string)
		return s
	}
	num := func(body map[string]any, key string, def int) int {
		if f, ok := body[key].(float64); ok {
			return int(f)
		}
		if s, ok := body[key].(string); ok {
			if n, err := strconv.Atoi(s); err == nil {
				return n
			}
		}
		return def
	}
	ctx := func(r *http.Request) context.Context {
		// No artificial timeout: the server cancels on disconnect, and
		// drivers bound their own waits (boot poll, spawn timeouts).
		return r.Context()
	}

	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		devs, err := c.ListAll(ctx(r))
		if err != nil {
			fail(w, err)
			return
		}
		if devs == nil {
			write(w, 200, []any{})
			return
		}
		write(w, 200, devs)
	})
	mux.HandleFunc("/api/doctor", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, c.Doctor(ctx(r)))
	})
	lifecycle := func(op string, call func(context.Context, string, string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, good := decode(w, r)
			if !good {
				return
			}
			if err := call(ctx(r), str(body, "platform"), str(body, "id")); err != nil {
				fail(w, err)
				return
			}
			ok(w, nil)
		}
	}
	mux.HandleFunc("/api/boot", lifecycle("boot", c.Boot))
	mux.HandleFunc("/api/shutdown", lifecycle("shutdown", c.Shutdown))
	mux.HandleFunc("/api/slim", lifecycle("slim", c.Slim))
	mux.HandleFunc("/api/restore", lifecycle("restore", c.Restore))
	mux.HandleFunc("/api/normalize", lifecycle("normalize", c.Normalize))
	mux.HandleFunc("/api/launch", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		out, err := c.Launch(ctx(r), str(body, "platform"), str(body, "id"), str(body, "app"))
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"output": out})
	})
	appOp := func(call func(context.Context, string, string, string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, good := decode(w, r)
			if !good {
				return
			}
			if err := call(ctx(r), str(body, "platform"), str(body, "id"), str(body, "app")); err != nil {
				fail(w, err)
				return
			}
			ok(w, nil)
		}
	}
	mux.HandleFunc("/api/terminate", appOp(c.Terminate))
	mux.HandleFunc("/api/uninstall", appOp(c.Uninstall))
	mux.HandleFunc("/api/install", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		// path is server-local (preview UI runs on the same host).
		if err := c.Install(ctx(r), str(body, "platform"), str(body, "id"), str(body, "path")); err != nil {
			fail(w, err)
			return
		}
		ok(w, nil)
	})
	mux.HandleFunc("/api/tap", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		if err := c.Tap(ctx(r), str(body, "platform"), str(body, "id"), num(body, "x", 0), num(body, "y", 0)); err != nil {
			fail(w, err)
			return
		}
		ok(w, nil)
	})
	mux.HandleFunc("/api/swipe", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		err := c.Swipe(ctx(r), str(body, "platform"), str(body, "id"),
			num(body, "x1", 0), num(body, "y1", 0), num(body, "x2", 0), num(body, "y2", 0), num(body, "ms", 300))
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, nil)
	})
	mux.HandleFunc("/api/type", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		if err := c.Type(ctx(r), str(body, "platform"), str(body, "id"), str(body, "text")); err != nil {
			fail(w, err)
			return
		}
		ok(w, nil)
	})
	mux.HandleFunc("/api/key", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		if err := c.Key(ctx(r), str(body, "platform"), str(body, "id"), str(body, "code")); err != nil {
			fail(w, err)
			return
		}
		ok(w, nil)
	})
	mux.HandleFunc("/api/press", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		if err := c.Press(ctx(r), str(body, "platform"), str(body, "id"), str(body, "button")); err != nil {
			fail(w, err)
			return
		}
		ok(w, nil)
	})
	mux.HandleFunc("/api/open-url", func(w http.ResponseWriter, r *http.Request) {
		body, good := decode(w, r)
		if !good {
			return
		}
		if err := c.OpenURL(ctx(r), str(body, "platform"), str(body, "id"), str(body, "url")); err != nil {
			fail(w, err)
			return
		}
		ok(w, nil)
	})
	mux.Handle("/api/stream", streamws.Handler(c))

	return mux, nil
}
