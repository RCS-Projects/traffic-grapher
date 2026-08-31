package main

import (
	"testing"
	"time"
)

func TestHistoryKeepsOnlyConfiguredWindow(t *testing.T) {
	app := &App{}
	now := time.Now().UnixMilli()
	app.recordSample(Sample{TS: now - (historyWindow + time.Minute).Milliseconds()})
	app.recordSample(Sample{TS: now})
	history := app.historySnapshot()
	if len(history) != 1 || history[0].TS != now {
		t.Fatalf("unexpected retained history: %#v", history)
	}
}
