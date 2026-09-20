package barhelper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		// More readings than the queue can hold, against a server that never
		// answers.
		for i := 0; i < queueSize*4; i++ {
			c.KegAmount("keg", float64(i))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("KegAmount blocked")
	}
}
