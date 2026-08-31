package main

import (
	"math"
	"testing"
	"time"
)

func TestComputeDelta(t *testing.T) {
	p := NewPoller(nil)
	now := time.Now()

	// First call sets baseline, returns 0.
	in, out := p.computeDelta("k", 1000, 2000, true, now)
	if in != 0 || out != 0 {
		t.Fatalf("expected 0,0 on first call, got %v,%v", in, out)
	}

	// 1000 bytes over 2 seconds => 4000 bps
	in, out = p.computeDelta("k", 2000, 2500, true, now.Add(2*time.Second))
	if in != 4000 || out != 2000 {
		t.Fatalf("expected 4000,2000 got %v,%v", in, out)
	}

	// 32-bit rollover
	p2 := NewPoller(nil)
	p2.computeDelta("k", math.MaxUint32-100, 0, false, now)
	in, out = p2.computeDelta("k", 200, 0, false, now.Add(time.Second))
	// delta = 100+200+1 = 301
	if in != 301*8 {
		t.Fatalf("expected rollover delta %d got %v", 301*8, in)
	}
}

func TestInterfaceUsesPerPortHighCapacityCounters(t *testing.T) {
	device := Device{UseHC: true, Interfaces: []InterfaceInfo{{Index: 1, HasHC: true}, {Index: 2, HasHC: false}}}
	if !interfaceUsesHC(device, 1) {
		t.Fatal("interface 1 should use 64-bit counters")
	}
	if interfaceUsesHC(device, 2) {
		t.Fatal("interface 2 should fall back to 32-bit counters")
	}
}
