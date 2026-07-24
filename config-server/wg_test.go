package main

import (
	"testing"
	"time"
)

// TestParsePeerStats feeds a realistic wireguard-go UAPI get=1
// transcript: device-level keys first, then two peer sections, one of
// which has never completed a handshake.
func TestParsePeerStats(t *testing.T) {
	raw := "private_key=a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0\n" +
		"listen_port=51820\n" +
		"public_key=b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1\n" +
		"endpoint=203.0.113.9:33333\n" +
		"last_handshake_time_sec=1750000000\n" +
		"last_handshake_time_nsec=123456789\n" +
		"rx_bytes=123456\n" +
		"tx_bytes=654321\n" +
		"allowed_ip=10.99.0.54/32\n" +
		"public_key=c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2\n" +
		"last_handshake_time_sec=0\n" +
		"last_handshake_time_nsec=0\n" +
		"rx_bytes=0\n" +
		"tx_bytes=148\n" +
		"allowed_ip=10.99.0.55/32\n"

	stats := parsePeerStats(raw)
	if len(stats) != 2 {
		t.Fatalf("got %d peers, want 2", len(stats))
	}

	live := stats["b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"]
	if live.endpoint != "203.0.113.9:33333" {
		t.Errorf("endpoint: got %q", live.endpoint)
	}
	if !live.lastHandshake.Equal(time.Unix(1750000000, 0)) {
		t.Errorf("handshake: got %v", live.lastHandshake)
	}
	if live.rxBytes != 123456 || live.txBytes != 654321 {
		t.Errorf("counters: got rx=%d tx=%d", live.rxBytes, live.txBytes)
	}

	silent := stats["c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2"]
	if !silent.lastHandshake.IsZero() {
		t.Errorf("never-handshaken peer must have zero time, got %v", silent.lastHandshake)
	}
	if silent.txBytes != 148 {
		t.Errorf("silent peer tx: got %d", silent.txBytes)
	}
}
