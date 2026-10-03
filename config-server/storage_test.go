package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestStorageSummarize pins the cnb5 picture: three attached volumes
// with robustness faulted read as FAULTED by their PVC names, a
// detached volume's `unknown` is not a fault, and Scheduled=False
// surfaces even on a healthy volume.
func TestStorageSummarize(t *testing.T) {
	const body = `{"items":[
	 {"metadata":{"name":"pvc-1"},"spec":{"numberOfReplicas":1},"status":{"state":"attached","robustness":"faulted",
	   "kubernetesStatus":{"namespace":"media","pvcName":"tv"}}},
	 {"metadata":{"name":"pvc-2"},"spec":{"numberOfReplicas":1},"status":{"state":"attached","robustness":"faulted",
	   "kubernetesStatus":{"namespace":"media","pvcName":"movies"}}},
	 {"metadata":{"name":"pvc-3"},"spec":{"numberOfReplicas":2},"status":{"state":"attached","robustness":"degraded",
	   "kubernetesStatus":{"namespace":"gateway","pvcName":"gateway-state"}}},
	 {"metadata":{"name":"pvc-4"},"spec":{"numberOfReplicas":2},"status":{"state":"detached","robustness":"unknown",
	   "kubernetesStatus":{"namespace":"vms","pvcName":"win2k25-iso"}}},
	 {"metadata":{"name":"pvc-5"},"spec":{"numberOfReplicas":2},"status":{"state":"attached","robustness":"healthy",
	   "conditions":[{"type":"Scheduled","status":"False","message":"replica scheduling failed"}],
	   "kubernetesStatus":{"namespace":"sap","pvcName":"sap-provisioner-state"}}},
	 {"metadata":{"name":"pvc-orphan"},"spec":{"numberOfReplicas":2},"status":{"state":"attached","robustness":"healthy",
	   "kubernetesStatus":{}}}
	]}`
	var vols longhornVolumeList
	if err := json.Unmarshal([]byte(body), &vols); err != nil {
		t.Fatal(err)
	}
	s := vols.summarize()
	if s.Total != 6 || s.Detached != 1 {
		t.Fatalf("counts = %+v", s)
	}
	if strings.Join(s.Faulted, ",") != "media/movies,media/tv" || strings.Join(s.Degraded, ",") != "gateway/gateway-state" ||
		strings.Join(s.Unscheduled, ",") != "sap/sap-provisioner-state" {
		t.Fatalf("buckets = %+v", s)
	}
	line, warn := s.line()
	if !warn || !strings.Contains(line, "2 FAULTED (media/movies media/tv)") || !strings.Contains(line, "1 degraded") ||
		!strings.Contains(line, "1 unschedulable") || !strings.Contains(line, "(1 detached)") || strings.Contains(line, "healthy") {
		t.Fatalf("line = %q warn=%v", line, warn)
	}

	// All good: one calm line, no warn.
	calm := storageSnapshot{Total: 13, Detached: 1}
	if line, warn := calm.line(); warn || line != "13 volumes healthy (1 detached)" {
		t.Fatalf("calm = %q warn=%v", line, warn)
	}
	// A failed poll keeps the last counts and warns beside them; a
	// failure before any read is just the error.
	failed := calm
	failed.Err = "GET /apis/longhorn.io/...: 503 Service Unavailable"
	if line, warn := failed.line(); !warn || !strings.HasPrefix(line, "13 volumes") || !strings.Contains(line, "last poll failed") {
		t.Fatalf("failed = %q warn=%v", line, warn)
	}
	if line, warn := (storageSnapshot{Err: "dial: unreachable"}).line(); !warn || !strings.HasPrefix(line, "error:") {
		t.Fatalf("never read = %q warn=%v", line, warn)
	}
}

// TestStorageRidesGitopsClient: one poll, two GETs on the same
// authenticated client; a Longhorn failure leaves the gitops read
// intact and vice versa.
func TestStorageRidesGitopsClient(t *testing.T) {
	const app = `{"status":{"reconciledAt":"2026-09-29T19:10:00Z","sync":{"status":"Synced","revision":"f93ebd3bec02"},"health":{"status":"Healthy"}}}`
	const vols = `{"items":[{"metadata":{"name":"pvc-1"},"status":{"state":"attached","robustness":"faulted","kubernetesStatus":{"namespace":"media","pvcName":"tv"}}}]}`
	var paths []string
	g, m, facet := newGitopsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "hub" {
			http.Error(w, "no client cert", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case gitopsAppPath:
			_, _ = w.Write([]byte(app))
		case longhornVolumesPath:
			_, _ = w.Write([]byte(vols))
		default:
			http.NotFound(w, r)
		}
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	kc, err := g.kubeClient(m, facet)
	if err != nil {
		t.Fatal(err)
	}
	defer kc.CloseIdleConnections()
	a, err := g.fetchApp(ctx, kc)
	if err != nil {
		t.Fatal(err)
	}
	g.recordApp(a)
	v, err := g.fetchVolumes(ctx, kc)
	if err != nil {
		t.Fatal(err)
	}
	g.setStorage(v.summarize(), nil)
	if len(paths) != 2 || paths[0] != gitopsAppPath || paths[1] != longhornVolumesPath {
		t.Fatalf("paths = %v", paths)
	}
	at := time.Date(2026, 9, 29, 19, 12, 0, 0, time.UTC)
	if line, warn := g.status().line(at); warn || !strings.Contains(line, "Synced/Healthy") {
		t.Fatalf("gitops line = %q warn=%v", line, warn)
	}
	if line, warn := g.storageStatus().line(); !warn || !strings.Contains(line, "1 FAULTED (media/tv)") {
		t.Fatalf("storage line = %q warn=%v", line, warn)
	}
	// Longhorn gone (CRD 404) keeps the last storage read and flags it.
	g.setStorage(storageSnapshot{}, context.DeadlineExceeded)
	if line, warn := g.storageStatus().line(); !warn || !strings.Contains(line, "media/tv") || !strings.Contains(line, "last poll failed") {
		t.Fatalf("storage after failure = %q warn=%v", line, warn)
	}
}
