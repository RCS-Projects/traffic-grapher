package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

const (
	oidIfName        = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfAlias       = "1.3.6.1.2.1.31.1.1.1.18"
	oidIfOperStatus  = "1.3.6.1.2.1.2.2.1.8"
	oidIfSpeed       = "1.3.6.1.2.1.2.2.1.5"
	oidIfHighSpeed   = "1.3.6.1.2.1.31.1.1.1.15"
	oidIfHCInOctets  = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets = "1.3.6.1.2.1.31.1.1.1.10"
	oidIfInOctets    = "1.3.6.1.2.1.2.2.1.10"
	oidIfOutOctets   = "1.3.6.1.2.1.2.2.1.16"
)

// InterfaceInfo describes one SNMP interface.
type InterfaceInfo struct {
	DeviceID string `json:"deviceID"`
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Alias    string `json:"alias"`
	Status   int    `json:"status"`
	Speed    uint64 `json:"speed"`
	HasHC    bool   `json:"hasHC"`
}

// ScanResult is returned by ScanDevice.
type ScanResult struct {
	Device     Device          `json:"device"`
	Interfaces []InterfaceInfo `json:"interfaces"`
}

func newSNMP(ip, community, version string, port uint16) *gosnmp.GoSNMP {
	v := gosnmp.Version2c
	if strings.EqualFold(version, "v1") {
		v = gosnmp.Version1
	}
	if port == 0 {
		port = 161
	}
	return &gosnmp.GoSNMP{
		Target:    ip,
		Port:      port,
		Community: community,
		Version:   v,
		Timeout:   3 * time.Second,
		Retries:   1,
	}
}

func oidIndex(name string) (int, error) {
	parts := strings.Split(name, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "" {
			continue
		}
		return strconv.Atoi(parts[i])
	}
	return 0, fmt.Errorf("no index in OID %s", name)
}

func toString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func toUint64(v interface{}) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case uint32:
		return uint64(n), true
	case uint:
		return uint64(n), true
	case int64:
		return uint64(n), true
	case int:
		return uint64(n), true
	case string:
		u, err := strconv.ParseUint(n, 10, 64)
		if err == nil {
			return u, true
		}
	}
	return 0, false
}

// ScanDevice walks the interface table and returns what it found.
func ScanDevice(ip, community, version string, port uint16) (ScanResult, error) {
	id := ip
	g := newSNMP(ip, community, version, port)
	if err := g.Connect(); err != nil {
		return ScanResult{}, fmt.Errorf("connect: %w", err)
	}
	defer g.Conn.Close()

	ifaces := map[int]*InterfaceInfo{}

	walk := func(root string, setter func(idx int, pdu gosnmp.SnmpPDU)) error {
		var result []gosnmp.SnmpPDU
		var err error
		if g.Version == gosnmp.Version1 {
			// SNMPv1 has no GETBULK operation; WalkAll uses GETNEXT.
			result, err = g.WalkAll(root)
		} else {
			result, err = g.BulkWalkAll(root)
		}
		if err != nil {
			return err
		}
		for _, pdu := range result {
			idx, err := oidIndex(pdu.Name)
			if err != nil {
				continue
			}
			setter(idx, pdu)
		}
		return nil
	}

	if err := walk(oidIfName, func(idx int, pdu gosnmp.SnmpPDU) {
		ifaces[idx] = &InterfaceInfo{DeviceID: id, Index: idx, Name: toString(pdu.Value)}
	}); err != nil {
		return ScanResult{}, fmt.Errorf("ifName: %w", err)
	}
	if err := walk(oidIfAlias, func(idx int, pdu gosnmp.SnmpPDU) {
		if i, ok := ifaces[idx]; ok {
			i.Alias = toString(pdu.Value)
		}
	}); err != nil {
		return ScanResult{}, fmt.Errorf("ifAlias: %w", err)
	}
	if err := walk(oidIfOperStatus, func(idx int, pdu gosnmp.SnmpPDU) {
		if n, ok := toUint64(pdu.Value); ok {
			if i, ok := ifaces[idx]; ok {
				i.Status = int(n)
			}
		}
	}); err != nil {
		return ScanResult{}, fmt.Errorf("ifOperStatus: %w", err)
	}
	if err := walk(oidIfSpeed, func(idx int, pdu gosnmp.SnmpPDU) {
		if n, ok := toUint64(pdu.Value); ok {
			if i, ok := ifaces[idx]; ok {
				i.Speed = n
			}
		}
	}); err != nil {
		return ScanResult{}, fmt.Errorf("ifSpeed: %w", err)
	}
	// ifSpeed is capped by its 32-bit gauge. ifHighSpeed reports Mbps and is
	// authoritative for modern links above 4.29 Gbps. It is optional on older
	// agents, so a failed walk leaves the ifSpeed value in place.
	_ = walk(oidIfHighSpeed, func(idx int, pdu gosnmp.SnmpPDU) {
		if n, ok := toUint64(pdu.Value); ok && n > 0 {
			if i, ok := ifaces[idx]; ok {
				i.Speed = n * 1_000_000
			}
		}
	})

	// Detect 64-bit counter support per interface; mixed-capability agents are
	// common enough that a device-wide assumption can break individual ports.
	hcIndexes := make(map[int]bool)
	_ = walk(oidIfHCInOctets, func(idx int, _ gosnmp.SnmpPDU) { hcIndexes[idx] = true })

	list := make([]InterfaceInfo, 0, len(ifaces))
	for _, i := range ifaces {
		i.HasHC = hcIndexes[i.Index]
		list = append(list, *i)
	}
	hcSupported := len(hcIndexes) > 0

	device := Device{
		ID:         id,
		IP:         ip,
		Community:  community,
		Version:    version,
		Port:       port,
		UseHC:      hcSupported,
		Interfaces: list,
	}

	return ScanResult{Device: device, Interfaces: list}, nil
}
