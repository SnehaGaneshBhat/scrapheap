package main

import (
	"net"
	"testing"

	"github.com/scrap-heap/backend/internal/sched"
)

func TestAddressFiltering(t *testing.T) {
	inputs := []struct {
		address string
		want    bool
	}{
		{address: "192.168.1.24", want: true},
		{address: "10.0.0.7", want: true},
		{address: "127.0.0.1", want: false},
		{address: "169.254.12.34", want: false},
		{address: "::1", want: false},
		{address: "fe80::1", want: false},
	}
	for _, tt := range inputs {
		t.Run(tt.address, func(t *testing.T) {
			if got := isPrintableIPv4(net.ParseIP(tt.address)); got != tt.want {
				t.Fatalf("isPrintableIPv4(%s)=%t, want %t", tt.address, got, tt.want)
			}
		})
	}
}

func TestParseSchedulerMode(t *testing.T) {
	tests := []struct {
		value string
		mode  sched.Mode
	}{
		{value: "pull", mode: sched.PlainPull},
		{value: "plain", mode: sched.PlainPull},
		{value: "aware", mode: sched.CapabilityAware},
	}
	for _, tt := range tests {
		mode, err := parseSchedulerMode(tt.value)
		if err != nil || mode != tt.mode {
			t.Fatalf("parseSchedulerMode(%q)=(%v,%v), want %v", tt.value, mode, err, tt.mode)
		}
	}
}
