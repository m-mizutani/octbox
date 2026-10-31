package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/octbox/pkg/adapter/herdr"
)

type received struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// fakeHerdr accepts one request per connection, like herdr's socket API, and
// answers it with reply.
type fakeHerdr struct {
	path  string
	reply func(req received) string // "" closes without answering

	mu          sync.Mutex
	requests    []received
	connections int
}

func newFakeHerdr(t *testing.T, reply func(req received) string) *fakeHerdr {
	t.Helper()
	// t.TempDir() includes the test name and can exceed the 104-byte limit
	// of a Unix socket path on macOS.
	dir, err := os.MkdirTemp("", "hd")
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	f := &fakeHerdr{path: filepath.Join(dir, "s.sock"), reply: reply}
	ln, err := net.Listen("unix", f.path)
	gt.NoError(t, err).Required()

	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.serve(conn)
			}()
		}
	}()
	return f
}

func (f *fakeHerdr) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req received
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.connections++
	f.mu.Unlock()

	answer := f.reply(req)
	if answer == "" {
		return
	}
	_, _ = conn.Write([]byte(answer + "\n"))
}

func (f *fakeHerdr) received() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.requests...)
}

func ok(req received) string {
	return `{"id":"` + req.ID + `","result":{"type":"ok"}}`
}

func params(t *testing.T, req received) map[string]any {
	t.Helper()
	var m map[string]any
	gt.NoError(t, json.Unmarshal(req.Params, &m)).Required()
	return m
}

func TestClient(t *testing.T) {
	ctx := context.Background()

	t.Run("Register reports idle", func(t *testing.T) {
		f := newFakeHerdr(t, ok)
		gt.NoError(t, herdr.New(f.path, "w1:p2").Register(ctx)).Required()

		reqs := f.received()
		gt.A(t, reqs).Length(1).Required()
		gt.Equal(t, reqs[0].Method, "pane.report_agent")
		gt.Equal(t, params(t, reqs[0]), map[string]any{
			"pane_id": "w1:p2", "source": "octbox", "agent": "octbox", "state": "idle",
		})
	})

	t.Run("ShowUpdate reports working then idle", func(t *testing.T) {
		f := newFakeHerdr(t, ok)
		gt.NoError(t, herdr.New(f.path, "w1:p2").ShowUpdate(ctx)).Required()

		reqs := f.received()
		gt.A(t, reqs).Length(2).Required()
		gt.Equal(t, params(t, reqs[0])["state"], any("working"))
		gt.Equal(t, params(t, reqs[1])["state"], any("idle"))
		f.mu.Lock()
		gt.Equal(t, f.connections, 2)
		f.mu.Unlock()
	})

	t.Run("Unregister releases the agent", func(t *testing.T) {
		f := newFakeHerdr(t, ok)
		gt.NoError(t, herdr.New(f.path, "w1:p2").Unregister(ctx)).Required()

		reqs := f.received()
		gt.A(t, reqs).Length(1).Required()
		gt.Equal(t, reqs[0].Method, "pane.release_agent")
		gt.Equal(t, params(t, reqs[0]), map[string]any{
			"pane_id": "w1:p2", "source": "octbox", "agent": "octbox",
		})
	})

	t.Run("request IDs differ and must be echoed", func(t *testing.T) {
		f := newFakeHerdr(t, ok)
		client := herdr.New(f.path, "w1:p2")
		gt.NoError(t, client.Register(ctx)).Required()
		gt.NoError(t, client.Register(ctx)).Required()
		reqs := f.received()
		gt.A(t, reqs).Length(2).Required()
		gt.NotEqual(t, reqs[0].ID, reqs[1].ID)

		other := newFakeHerdr(t, func(received) string { return `{"id":"someone-else","result":{}}` })
		gt.Error(t, herdr.New(other.path, "w1:p2").Register(ctx))
	})

	t.Run("an error response is returned with its code", func(t *testing.T) {
		f := newFakeHerdr(t, func(req received) string {
			return `{"id":"` + req.ID + `","error":{"code":"not_found","message":"pane not found"}}`
		})
		err := herdr.New(f.path, "w1:p2").ShowUpdate(ctx)
		gt.Error(t, err).Required()
		values := goerr.Values(err)
		gt.Equal(t, values["code"], any("not_found"))
		gt.Equal(t, values["method"], any("pane.report_agent"))
		// idle is not sent after working failed.
		gt.A(t, f.received()).Length(1)
	})

	t.Run("a failed idle after working is returned", func(t *testing.T) {
		f := newFakeHerdr(t, func(req received) string {
			var p map[string]any
			_ = json.Unmarshal(req.Params, &p)
			if p["state"] == "idle" {
				return `{"id":"` + req.ID + `","error":{"code":"internal","message":"failed"}}`
			}
			return ok(req)
		})
		gt.Error(t, herdr.New(f.path, "w1:p2").ShowUpdate(ctx))
		reqs := f.received()
		gt.A(t, reqs).Length(2).Required()
		gt.Equal(t, params(t, reqs[1])["state"], any("idle"))
	})

	t.Run("a missing socket is an error", func(t *testing.T) {
		gt.Error(t, herdr.New(filepath.Join(os.TempDir(), "octbox-no-such.sock"), "w1:p2").Register(ctx))
	})

	t.Run("no response times out", func(t *testing.T) {
		release := make(chan struct{})
		f := newFakeHerdr(t, func(received) string {
			<-release
			return ""
		})
		// Registered after newFakeHerdr, so it runs before the fake waits
		// for its connections.
		t.Cleanup(func() { close(release) })
		client := herdr.New(f.path, "w1:p2")
		herdr.SetTimeout(client, 100*time.Millisecond)

		started := time.Now()
		err := client.Register(ctx)
		gt.Error(t, err)
		var netErr net.Error
		gt.True(t, errors.As(err, &netErr) && netErr.Timeout())
		gt.True(t, time.Since(started) < time.Second)
	})

	t.Run("a response that is not JSON is an error", func(t *testing.T) {
		f := newFakeHerdr(t, func(received) string { return "not json" })
		gt.Error(t, herdr.New(f.path, "w1:p2").Register(ctx))
	})

	t.Run("a connection closed before answering is an error", func(t *testing.T) {
		f := newFakeHerdr(t, func(received) string { return "" })
		gt.Error(t, herdr.New(f.path, "w1:p2").Register(ctx))
	})
}
