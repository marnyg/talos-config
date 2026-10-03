package main

// Storage watch: the second thing the gitops poll reads over the
// kube-api facet is Longhorn's Volume list, rendered as the /status
// `storage` row.
//
// Why (talos-config-cnb5, 2026-10-01): the media library's three
// volumes faulted when w1 went off on 2026-09-21 and nothing said so
// for 12 days. Every pod that mounted them stayed Running on a hung
// NFS mount, ArgoCD read Healthy, and /status had no eye on the data
// plane at all. Longhorn knows: a Volume's status.robustness goes
// `faulted` (no usable replica) or `degraded` (fewer healthy replicas
// than asked) the moment it happens. This row makes that visible from
// the one place the owner already looks.
//
// Nothing here acts (same posture as gitops.go / ADR-0027). The poll,
// dial and client cert are gitops.go's; this file is only the
// Longhorn-shaped half: what we read and how the row reads.

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// longhornVolumesPath lists every Volume CR; Longhorn keeps them
	// all in its own namespace whatever namespace the PVC is in.
	longhornVolumesPath = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/volumes"
)

// longhornVolumeList is the slice of the Volume list we read.
type longhornVolumeList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			NumberOfReplicas int `json:"numberOfReplicas"`
		} `json:"spec"`
		Status struct {
			State      string `json:"state"`      // attached, detached, …
			Robustness string `json:"robustness"` // healthy, degraded, faulted, unknown
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"conditions"`
			KubernetesStatus struct {
				Namespace string `json:"namespace"`
				PVCName   string `json:"pvcName"`
			} `json:"kubernetesStatus"`
		} `json:"status"`
	} `json:"items"`
}

// storageSnapshot is what the last poll saw of Longhorn.
type storageSnapshot struct {
	Err         string   // last fetch failure, "" when the last poll read the list
	Total       int      // volumes
	Detached    int      // volumes with no workload (robustness unknown by design)
	Faulted     []string // robustness faulted: no usable replica, data unreachable
	Degraded    []string // robustness degraded: fewer healthy replicas than spec
	Unscheduled []string // condition Scheduled=False: a replica has nowhere to go
}

// volumeName is how the row names a volume: the PVC the owner knows
// (`media/tv`), falling back to the Longhorn name for an orphan.
func volumeName(ns, pvc, lh string) string {
	if pvc != "" {
		return ns + "/" + pvc
	}
	return lh
}

// summarize folds the list into the snapshot. A detached volume is
// `unknown`, which is not a fault: nothing is attached to tell.
func (l longhornVolumeList) summarize() storageSnapshot {
	var s storageSnapshot
	for _, v := range l.Items {
		s.Total++
		name := volumeName(v.Status.KubernetesStatus.Namespace, v.Status.KubernetesStatus.PVCName, v.Metadata.Name)
		switch v.Status.Robustness {
		case "faulted":
			s.Faulted = append(s.Faulted, name)
		case "degraded":
			s.Degraded = append(s.Degraded, name)
		}
		if v.Status.State == "detached" {
			s.Detached++
		}
		for _, c := range v.Status.Conditions {
			if c.Type == "Scheduled" && c.Status == "False" {
				s.Unscheduled = append(s.Unscheduled, name)
			}
		}
	}
	sort.Strings(s.Faulted)
	sort.Strings(s.Degraded)
	sort.Strings(s.Unscheduled)
	return s
}

// line renders the /status row and whether it warrants the warn class:
// a fetch error or any volume faulted, degraded or unschedulable.
// Robustness is not health: a `faulted` volume's pods stay Running.
func (s storageSnapshot) line() (string, bool) {
	if s.Err != "" && s.Total == 0 {
		return "error: " + s.Err, true
	}
	var parts []string
	warn := false
	if len(s.Faulted) > 0 {
		warn = true
		parts = append(parts, fmt.Sprintf("%d FAULTED (%s)", len(s.Faulted), strings.Join(s.Faulted, " ")))
	}
	if len(s.Degraded) > 0 {
		warn = true
		parts = append(parts, fmt.Sprintf("%d degraded (%s)", len(s.Degraded), strings.Join(s.Degraded, " ")))
	}
	if len(s.Unscheduled) > 0 {
		warn = true
		parts = append(parts, fmt.Sprintf("%d unschedulable (%s)", len(s.Unscheduled), strings.Join(s.Unscheduled, " ")))
	}
	head := fmt.Sprintf("%d volumes", s.Total)
	if !warn {
		head += " healthy"
	}
	if s.Detached > 0 {
		head += fmt.Sprintf(" (%d detached)", s.Detached)
	}
	parts = append([]string{head}, parts...)
	if s.Err != "" {
		warn = true
		parts = append(parts, "last poll failed: "+s.Err)
	}
	return strings.Join(parts, " — "), warn
}
