//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketSamples(t *testing.T) {
	initConfig()
	app := NewApp()

	staticFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		t.Fatalf("static fs: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/scan", app.handleScan)
	mux.HandleFunc("/api/start", app.handleStart)
	mux.HandleFunc("/api/stop", app.handleStop)
	mux.HandleFunc("/ws", app.handleWebSocket)
	mux.Handle("/", spaHandler(staticFS))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() { _ = http.Serve(ln, mux) }()
	base := "http://" + ln.Addr().String()

	// Scan the firewall.
	res, err := http.Post(base+"/api/scan", "application/json",
		bytes.NewReader(mustJSON(map[string]interface{}{"ip": "192.168.255.1", "community": "public", "version": "v2c", "port": 161})))
	if err != nil {
		t.Fatalf("scan post: %v", err)
	}
	var scan ScanResult
	if err := json.NewDecoder(res.Body).Decode(&scan); err != nil {
		t.Fatalf("scan decode: %v", err)
	}
	res.Body.Close()
	if len(scan.Interfaces) == 0 {
		t.Fatal("no interfaces found")
	}

	// Pick the first up physical interface.
	var picked InterfaceInfo
	for _, i := range scan.Interfaces {
		if i.Status == 1 && i.Speed > 0 {
			picked = i
			break
		}
	}
	if picked.Name == "" {
		picked = scan.Interfaces[0]
	}

	cfg := Config{
		Devices:  []Device{scan.Device},
		Interval: 2,
		Selected: []string{ifaceKey(scan.Device.ID, picked.Index)},
	}
	if _, err := postJSON(base+"/api/config", cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Connect WebSocket.
	wsURL := url.URL{Scheme: "ws", Host: ln.Addr().String(), Path: "/ws"}
	c, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer c.Close()

	// Expect config message.
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfgMsg map[string]interface{}
	if err := json.Unmarshal(msg, &cfgMsg); err != nil {
		t.Fatalf("config unmarshal: %v", err)
	}
	if cfgMsg["type"] != "config" {
		t.Fatalf("expected config message, got %s", cfgMsg["type"])
	}

	// Start monitoring.
	if _, err := postJSON(base+"/api/start", nil); err != nil {
		t.Fatalf("start: %v", err)
	}

	// A monitoring-status event is broadcast when Start succeeds. Read through
	// status/config frames until the first actual traffic sample arrives.
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var sampleMsg map[string]interface{}
	for {
		_, msg, err = c.ReadMessage()
		if err != nil {
			t.Fatalf("read sample: %v", err)
		}
		if err := json.Unmarshal(msg, &sampleMsg); err != nil {
			t.Fatalf("sample unmarshal: %v", err)
		}
		if sampleMsg["type"] == "sample" {
			break
		}
	}
	t.Logf("got sample: %s", string(msg))
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func postJSON(path string, v interface{}) (*http.Response, error) {
	var body io.Reader
	if v != nil {
		body = bytes.NewReader(mustJSON(v))
	}
	return http.Post(path, "application/json", body)
}
