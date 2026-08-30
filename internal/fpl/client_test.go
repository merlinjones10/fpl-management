package fpl

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestRecordBootstrapEmitsEmbeddedMetrics(t *testing.T) {
	var buf bytes.Buffer
	c := New(WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))
	c.recordBootstrap(time.Now().Add(-25*time.Millisecond), &httpError{status: 403})

	var event map[string]any
	if err := json.Unmarshal(buf.Bytes(), &event); err != nil {
		t.Fatalf("decode metric event: %v\n%s", err, buf.String())
	}
	if got, want := event["msg"], "fpl bootstrap fetch"; got != want {
		t.Errorf("message = %v, want %q", got, want)
	}
	if got, want := event["outcome"], "failure"; got != want {
		t.Errorf("outcome = %v, want %q", got, want)
	}
	if got, want := event["http_status"], "403"; got != want {
		t.Errorf("http_status = %v, want %q", got, want)
	}

	aws, ok := event["_aws"].(map[string]any)
	if !ok {
		t.Fatalf("_aws = %#v, want metric metadata", event["_aws"])
	}
	metrics, ok := aws["CloudWatchMetrics"].([]any)
	if !ok || len(metrics) != 1 {
		t.Fatalf("CloudWatchMetrics = %#v, want one metric definition", aws["CloudWatchMetrics"])
	}
	definition, ok := metrics[0].(map[string]any)
	if !ok || definition["Namespace"] != "FPLLeagueBot" {
		t.Errorf("metric definition = %#v, want FPLLeagueBot namespace", metrics[0])
	}
}
