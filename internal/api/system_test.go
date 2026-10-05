package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/config"
	"github.com/matt-freed/open-plaato-keg/internal/logbuf"
)

func TestSystemEnvRedactsAPIKey(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodGet, "/api/system/env", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "test-api-key") {
		t.Fatalf("the response leaks the API key: %s", rec.Body.String())
	}

	var body struct {
		Settings []config.Setting `json:"settings"`
	}
	a.decode(rec, &body)
	for _, s := range body.Settings {
		if s.Name == "BARHELPER_API_KEY" && !s.Redacted {
			t.Errorf("API key not marked redacted: %+v", s)
		}
	}
}

func TestSystemLogsPagesBySeq(t *testing.T) {
	a := newTestAPI(t)
	log := slog.New(logbuf.NewHandler(slog.NewTextHandler(io.Discard, nil), a.logs))
	log.Info("first")
	log.Warn("second", "keg", "abc")

	var body logsResponse
	a.decode(a.do(http.MethodGet, "/api/system/logs", nil), &body)
	if len(body.Records) != 2 || body.OldestSeq != 1 || body.LatestSeq != 2 || body.Capacity != logbuf.Capacity {
		t.Fatalf("body = %+v", body)
	}

	a.decode(a.do(http.MethodGet, "/api/system/logs?after=1", nil), &body)
	if len(body.Records) != 1 || body.Records[0].Message != "second" || body.Records[0].Level != "WARN" {
		t.Fatalf("after=1 returned %+v", body.Records)
	}

	a.decode(a.do(http.MethodGet, "/api/system/logs?after=2", nil), &body)
	if body.Records == nil || len(body.Records) != 0 {
		t.Fatalf("after=2 returned %+v", body.Records)
	}

	if rec := a.do(http.MethodGet, "/api/system/logs?after=x", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad after: status = %d, want 400", rec.Code)
	}
}
