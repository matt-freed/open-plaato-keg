package barhelper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/config"
)

// An actual BarHelper success body, including the stray unquoted field that
// makes it invalid JSON.
const successBody = `{"volume: 15.89" "prevKegAmount": 15.87, "newKegAmount": 15.89, "success": true, "message": "Keg Monitor updated successfully"}`

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   Outcome
	}{
		{"real success body", 200, successBody, OutcomeSuccess},
		{"minimal success", 200, `{"success":true}`, OutcomeSuccess},
		{"spaced success", 200, `{"success": true}`, OutcomeSuccess},
		{"explicit failure", 200, `{"success": false}`, OutcomeNoSuccessFlag},
		{"no flag at all", 200, `{}`, OutcomeNoSuccessFlag},
		{"rate limited", 429, "Too many requests, max 2 per minute.", OutcomeRateLimited},
		// An auth failure is reported in the body even with a 200.
		{"auth error", 200, "Wrong auth token", OutcomeAuthError},
		{"auth error with error status", 401, "Wrong auth token", OutcomeAuthError},
		{"server error", 500, "Internal Server Error", OutcomeUnexpected},
		{"json error body", 500, `{"error":"boom"}`, OutcomeUnexpected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.status, []byte(tt.body)); got != tt.want {
				t.Errorf("Classify(%d, %q) = %q, want %q", tt.status, tt.body, got, tt.want)
			}
		})
	}
}

func TestNewReturnsNilWhenDisabled(t *testing.T) {
	if c := New(config.BarHelperConfig{Enabled: false}); c != nil {
		t.Error("New returned a client for a disabled integration")
	}
}

// A nil client must be safe to use, so callers need no enabled check.
func TestNilClientIsSafe(t *testing.T) {
	var c *Client
	c.Start(context.Background())
	c.KegAmount("keg", 1.0)
	c.Wait()
}

func newTestClient(t *testing.T, handler http.HandlerFunc, monitors map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := New(config.BarHelperConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		APIKey:   "test-key",
		Unit:     "l",
		Monitors: monitors,
	})
	if c == nil {
		t.Fatal("New returned nil for an enabled integration")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		c.Wait()
	})
	c.Start(ctx)
	return c
}

func TestSendsVolumeAsANumber(t *testing.T) {
	type received struct {
		auth        string
		contentType string
		body        map[string]any
	}
	got := make(chan received, 1)

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got <- received{
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		}
		w.Write([]byte(successBody))
	}, map[string]string{"keg-token": "monitor-1"})

	c.KegAmount("keg-token", 15.89)

	select {
	case r := <-got:
		// The raw key, with no "Bearer" prefix.
		if r.auth != "test-key" {
			t.Errorf("Authorization = %q, want the raw key", r.auth)
		}
		if r.contentType != "application/json" {
			t.Errorf("Content-Type = %q", r.contentType)
		}
		if r.body["name"] != "monitor-1" {
			t.Errorf("name = %v, want monitor-1", r.body["name"])
		}
		if r.body["type"] != "l" {
			t.Errorf("type = %v, want l", r.body["type"])
		}
		// BarHelper answers a stringified volume with a 500, so this must be a
		// JSON number rather than a string.
		volume, ok := r.body["volume"].(float64)
		if !ok {
			t.Fatalf("volume = %#v (%T), want a JSON number", r.body["volume"], r.body["volume"])
		}
		if volume != 15.89 {
			t.Errorf("volume = %v, want 15.89", volume)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no request was sent")
	}
}

// A keg that is not in the monitor mapping must never be forwarded.
func TestUnmappedKegIsNotSent(t *testing.T) {
	called := make(chan struct{}, 1)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.Write([]byte(`{"success":true}`))
	}, map[string]string{"known-keg": "monitor-1"})

	c.KegAmount("unknown-keg", 5.0)

	select {
	case <-called:
		t.Fatal("a request was sent for an unmapped keg")
	case <-time.After(250 * time.Millisecond):
	}
}

