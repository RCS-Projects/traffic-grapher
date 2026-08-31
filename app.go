package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// App ties together config, SNMP polling and WebSocket clients.
type App struct {
	poller    *Poller
	hub       *Hub
	mu        sync.RWMutex
	historyMu sync.RWMutex
	history   []Sample
}

const historyWindow = 10 * time.Minute

// NewApp creates the app and starts the poller if the saved config has selections.
func NewApp() *App {
	a := &App{hub: newHub()}
	a.poller = NewPoller(a)
	go a.hub.run()

	cfg := getConfig()
	if len(cfg.Devices) > 0 && hasPollingTargets(cfg) {
		a.poller.Start(cfg)
	}
	return a
}

// Broadcast sends a JSON message to every connected web client.
func (a *App) Broadcast(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	a.hub.broadcast <- data
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// GET /api/config  -> current config
// POST /api/config -> save config
func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, getConfig())
	case http.MethodPost:
		oldCfg := getConfig()
		var cfg Config
		if err := readJSON(r, &cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := saveConfig(cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		savedCfg := getConfig()
		if a.poller != nil && a.poller.Running() && pollerRestartRequired(oldCfg, savedCfg) {
			a.poller.Start(savedCfg)
		}
		a.Broadcast(map[string]interface{}{"type": "config", "data": savedCfg})
		a.Broadcast(map[string]interface{}{"type": "monitoring", "running": a.poller.Running()})
		writeJSON(w, http.StatusOK, savedCfg)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// GET /api/status reports whether SNMP sampling is active.
func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": a.poller.Running()})
}

// POST /api/scan {ip, community, version, port}
func (a *App) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		IP        string `json:"ip"`
		Community string `json:"community"`
		Version   string `json:"version"`
		Port      uint16 `json:"port"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := ScanDevice(req.IP, req.Community, req.Version, req.Port)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /api/start
func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := getConfig()
	if !hasPollingTargets(cfg) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "select at least one interface before starting monitoring"})
		return
	}
	a.poller.Start(cfg)
	a.Broadcast(map[string]interface{}{"type": "monitoring", "running": true})
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// POST /api/stop
func (a *App) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a.poller.Stop()
	a.Broadcast(map[string]interface{}{"type": "monitoring", "running": false})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// ---------- WebSocket ----------

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (a *App) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}
	client := &Client{hub: a.hub, conn: conn, send: make(chan []byte, 256)}
	a.hub.register <- client

	// Send current config on connect so the UI can stay in sync.
	cfg := getConfig()
	if data, err := json.Marshal(map[string]interface{}{"type": "config", "data": cfg}); err == nil {
		select {
		case client.send <- data:
		default:
		}
	}
	if data, err := json.Marshal(map[string]interface{}{"type": "monitoring", "running": a.poller.Running()}); err == nil {
		select {
		case client.send <- data:
		default:
		}
	}
	if data, err := json.Marshal(map[string]interface{}{"type": "history", "data": a.historySnapshot()}); err == nil {
		select {
		case client.send <- data:
		default:
		}
	}

	go client.writePump()
	go client.readPump()
}

func (a *App) publishSample(sample Sample) {
	a.recordSample(sample)
	a.Broadcast(map[string]interface{}{"type": "sample", "data": sample})
}

func (a *App) recordSample(sample Sample) {
	cutoff := sample.TS - historyWindow.Milliseconds()
	a.historyMu.Lock()
	a.history = append(a.history, sample)
	first := 0
	for first < len(a.history) && a.history[first].TS < cutoff {
		first++
	}
	if first > 0 {
		a.history = append([]Sample(nil), a.history[first:]...)
	}
	a.historyMu.Unlock()
}

func (a *App) historySnapshot() []Sample {
	a.historyMu.RLock()
	defer a.historyMu.RUnlock()
	return append([]Sample(nil), a.history...)
}

// ---------- Hub ----------

type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

func newHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// ---------- Client ----------

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
)

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("websocket error: %v", err)
			}
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.WriteMessage(websocket.TextMessage, message)
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
