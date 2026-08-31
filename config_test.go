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
