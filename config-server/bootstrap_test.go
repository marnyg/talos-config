package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
)

func svc(id, state string) *machineapi.ServiceInfo {
	return &machineapi.ServiceInfo{Id: id, State: state}
}

func TestObserveEtcd(t *testing.T) {
	cases := []struct {
		name     string
		services []*machineapi.ServiceInfo
		want     etcdObservation
	}{
		{"no services", nil, etcdAbsent},
		{"no etcd", []*machineapi.ServiceInfo{svc("apid", "Running")}, etcdAbsent},
		{"etcd preparing", []*machineapi.ServiceInfo{svc("etcd", "Preparing")}, etcdWaiting},
		{"etcd waiting", []*machineapi.ServiceInfo{svc("etcd", "Waiting")}, etcdWaiting},
		{"etcd running", []*machineapi.ServiceInfo{svc("apid", "Running"), svc("etcd", "Running")}, etcdRunning},
	}
	for _, tc := range cases {
		if got := observeEtcd(tc.services); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestBootStateHappyPath: unreachable → waiting×2 → bootstrap → running → done.
func TestBootStateHappyPath(t *testing.T) {
	var st bootState
	steps := []struct {
		obs  etcdObservation
		want bootAction
	}{
		{etcdUnreachable, actNone},
		{etcdAbsent, actNone},
		{etcdWaiting, actNone},      // first waiting: streak too short
		{etcdWaiting, actBootstrap}, // second consecutive: fire
	}
	for i, s := range steps {
		if got := st.next(s.obs); got != s.want {
			t.Fatalf("step %d (%s): got %v, want %v", i, s.obs, got, s.want)
		}
	}
	st.attempted = true // the loop sets this on a successful call
	if got := st.next(etcdWaiting); got != actNone {
		t.Fatalf("after successful attempt, waiting must not re-trigger, got %v", got)
	}
	if got := st.next(etcdRunning); got != actDone {
		t.Fatalf("running must complete, got %v", got)
	}
	if got := st.next(etcdWaiting); got != actNone {
		t.Fatal("done state must be terminal even if etcd flaps")
	}
}

// TestBootStateStreakReset: a blip between waiting observations resets
// the streak — bootstrap requires *consecutive* confirmation.
func TestBootStateStreakReset(t *testing.T) {
	var st bootState
	st.next(etcdWaiting)
	st.next(etcdUnreachable) // blip
	if got := st.next(etcdWaiting); got != actNone {
		t.Fatalf("streak should have reset, got %v", got)
	}
	if got := st.next(etcdWaiting); got != actBootstrap {
		t.Fatalf("two fresh consecutive waits should fire, got %v", got)
	}
}

// TestBootStateAlreadyBootstrapped: a cluster that is already running
// goes straight to done and never fires.
func TestBootStateAlreadyBootstrapped(t *testing.T) {
	var st bootState
	if got := st.next(etcdRunning); got != actDone {
		t.Fatalf("got %v, want actDone", got)
	}
	for _, obs := range []etcdObservation{etcdWaiting, etcdWaiting, etcdWaiting} {
		if got := st.next(obs); got != actNone {
			t.Fatalf("done is terminal, got %v", got)
		}
	}
}

// TestControlPlanesFilter: eligibility comes from the declared
// machine.type in the base config — not the filename; the single-CP
// guard depends on this.
func TestControlPlanesFilter(t *testing.T) {
	root := t.TempDir()
	write := func(name, typ string) {
		t.Helper()
		body := "version: v1alpha1\nmachine:\n  type: " + typ + "\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Filenames deliberately lie: the worker file is named
	// "controlplane.yaml" and vice versa.
	write("controlplane.yaml", "worker")
	write("worker.yaml", "controlplane")

	machines := map[string]machine{
		"aa:aa:aa:aa:aa:aa": {Config: "worker.yaml"},
		"bb:bb:bb:bb:bb:bb": {Config: "controlplane.yaml"},
		"cc:cc:cc:cc:cc:cc": {Config: "missing.yaml"}, // unreadable: skipped
	}
	cps := controlPlanes(root, machines)
	if len(cps) != 1 {
		t.Fatalf("got %d control planes, want 1", len(cps))
	}
	if _, ok := cps["aa:aa:aa:aa:aa:aa"]; !ok {
		t.Fatal("declared type must win over filename")
	}
}

// TestStepSealed: the loop must be inert while the server is sealed.
func TestStepSealed(t *testing.T) {
	wgm := newWGManager(51820, netip.MustParsePrefix("10.99.0.1/24"), "203.0.113.7:51820", "", "talos.wg", t.TempDir(), nil, nil)
	b := newBootstrapper(t.TempDir(), wgm)
	b.step(t.Context()) // must not panic or act
	if b.st.attempted || b.st.done {
		t.Fatal("sealed step must not change state")
	}
}
