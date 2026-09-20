// Package barhelper forwards keg volume readings to the BarHelper custom keg
// monitor API.
//
// See https://docs.barhelper.app/english/settings/custom-keg-monitor
package barhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/config"
)

// queueSize bounds how many readings may be waiting to be sent. A keg reports
// continuously and BarHelper accepts at most two updates a minute, so a
// backlog means readings are already being superseded.
const queueSize = 16

// requestTimeout caps how long one send may take.
const requestTimeout = 15 * time.Second

// Outcome classifies BarHelper's answer to a send.
type Outcome string

const (
	// OutcomeSuccess means the reading was accepted.
	OutcomeSuccess Outcome = "success"
	// OutcomeAuthError means the API key is wrong.
	OutcomeAuthError Outcome = "auth_error"
	// OutcomeRateLimited means the update was dropped by BarHelper's own limit
	// of two per minute.
	OutcomeRateLimited Outcome = "rate_limited"
	// OutcomeNoSuccessFlag means the request was accepted but not confirmed.
	OutcomeNoSuccessFlag Outcome = "no_success_flag"
	// OutcomeUnexpected covers everything else.
	OutcomeUnexpected Outcome = "unexpected"
)

type reading struct {
	monitorID string
	amount    float64
}

// Client forwards readings in the background.
type Client struct {
	cfg  config.BarHelperConfig
	http *http.Client
	work chan reading
	done chan struct{}
}

// New returns a client for cfg, or nil if the integration is disabled.
func New(cfg config.BarHelperConfig) *Client {
	if !cfg.Enabled {
		return nil
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: requestTimeout},
		work: make(chan reading, queueSize),
		done: make(chan struct{}),
	}
}

// Start begins processing readings until ctx is cancelled.
func (c *Client) Start(ctx context.Context) {
	if c == nil {
		return
	}
	go func() {
		defer close(c.done)
		for {
			select {
			case <-ctx.Done():
				return
			case r := <-c.work:
				c.send(ctx, r)
			}
		}
	}()
}

// Wait blocks until the background worker has stopped.
func (c *Client) Wait() {
	if c == nil {
		return
	}
	<-c.done
}

// KegAmount queues a volume reading for a keg, implementing keg.AmountConsumer.
//
// Queueing rather than sending inline keeps a slow or unreachable BarHelper
// from stalling the TCP ingest path.
func (c *Client) KegAmount(kegID string, amount float64) {
	if c == nil {
		return
	}
	monitorID, ok := c.cfg.Monitors[kegID]
	if !ok {
		// A keg that is not in the mapping is simply not forwarded.
		return
	}

	select {
	case c.work <- reading{monitorID: monitorID, amount: amount}:
	default:
		slog.Warn("dropping a BarHelper update; the queue is full", "monitor", monitorID)
	}
}

// payload is the request body BarHelper expects.
//
// Volume must be a JSON number: BarHelper answers a stringified volume with a
// 500, which silently broke this integration once already.
type payload struct {
	Name   string  `json:"name"`
	Volume float64 `json:"volume"`
	Type   string  `json:"type"`
}

func (c *Client) send(ctx context.Context, r reading) {
	body, err := json.Marshal(payload{Name: r.monitorID, Volume: r.amount, Type: c.cfg.Unit})
	if err != nil {
		slog.Error("failed to build the BarHelper payload", "monitor", r.monitorID, "error", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		slog.Error("failed to build the BarHelper request", "monitor", r.monitorID, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// BarHelper takes the raw key, with no "Bearer" prefix.
	req.Header.Set("Authorization", c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		slog.Error("failed to reach BarHelper", "monitor", r.monitorID, "error", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	logOutcome(Classify(resp.StatusCode, respBody), r, resp.StatusCode, respBody)
}

// Classify interprets a BarHelper response.
func Classify(status int, body []byte) Outcome {
	// An auth failure is reported in the body whatever the status code, so it
	// is checked first.
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("Wrong auth")) {
		return OutcomeAuthError
	}
	if status == http.StatusTooManyRequests {
		return OutcomeRateLimited
	}
	if status >= 200 && status < 300 {
		if reportsSuccess(body) {
			return OutcomeSuccess
		}
		return OutcomeNoSuccessFlag
	}
	return OutcomeUnexpected
}

// reportsSuccess looks for BarHelper's success flag.
//
// The body is not always valid JSON — a successful response has been observed
// with an unquoted stray field — so a decoded check falls back to a textual
// one.
func reportsSuccess(body []byte) bool {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err == nil {
		flag, ok := decoded["success"].(bool)
		return ok && flag
	}
	return successPattern(string(body))
}

func successPattern(s string) bool {
	idx := strings.Index(s, `"success"`)
	if idx < 0 {
		return false
	}
	rest := strings.TrimSpace(s[idx+len(`"success"`):])
	rest = strings.TrimPrefix(rest, ":")
	return strings.HasPrefix(strings.TrimSpace(rest), "true")
}

func logOutcome(outcome Outcome, r reading, status int, body []byte) {
	// The value and its type are logged on every failure path: sending the
	// wrong type is what broke this integration before.
	attrs := []any{
		"monitor", r.monitorID,
		"volume", r.amount,
		"status", status,
		"response", truncate(string(body), 200),
	}
	switch outcome {
	case OutcomeSuccess:
		slog.Info("sent a reading to BarHelper", attrs...)
	case OutcomeRateLimited:
		slog.Warn("BarHelper rate limited this update; it was dropped", attrs...)
	case OutcomeNoSuccessFlag:
		slog.Warn("BarHelper accepted the request but did not confirm it", attrs...)
	case OutcomeAuthError:
		slog.Error("BarHelper rejected the API key", attrs...)
	default:
		slog.Error("unexpected response from BarHelper", attrs...)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("... (%d bytes)", len(s))
}
