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
	"sync"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/config"
)

// MinSendInterval is the shortest gap between updates for a single monitor.
//
// BarHelper accepts at most two updates a minute per monitor, so sending
// faster than this only produces rejections. A keg reports continuously — a
// single scale can emit dozens of volume readings a second during a pour.
const MinSendInterval = 30 * time.Second

// flushInterval is how often pending readings are examined for sending.
const flushInterval = time.Second

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

// monitorState is the latest reading for one monitor and what has been sent.
type monitorState struct {
	// latest is the most recent volume the keg reported.
	latest     float64
	haveLatest bool
	// lastSent is the volume BarHelper last accepted.
	lastSent   float64
	haveSent   bool
	lastSendAt time.Time
}

// Client forwards readings in the background.
//
// Readings are coalesced rather than queued: only the most recent volume per
// monitor is kept, and it is sent at most once per MinSendInterval. That keeps
// the request rate inside BarHelper's limit while still converging on the
// keg's resting volume once a pour finishes.
type Client struct {
	cfg  config.BarHelperConfig
	http *http.Client
	done chan struct{}
	// minSendInterval is MinSendInterval, overridden in tests so they need not
	// wait out the production interval.
	minSendInterval time.Duration

	mu    sync.Mutex
	state map[string]*monitorState
}

// New returns a client for cfg, or nil if the integration is disabled.
func New(cfg config.BarHelperConfig) *Client {
	if !cfg.Enabled {
		return nil
	}
	return &Client{
		cfg:             cfg,
		http:            &http.Client{Timeout: requestTimeout},
		done:            make(chan struct{}),
		minSendInterval: MinSendInterval,
		state:           map[string]*monitorState{},
	}
}

// Start begins processing readings until ctx is cancelled.
func (c *Client) Start(ctx context.Context) {
	if c == nil {
		return
	}
	go func() {
		defer close(c.done)

		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				for _, r := range c.due(now) {
					c.send(ctx, r)
				}
			}
		}
	}()
}

// due returns the readings that should be sent now, marking each monitor as
// attempted so it is not retried until the interval has passed again.
func (c *Client) due(now time.Time) []reading {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []reading
	for monitorID, st := range c.state {
		switch {
		case !st.haveLatest:
			continue
		// Nothing has changed since BarHelper last accepted a value.
		case st.haveSent && st.lastSent == st.latest:
			continue
		case !st.lastSendAt.IsZero() && now.Sub(st.lastSendAt) < c.minSendInterval:
			continue
		}
		st.lastSendAt = now
		out = append(out, reading{monitorID: monitorID, amount: st.latest})
	}
	return out
}

// recordSent notes that BarHelper accepted a value, so an unchanged reading is
// not sent again. A failed send is deliberately not recorded, so it is retried
// once the interval has passed.
func (c *Client) recordSent(monitorID string, amount float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.state[monitorID]; st != nil {
		st.lastSent = amount
		st.haveSent = true
	}
}

// Wait blocks until the background worker has stopped.
func (c *Client) Wait() {
	if c == nil {
		return
	}
	<-c.done
}

// KegAmount records the latest volume for a keg, implementing
// keg.AmountConsumer.
//
// It only updates in-memory state, so it never blocks the TCP ingest path that
// calls it, however slow or unreachable BarHelper happens to be. The
// background worker decides when to send.
func (c *Client) KegAmount(kegID string, amount float64) {
	if c == nil {
		return
	}
	monitorID, ok := c.cfg.Monitors[kegID]
	if !ok {
		// A keg that is not in the mapping is simply not forwarded.
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	st := c.state[monitorID]
	if st == nil {
		st = &monitorState{}
		c.state[monitorID] = st
	}
	st.latest = amount
	st.haveLatest = true
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
	outcome := Classify(resp.StatusCode, respBody)
	if outcome == OutcomeSuccess {
		c.recordSent(r.monitorID, r.amount)
	}
	logOutcome(outcome, r, resp.StatusCode, respBody)
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
