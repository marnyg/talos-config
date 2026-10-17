package main

import (
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

const (
	testHubID    = cert.ActorID("ed:" + "aa" + "00000000000000000000000000000000000000000000000000000000000000")
	testMemberID = cert.ActorID("ed:" + "bb" + "00000000000000000000000000000000000000000000000000000000000000")
	testStranger = cert.ActorID("ed:" + "cc" + "00000000000000000000000000000000000000000000000000000000000000")
)

// TestRelayAdmitColdWindow: the rule from the 2026-10-17 ruling (5gz).
// Open while any cold condition holds — <5 min since start, hub not
// serving, nobody witnessed — then gated on self + witnessed members.
func TestRelayAdmitColdWindow(t *testing.T) {
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		since    time.Duration
		serving  bool
		members  []cert.ActorID
		id       cert.ActorID
		wantOK   bool
		wantMode string
	}{
		{"just started, stranger", 0, true, []cert.ActorID{testMemberID}, testStranger, true, "open (cold"},
		{"4m59s, stranger", 5*time.Minute - time.Second, true, []cert.ActorID{testMemberID}, testStranger, true, "open (cold"},
		{"warm but sealed, stranger", 10 * time.Minute, false, []cert.ActorID{testMemberID}, testStranger, true, "open (cold"},
		{"warm, serving, nobody witnessed", 10 * time.Minute, true, nil, testStranger, true, "open (cold"},
		{"5m exactly, gated: member", 5 * time.Minute, true, []cert.ActorID{testMemberID}, testMemberID, true, "gated (1 members)"},
		{"gated: hub itself", time.Hour, true, []cert.ActorID{testMemberID}, testHubID, true, "gated (1 members)"},
		{"gated: stranger", time.Hour, true, []cert.ActorID{testMemberID}, testStranger, false, "gated (1 members)"},
		{"gated: empty id", time.Hour, true, []cert.ActorID{testMemberID}, "", false, "gated (1 members)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &relayAdmitter{
				self:    testHubID,
				members: func() []cert.ActorID { return tc.members },
				serving: func() bool { return tc.serving },
				started: start,
				now:     func() time.Time { return start.Add(tc.since) },
			}
			ok, mode := a.Admit(tc.id)
			if ok != tc.wantOK || !strings.HasPrefix(mode, tc.wantMode) {
				t.Fatalf("Admit(%s) = %v %q, want %v %q…", tc.id, ok, mode, tc.wantOK, tc.wantMode)
			}
		})
	}
}

// TestRelayHookFor: the hook points at the hub's own port on loopback,
// carries a fresh 32-byte token per process, and its Admit reads the
// live hub: a sealed hub past the start window is still cold (open).
func TestRelayHookFor(t *testing.T) {
	hub := testHubManager(t, nil)
	h1, err := relayHookFor(hub, 8080, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := relayHookFor(hub, 8080, time.Now())
	if h1.URL != "http://127.0.0.1:8080/relay/access" || len(h1.Token) != 64 || h1.Token == h2.Token || !h1.enabled() {
		t.Fatalf("hook: %+v", h1)
	}
	if ok, mode := h1.Admit(testStranger); !ok || !strings.HasPrefix(mode, "open (cold") {
		t.Fatalf("sealed hub gated a stranger: %v %q", ok, mode)
	}
	for _, partial := range []relayHook{{}, {URL: h1.URL}, {URL: h1.URL, Token: h1.Token}, {Token: h1.Token, Admit: h1.Admit}} {
		if partial.enabled() {
			t.Fatalf("partial hook enabled: %+v", partial)
		}
	}
}
