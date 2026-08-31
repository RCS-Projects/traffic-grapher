package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Device is an SNMP target.
type Device struct {
	ID         string          `json:"id"`
	IP         string          `json:"ip"`
	Community  string          `json:"community"`
	Version    string          `json:"version"` // "v1" or "v2c"
	Port       uint16          `json:"port"`
	UseHC      bool            `json:"useHC"`
	Interfaces []InterfaceInfo `json:"interfaces,omitempty"`
}

// DashboardCard describes one configurable live graph on the dashboard.
// SourceKey is an interface key ("device:index") or a group name.
type DashboardCard struct {
	ID         string `json:"id"`
	SourceType string `json:"sourceType"` // "interface" or "group"
	SourceKey  string `json:"sourceKey"`
	Visible    bool   `json:"visible"`
	ScaleMode  string `json:"scaleMode"` // "auto" or "capacity"
	Height     int    `json:"height,omitempty"`
}

// Member identifies one interface inside a group.
type Member struct {
	DeviceID string `json:"deviceID"`
	Index    int    `json:"index"`
}

// Group is a named collection of interfaces whose bandwidth is summed.
type Group struct {
	Name    string   `json:"name"`
	Members []Member `json:"members"`
}

// Config holds persistent app state.
type Config struct {
	Devices           []Device          `json:"devices"`
	Groups            []Group           `json:"groups"`
	DashboardCards    []DashboardCard   `json:"dashboardCards"`
	DashboardColumns  int               `json:"dashboardColumns"`
	GroupMemberTraces *bool             `json:"groupMemberTraces,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"` // interface key -> user-friendly name
	Interval          int               `json:"interval"`         // seconds
	Selected          []string          `json:"selected"`         // keys "deviceID:index"
}

// configPath uses CONFIG_DIR when supplied. Containers mount this directory so
// device settings and dashboard layout survive image upgrades.
func configPath() (string, error) {
	if dir := os.Getenv("CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	ex, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(ex), "config.json"), nil
}

var (
	configMu sync.RWMutex
	config   Config
)

func initConfig() {
	config = Config{
		Interval: 3,
		Selected: []string{},
	}
	_ = loadConfig()
}

func loadConfig() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	configMu.Lock()
	defer configMu.Unlock()
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	normalizeConfig(&config)
	return nil
}

func saveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	configMu.Lock()
	defer configMu.Unlock()
	normalizeConfig(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	config = cfg
	return nil
}

// normalizeConfig keeps older config.json files compatible and supplies cards
// for every selected interface and group that does not yet have one.
func normalizeConfig(cfg *Config) {
	if cfg.Interval <= 0 {
		cfg.Interval = 3
	}
	if cfg.DashboardColumns < 1 || cfg.DashboardColumns > 4 {
		cfg.DashboardColumns = 1
	}
	if cfg.Selected == nil {
		cfg.Selected = []string{}
	}
	if cfg.DashboardCards == nil {
		cfg.DashboardCards = []DashboardCard{}
	}
	if cfg.Labels == nil {
		cfg.Labels = make(map[string]string)
	}

	existing := make(map[string]bool, len(cfg.DashboardCards))
	selected := make(map[string]bool, len(cfg.Selected))
	for _, key := range cfg.Selected {
		selected[key] = true
	}
	for i := range cfg.DashboardCards {
		card := &cfg.DashboardCards[i]
		if card.ID == "" {
			card.ID = card.SourceType + ":" + card.SourceKey
		}
		if card.ScaleMode != "capacity" {
			card.ScaleMode = "auto"
		}
		if card.Height < 140 {
			card.Height = 205
		}
		// A visible interface graph is a persisted monitoring choice. This also
		// migrates layouts written before dashboard cards and selections could
		// briefly fall out of sync.
		if card.SourceType == "interface" && card.Visible && card.SourceKey != "" && !selected[card.SourceKey] {
			cfg.Selected = append(cfg.Selected, card.SourceKey)
			selected[card.SourceKey] = true
		}
		existing[card.ID] = true
	}
	for _, key := range cfg.Selected {
		id := "interface:" + key
		if !existing[id] {
			cfg.DashboardCards = append(cfg.DashboardCards, DashboardCard{ID: id, SourceType: "interface", SourceKey: key, Visible: true, ScaleMode: "auto", Height: 205})
			existing[id] = true
		}
	}
	for _, group := range cfg.Groups {
		id := "group:" + group.Name
		if !existing[id] {
			cfg.DashboardCards = append(cfg.DashboardCards, DashboardCard{ID: id, SourceType: "group", SourceKey: group.Name, Visible: true, ScaleMode: "auto", Height: 205})
			existing[id] = true
		}
	}
}

func getConfig() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	c := config
	c.Devices = make([]Device, len(config.Devices))
	copy(c.Devices, config.Devices)
	c.Groups = make([]Group, len(config.Groups))
	for i, group := range config.Groups {
		c.Groups[i] = group
		c.Groups[i].Members = append([]Member(nil), group.Members...)
	}
	c.DashboardCards = append([]DashboardCard(nil), config.DashboardCards...)
	c.Labels = make(map[string]string, len(config.Labels))
	for key, label := range config.Labels {
		c.Labels[key] = label
	}
	c.Selected = make([]string, len(config.Selected))
	copy(c.Selected, config.Selected)
	return c
}

func hasPollingTargets(cfg Config) bool {
	if len(cfg.Selected) > 0 {
		return true
	}
	for _, group := range cfg.Groups {
		if len(group.Members) > 0 {
			return true
		}
	}
	return false
}

// pollerRestartRequired reports changes that invalidate counter baselines or
// the ticker. Selections, groups, labels, and dashboard layout are read on the
// next poll and do not need to interrupt live monitoring.
func pollerRestartRequired(oldCfg, newCfg Config) bool {
	if oldCfg.Interval != newCfg.Interval || len(oldCfg.Devices) != len(newCfg.Devices) {
		return true
	}
	for i, oldDevice := range oldCfg.Devices {
		newDevice := newCfg.Devices[i]
		if oldDevice.ID != newDevice.ID || oldDevice.IP != newDevice.IP || oldDevice.Community != newDevice.Community || oldDevice.Version != newDevice.Version || oldDevice.Port != newDevice.Port || oldDevice.UseHC != newDevice.UseHC {
			return true
		}
	}
	return false
}

func ifaceKey(deviceID string, index int) string {
	return fmt.Sprintf("%s:%d", deviceID, index)
}

func parseIfaceKey(key string) (string, int, bool) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	var idx int
	if _, err := fmt.Sscanf(parts[1], "%d", &idx); err != nil {
		return "", 0, false
	}
	return parts[0], idx, true
}
