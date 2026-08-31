package main

import "testing"

func TestNormalizeConfigCreatesDashboardCards(t *testing.T) {
	cfg := Config{
		Selected: []string{"router:4"},
		Groups:   []Group{{Name: "WAN", Members: []Member{{DeviceID: "router", Index: 4}}}},
	}
	normalizeConfig(&cfg)
	if cfg.Interval != 3 {
		t.Fatalf("interval = %d, want default 3", cfg.Interval)
	}
	if len(cfg.DashboardCards) != 2 {
		t.Fatalf("dashboard cards = %d, want 2", len(cfg.DashboardCards))
	}
	if cfg.DashboardCards[0].ID != "interface:router:4" || cfg.DashboardCards[1].ID != "group:WAN" {
		t.Fatalf("unexpected generated cards: %#v", cfg.DashboardCards)
	}
}

func TestNormalizeConfigPreservesSavedCardOptions(t *testing.T) {
	cfg := Config{
		Interval: 5,
		Selected: []string{"router:4"},
		DashboardCards: []DashboardCard{{
			ID: "interface:router:4", SourceType: "interface", SourceKey: "router:4", Visible: false, ScaleMode: "capacity",
		}},
	}
	normalizeConfig(&cfg)
	if len(cfg.DashboardCards) != 1 || cfg.DashboardCards[0].Visible || cfg.DashboardCards[0].ScaleMode != "capacity" {
		t.Fatalf("saved dashboard card was changed: %#v", cfg.DashboardCards)
	}
}

func TestGroupMembersArePollingTargets(t *testing.T) {
	cfg := Config{Groups: []Group{{Name: "WAN", Members: []Member{{DeviceID: "router", Index: 7}}}}}
	if !hasPollingTargets(cfg) {
		t.Fatal("group member should count as a polling target")
	}
	targets := pollingTargets(cfg, map[string]Device{"router": {ID: "router"}})
	if !targets["router"][7] {
		t.Fatalf("group interface missing from polling targets: %#v", targets)
	}
}

func TestLayoutChangesDoNotRestartPoller(t *testing.T) {
	oldCfg := Config{Interval: 3, Devices: []Device{{ID: "router", IP: "192.0.2.1", Community: "public", Version: "v2c", Port: 161, UseHC: true}}, DashboardColumns: 1}
	newCfg := oldCfg
	newCfg.DashboardColumns = 3
	newCfg.Labels = map[string]string{"router:7": "WAN"}
	if pollerRestartRequired(oldCfg, newCfg) {
		t.Fatal("dashboard-only change should not restart polling")
	}
	newCfg.Interval = 1
	if !pollerRestartRequired(oldCfg, newCfg) {
		t.Fatal("interval change should restart polling")
	}
}
