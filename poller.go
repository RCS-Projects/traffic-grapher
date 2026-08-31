package main

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Sample is pushed to web clients every poll cycle.
type Sample struct {
	TS         int64                  `json:"ts"`
	PollMS     int64                  `json:"pollMS"`
	Interfaces map[string]IfaceSample `json:"interfaces"`
	Groups     map[string]GroupSample `json:"groups"`
	Errors     map[string]string      `json:"errors,omitempty"`
}

// IfaceSample holds bandwidth for one interface.
type IfaceSample struct {
	In    float64 `json:"in"`
	Out   float64 `json:"out"`
	Total float64 `json:"total"`
	Name  string  `json:"name"`
}

// GroupSample holds bandwidth for one group.
type GroupSample struct {
	In      float64  `json:"in"`
	Out     float64  `json:"out"`
	Total   float64  `json:"total"`
	Members []string `json:"members"`
}

type lastCounters struct {
	in  uint64
	out uint64
	t   time.Time
}

// Poller polls SNMP devices and broadcasts samples.
type Poller struct {
	app     *App
	mu      sync.Mutex
	ticker  *time.Ticker
	stop    chan struct{}
	last    map[string]*lastCounters
	running bool
}

// NewPoller creates a poller.
func NewPoller(app *App) *Poller {
	return &Poller{
		app:  app,
		last: make(map[string]*lastCounters),
	}
}

// Start (re)starts polling with the given config.
func (p *Poller) Start(cfg Config) {
	p.Stop()
	p.mu.Lock()
	p.last = make(map[string]*lastCounters)
	stop := make(chan struct{})
	p.stop = stop
	interval := time.Duration(cfg.Interval) * time.Second
	if interval <= 0 {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	p.ticker = ticker
	p.running = true
	p.mu.Unlock()

	go func() {
		// Polling in one goroutine avoids overlapping SNMP requests when a
		// device takes longer than the chosen interval to respond.
		p.poll(cfg)
		for {
			select {
			case <-ticker.C:
				p.poll(getConfig())
			case <-stop:
				return
			}
		}
	}()
}

// Stop halts polling.
func (p *Poller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ticker != nil {
		p.ticker.Stop()
		p.ticker = nil
	}
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
	p.running = false
}

// Running reports whether the polling loop is currently active.
func (p *Poller) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Poller) poll(cfg Config) {
	now := time.Now()
	sample := Sample{
		TS:         now.UnixMilli(),
		Interfaces: make(map[string]IfaceSample),
		Groups:     make(map[string]GroupSample),
		Errors:     make(map[string]string),
	}

	deviceMap := make(map[string]Device, len(cfg.Devices))
	for _, d := range cfg.Devices {
		deviceMap[d.ID] = d
	}

	selectedByDevice := make(map[string]map[int]bool)
	for _, key := range cfg.Selected {
		devID, idx, ok := parseIfaceKey(key)
		if !ok {
			continue
		}
		if _, ok := deviceMap[devID]; !ok {
			continue
		}
		if selectedByDevice[devID] == nil {
			selectedByDevice[devID] = make(map[int]bool)
		}
		selectedByDevice[devID][idx] = true
	}

	for devID, sel := range selectedByDevice {
		dev := deviceMap[devID]
		curIn, curOut, err := p.pollDevice(dev, sel)
		if err != nil {
			sample.Errors[devID] = err.Error()
			continue
		}

		for idx := range sel {
			ci, okIn := curIn[idx]
			co, okOut := curOut[idx]
			if !okIn || !okOut {
				// Interface may be down / counters not returned this cycle.
				continue
			}
			key := ifaceKey(devID, idx)
			name := fmt.Sprintf("%s/%d", devID, idx)
			inbps, outbps := p.computeDelta(key, ci, co, dev.UseHC, now)
			total := inbps + outbps
			sample.Interfaces[key] = IfaceSample{
				In:    inbps,
				Out:   outbps,
				Total: total,
				Name:  name,
			}
		}
	}

	// Compute groups.
	for _, g := range cfg.Groups {
		gs := GroupSample{Members: make([]string, 0, len(g.Members))}
		for _, m := range g.Members {
			key := ifaceKey(m.DeviceID, m.Index)
			if s, ok := sample.Interfaces[key]; ok {
				gs.In += s.In
				gs.Out += s.Out
				gs.Members = append(gs.Members, key)
			}
		}
		gs.Total = gs.In + gs.Out
		sample.Groups[g.Name] = gs
	}

	if p.app != nil {
		sample.PollMS = time.Since(now).Milliseconds()
		p.app.Broadcast(map[string]interface{}{"type": "sample", "data": sample})
	}
}

func (p *Poller) pollDevice(dev Device, selected map[int]bool) (map[int]uint64, map[int]uint64, error) {
	g := newSNMP(dev.IP, dev.Community, dev.Version, dev.Port)
	if err := g.Connect(); err != nil {
		return nil, nil, err
	}
	defer g.Conn.Close()

	inOID := oidIfInOctets
	outOID := oidIfOutOctets
	if dev.UseHC {
		inOID = oidIfHCInOctets
		outOID = oidIfHCOutOctets
	}

	curIn := make(map[int]uint64, len(selected))
	curOut := make(map[int]uint64, len(selected))
	// A GET only asks the device for counters that are actively graphed. It is
	// considerably lighter and faster than walking both complete interface tables.
	requests := make([]string, 0, len(selected)*2)
	inRequests := make(map[string]int, len(selected))
	outRequests := make(map[string]int, len(selected))
	for idx := range selected {
		in := fmt.Sprintf("%s.%d", inOID, idx)
		out := fmt.Sprintf("%s.%d", outOID, idx)
		requests = append(requests, in, out)
		inRequests[in] = idx
		outRequests[out] = idx
	}
	// Keep requests comfortably within small SNMP agents' PDU limits.
	for start := 0; start < len(requests); start += 16 {
		end := start + 16
		if end > len(requests) {
			end = len(requests)
		}
		result, err := g.Get(requests[start:end])
		if err != nil {
			return nil, nil, fmt.Errorf("counter get: %w", err)
		}
		for _, pdu := range result.Variables {
			name := strings.TrimPrefix(pdu.Name, ".")
			if idx, ok := inRequests[name]; ok {
				if n, ok := toUint64(pdu.Value); ok {
					curIn[idx] = n
				}
			}
			if idx, ok := outRequests[name]; ok {
				if n, ok := toUint64(pdu.Value); ok {
					curOut[idx] = n
				}
			}
		}
	}
	return curIn, curOut, nil
}

func (p *Poller) computeDelta(key string, curIn, curOut uint64, useHC bool, now time.Time) (float64, float64) {
	max := uint64(math.MaxUint32)
	if useHC {
		max = math.MaxUint64
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	l := p.last[key]
	if l == nil {
		p.last[key] = &lastCounters{in: curIn, out: curOut, t: now}
		return 0, 0
	}

	seconds := now.Sub(l.t).Seconds()
	if seconds <= 0 {
		return 0, 0
	}

	delta := func(cur, prev uint64) uint64 {
		if cur >= prev {
			return cur - prev
		}
		return max - prev + cur + 1
	}

	inDelta := delta(curIn, l.in)
	outDelta := delta(curOut, l.out)

	l.in = curIn
	l.out = curOut
	l.t = now

	inbps := float64(inDelta*8) / seconds
	outbps := float64(outDelta*8) / seconds
	return inbps, outbps
}