// An unreachable or slow BarHelper must not block the caller, which is on the
// TCP ingest path.
func TestKegAmountDoesNotBlock(t *testing.T) {
	release := make(chan struct{})

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Also return when the client goes away. Shutting the test server down
		// waits for its handlers, so a handler that only watched release would
		// deadlock against the cleanup that closes it.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, map[string]string{"keg": "monitor-1"})

	// Registered after newTestClient so it runs before that helper's cleanups:
	// the handler is released before anything waits on it.
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		// A burst far larger than a pour produces, against a server that never
		// answers.
		for i := 0; i < 10000; i++ {
			c.KegAmount("keg", float64(i))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("KegAmount blocked")
	}
}

// A burst of readings must collapse into a single request carrying the most
// recent volume. A keg emits dozens of readings a second during a pour, and
// BarHelper accepts two a minute.
func TestBurstCollapsesToOneRequest(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []map[string]any
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Write([]byte(`{"success":true}`))
	}, map[string]string{"keg": "monitor-1"})

	for i := 1; i <= 50; i++ {
		c.KegAmount("keg", float64(i))
	}

	waitFor(t, "the coalesced reading to be sent", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(bodies) == 1
	})

	// Nothing further may go out inside the interval.
	time.Sleep(2 * flushInterval)

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("sent %d requests for one burst, want 1", len(bodies))
	}
	if bodies[0]["volume"] != float64(50) {
		t.Errorf("volume = %v, want the most recent reading 50", bodies[0]["volume"])
	}
}

// Re-reporting the same volume must not produce a second request; a resting
// keg reports its unchanged weight indefinitely.
func TestUnchangedVolumeIsNotResent(t *testing.T) {
	var (
		mu    sync.Mutex
		count int
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.Write([]byte(`{"success":true}`))
	}, map[string]string{"keg": "monitor-1"})

	c.KegAmount("keg", 15.5)
	waitFor(t, "the first reading to be sent", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return count == 1
	})

	// Keep reporting the same value well past the send interval.
	deadline := time.Now().Add(3 * flushInterval)
	for time.Now().Before(deadline) {
		c.KegAmount("keg", 15.5)
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("sent %d requests for an unchanged volume, want 1", count)
	}
}

// A reading that fails to send must be retried rather than silently lost.
func TestFailedSendIsRetried(t *testing.T) {
	var (
		mu       sync.Mutex
		attempts int
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			// BarHelper rate limited this one.
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Too many requests, max 2 per minute."))
			return
		}
		w.Write([]byte(`{"success":true}`))
	}, map[string]string{"keg": "monitor-1"})

	// A short interval keeps the test quick; the production value is
	// MinSendInterval.
	c.minSendInterval = 50 * time.Millisecond

	c.KegAmount("keg", 15.5)

	waitFor(t, "the rejected reading to be retried", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts >= 2
	})
}

// newOfflineClient builds a client for tests that drive due directly and
// never send anything.
func newOfflineClient(t *testing.T, monitors map[string]string) *Client {
	t.Helper()
	c := New(config.BarHelperConfig{
		Enabled:  true,
		Endpoint: "http://127.0.0.1:1",
		APIKey:   "test-key",
		Unit:     "l",
		Monitors: monitors,
	})
	if c == nil {
		t.Fatal("New returned nil for an enabled integration")
	}
	return c
}

// names lists the monitors in a batch of readings, for failure messages.
func names(rs []reading) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.monitorID
	}
	return out
}

