//go:build integration

package main

import (
	"testing"
)

func TestScanFirewall(t *testing.T) {
	res, err := ScanDevice("192.168.255.1", "public", "v2c", 161)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !res.Device.UseHC {
		t.Logf("warning: firewall reports no 64-bit counters")
	}
	t.Logf("device %s useHC=%v interfaces=%d", res.Device.IP, res.Device.UseHC, len(res.Interfaces))
	for _, i := range res.Interfaces {
		t.Logf("  idx=%d name=%s alias=%s status=%d speed=%d hc=%v", i.Index, i.Name, i.Alias, i.Status, i.Speed, i.HasHC)
	}
}
