// Package herdr reports octbox's state to the herdr pane it runs in, over
// herdr's socket API: newline-delimited JSON on a Unix domain socket.
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/octbox/pkg/domain/interfaces"
	"github.com/m-mizutani/octbox/pkg/utils/safe"
)

const (
	source    = "octbox" // identifies the integration; must not start with "herdr:"
	agentName = "octbox" // shown in the herdr sidebar
	// requestTimeout bounds one request, from connecting to reading the
	// response. ShowUpdate runs on the syncer goroutine, so a hung herdr
	// delays the next sync by at most two requests.
	requestTimeout = 2 * time.Second

	stateIdle    = "idle"
	stateWorking = "working"
)

// Client implements interfaces.UpdateIndicator for one herdr pane. herdr
// shows an agent as done when it goes from working to idle while its pane is
// not viewed, and back to idle once the user views it.
type Client struct {
	socketPath string
	paneID     string
	timeout    time.Duration
	lastID     atomic.Uint64
}

var _ interfaces.UpdateIndicator = (*Client)(nil)

func New(socketPath, paneID string) *Client {
	return &Client{socketPath: socketPath, paneID: paneID, timeout: requestTimeout}
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

// reportAgentParams carries no seq: herdr ignores a seq not higher than the
// last one accepted for the pane, so a restarted octbox would be ignored.
// Requests are sent one at a time, which keeps them in order.
type reportAgentParams struct {
	PaneID string `json:"pane_id"`
	Source string `json:"source"`
	Agent  string `json:"agent"`
	State  string `json:"state"`
}

type releaseAgentParams struct {
	PaneID string `json:"pane_id"`
	Source string `json:"source"`
	Agent  string `json:"agent"`
}

type response struct {
	ID    string         `json:"id"`
	Error *responseError `json:"error"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Register reports the pane idle, which shows octbox with no update.
func (x *Client) Register(ctx context.Context) error {
	return x.report(ctx, stateIdle)
}

// ShowUpdate reports working and then idle: herdr derives done from that
// transition, as it does not accept done as a reported state.
func (x *Client) ShowUpdate(ctx context.Context) error {
	if err := x.report(ctx, stateWorking); err != nil {
		return err
	}
	return x.report(ctx, stateIdle)
}

func (x *Client) Unregister(ctx context.Context) error {
	return x.send(ctx, "pane.release_agent", releaseAgentParams{
		PaneID: x.paneID, Source: source, Agent: agentName,
	})
}

func (x *Client) report(ctx context.Context, state string) error {
	return x.send(ctx, "pane.report_agent", reportAgentParams{
		PaneID: x.paneID, Source: source, Agent: agentName, State: state,
	})
}

// send writes one request on a new connection and reads its response line.
func (x *Client) send(ctx context.Context, method string, params any) error {
	ctx, cancel := context.WithTimeout(ctx, x.timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", x.socketPath)
	if err != nil {
		return goerr.Wrap(err, "failed to connect to herdr",
			goerr.V("socket_path", x.socketPath), goerr.V("method", method))
	}
	defer safe.Close(ctx, conn)
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return goerr.Wrap(err, "failed to set the herdr request deadline", goerr.V("method", method))
		}
	}

	req := request{ID: "octbox-" + strconv.FormatUint(x.lastID.Add(1), 10), Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return goerr.Wrap(err, "failed to encode the herdr request", goerr.V("method", method))
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return goerr.Wrap(err, "failed to send the herdr request", goerr.V("method", method))
	}

	raw, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return goerr.Wrap(err, "failed to read the herdr response", goerr.V("method", method))
	}
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return goerr.Wrap(err, "invalid herdr response", goerr.V("method", method))
	}
	if resp.ID != req.ID {
		return goerr.New("herdr answered another request",
			goerr.V("method", method), goerr.V("request_id", req.ID), goerr.V("response_id", resp.ID))
	}
	if resp.Error != nil {
		return goerr.New("herdr rejected the request",
			goerr.V("method", method), goerr.V("code", resp.Error.Code), goerr.V("message", resp.Error.Message))
	}
	return nil
}