// recorder captures which monitors were sent, in order.
type recorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *recorder) handler(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	r.mu.Lock()
	r.sent = append(r.sent, body.Name)
	r.mu.Unlock()

	w.Write([]byte(`{"success":true}`))
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

// monitorsFor builds a keg-to-monitor mapping of the given size.
func monitorsFor(n int) map[string]string {
	m := make(map[string]string, n)
	for i := 1; i <= n; i++ {
		m[fmt.Sprintf("keg%d", i)] = fmt.Sprintf("monitor%d", i)
	}
	return m
}

// BarHelper's limit applies to the API key as a whole, so the total across
// every monitor has to stay inside it however many kegs are connected.
func TestGlobalBudgetCapsTotalRate(t *testing.T) {
	rec := &recorder{}
	c := newTestClient(t, rec.handler, monitorsFor(7))

	// Short timings so the test does not wait out the production window.
	c.globalWindow = 3 * time.Second
	c.minSendInterval = 10 * time.Millisecond

	// Every keg reports a changing volume throughout, as a bar full of
	// settling scales does.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for v := 1.0; ; v++ {
			select {
			case <-stop:
				return
			default:
			}
			for i := 1; i <= 7; i++ {
				c.KegAmount(fmt.Sprintf("keg%d", i), v+float64(i)/100)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	waitFor(t, "the first window to be spent", func() bool {
		return len(rec.snapshot()) >= GlobalSendLimit
	})
	// Well inside the window, with every keg still reporting changes.
	time.Sleep(2 * time.Second)

	if got := len(rec.snapshot()); got > GlobalSendLimit {
		t.Errorf("sent %d requests inside one %v window, want at most %d",
			got, c.globalWindow, GlobalSendLimit)
	}
}

// The shared budget must go to whichever monitors have waited longest.
//
// This drives due directly rather than going through the worker, so the
// ordering is asserted exactly instead of being inferred from timing.
func TestDueServesTheLongestWaitingMonitors(t *testing.T) {
	c := newOfflineClient(t, monitorsFor(6))
	// The per-monitor interval is not what this test is about.
	c.minSendInterval = 0

	now := time.Now()
	ages := map[string]time.Duration{
		"monitor1": 1 * time.Minute,
		"monitor2": 9 * time.Minute,
		"monitor3": 3 * time.Minute,
		"monitor4": 7 * time.Minute,
		"monitor5": 2 * time.Minute,
		"monitor6": 5 * time.Minute,
	}
	for monitorID, age := range ages {
		c.state[monitorID] = &monitorState{
			latest:     1.0,
			haveLatest: true,
			lastSendAt: now.Add(-age),
		}
	}

	got := c.due(now)
	if len(got) != GlobalSendLimit {
		t.Fatalf("due returned %d readings, want %d", len(got), GlobalSendLimit)
	}
	// monitor2 has waited 9 minutes and monitor4 seven; the rest are newer.
	want := []string{"monitor2", "monitor4"}
	for i, r := range got {
		if r.monitorID != want[i] {
			t.Errorf("position %d = %s, want %s (served %v, want %v)",
				i, r.monitorID, want[i], names(got), want)
		}
	}
}

// Over several windows every monitor must get an equal share. Serving
// whichever monitor the map happened to yield first left real kegs unreported
// for minutes while others were served repeatedly.
func TestBudgetRotatesEvenlyBetweenMonitors(t *testing.T) {
	const (
		monitors = 6
		rounds   = 12
	)
	c := newOfflineClient(t, monitorsFor(monitors))
	c.minSendInterval = 0

	served := map[string]int{}
	now := time.Now()
	for round := 0; round < rounds; round++ {
		// Every keg reports a new volume, as settling scales do.
		for i := 1; i <= monitors; i++ {
			c.KegAmount(fmt.Sprintf("keg%d", i), float64(round)+float64(i)/100)
		}
		for _, r := range c.due(now) {
			served[r.monitorID]++
		}
		// Step past the shared window so the next round has a full budget.
		now = now.Add(c.globalWindow)
	}

	// rounds * GlobalSendLimit sends spread over monitors, exactly even.
	want := rounds * GlobalSendLimit / monitors
	for i := 1; i <= monitors; i++ {
		monitorID := fmt.Sprintf("monitor%d", i)
		if served[monitorID] != want {
			t.Errorf("%s served %d times over %d rounds, want %d (all: %v)",
				monitorID, served[monitorID], rounds, want, served)
		}
	}
}

// A lone keg should still get the full allowance rather than being held to a
// share of it.
func TestSingleMonitorUsesTheWholeBudget(t *testing.T) {
	rec := &recorder{}
	c := newTestClient(t, rec.handler, monitorsFor(1))

	c.globalWindow = 2 * time.Second
	c.minSendInterval = 10 * time.Millisecond

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for v := 1.0; ; v++ {
			select {
			case <-stop:
				return
			default:
			}
			c.KegAmount("keg1", v)
			time.Sleep(20 * time.Millisecond)
		}
	}()

	waitFor(t, "the single monitor to use the full budget", func() bool {
		return len(rec.snapshot()) == GlobalSendLimit
	})
}

// waitFor polls until cond holds, so the tests do not depend on tick timing.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
