//go:build iroh

// Package mobile is the gomobile bind surface for the Android TV/phone
// app (android/): a member of the identity plane on a VpnService
// (Mesh v3 P2.4, talos-config-359.9.4). It is the third presentation
// of the one runtime — the node agent under Kind node, irohup on a
// utun — with the same state layout (nodeagent.State under the app's
// files dir: key, kit.json, bundle.json, hub.json, mark) and the same
// fiction (meshtun over fakeip: 198.18/15, split DNS at 198.18.0.2).
//
// Enrollment is the headless device flow (nodeagent.EnrollDevice): the
// app mints its NodeId here (never derived, never in transit,
// ADR-0012), shows the QR + user code the hub answers with, and the
// Owner signs on /status from the phone with the wallet. What comes
// back is this member's Kit.
//
// API shape: gomobile binds only a narrow type set (no maps, no slices
// of structs), so anything structured crosses the boundary as a JSON
// string. Kotlin renders; Go owns all crypto, protocol and network.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/marnyg/talos-config/config-server/fakeip"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/protocol/cert"
)

// The VpnService.Builder's inputs (fakeip's plan): address TunIP/32,
// route FakeRange/FakePrefixLen, DNS server ResolverIP. Only the fake
// range enters the tunnel, so the phone's other traffic — iroh's own
// UDP, the DNS forwarder — never loops back into the tun.
const (
	TunIP         = fakeip.TunIP
	ResolverIP    = fakeip.ResolverIP
	FakeRange     = "198.18.0.0"
	FakePrefixLen = 15
	// DefaultMTU: comfortably inside every underlay (the tun carries
	// only IP-to-be-streamed, so the number is about gvisor's buffers,
	// not path MTU).
	DefaultMTU = 1280
	// DefaultHub is the hub's HTTPS base; the relay is the same host
	// (ADR-0022).
	DefaultHub = "https://marnyg-talos-config.fly.dev"
	// Zone is the presentation zone without its trailing dot.
	Zone = "mesh.internal"
)

// NodeID returns this device's NodeId (ed:<hex>), minting the key under
// stateDir if there is none yet. Rekey is the only thing that changes
// it.
func NodeID(stateDir string) (string, error) {
	priv, _, err := nodeagent.State{Dir: stateDir}.Key()
	if err != nil {
		return "", err
	}
	return string(cert.NewEdSigner(priv).ActorID()), nil
}

// Enrolled reports whether stateDir holds a Kit issued to its key.
func Enrolled(stateDir string) bool {
	st := nodeagent.State{Dir: stateDir}
	priv, minted, err := st.Key()
	if err != nil || minted {
		return false
	}
	kit, ok, err := st.Kit()
	if err != nil || !ok {
		return false
	}
	return nodeagent.CheckKit(kit, cert.NewEdSigner(priv).ActorID()) == nil
}

// MemberJSON is the Kit's summary for the connected screen:
// {"node","name","groups":[…],"exp":<unix>,"issuer"}. Error when not
// enrolled.
func MemberJSON(stateDir string) (string, error) {
	st := nodeagent.State{Dir: stateDir}
	kit, ok, err := st.Kit()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("not enrolled")
	}
	return marshal(map[string]any{
		"node":   kit.Member.Aud,
		"name":   kit.Member.Cav.Name,
		"groups": kit.Member.Cav.Groups,
		"exp":    kit.Member.Exp,
		"issuer": kit.Member.Iss,
	})
}

// Reenroll discards the Kit and the caches but keeps the key: the next
// Enroll re-signs the same NodeId (a renewal the hub could not do, or a
// name/group change).
func Reenroll(stateDir string) error {
	for _, f := range []string{nodeagent.KitFile, nodeagent.BundleFile, nodeagent.HubFile} {
		if err := os.Remove(filepath.Join(stateDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Rekey discards everything, the key included: a brand-new NodeId.
func Rekey(stateDir string) error {
	if err := Reenroll(stateDir); err != nil {
		return err
	}
	for _, f := range []string{nodeagent.KeyFile, nodeagent.MarkFile} {
		if err := os.Remove(filepath.Join(stateDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// EnrollListener is what the enroll screen renders: Kotlin implements
// it. Show is called once, when the hub has answered with the flow the
// Owner must act on. qrPNGBase64 encodes approveURL.
type EnrollListener interface {
	Show(userCode, approveURL, qrPNGBase64 string)
}

var (
	enrollMu     sync.Mutex
	enrollCancel context.CancelFunc
)

// Enroll runs one device flow to completion and persists the Kit: it
// blocks (call it off the main thread) until the Owner has signed, the
// flow was denied or expired, or CancelEnroll. name and group are
// proposals; the approver picks the final values on /status. The hub
// answers 503 while sealed (every deploy, until the wallet unseals it):
// that is an error here, the app retries when the user asks.
func Enroll(stateDir, hub, name, group string, show EnrollListener) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return errors.New("name must not be empty")
	}
	if group == "" {
		group = "media"
	}
	hub = strings.TrimRight(hub, "/")
	st := nodeagent.State{Dir: stateDir}
	priv, _, err := st.Key()
	if err != nil {
		return err
	}
	node := cert.NewEdSigner(priv).ActorID()

	ctx, cancel := context.WithCancel(context.Background())
	enrollMu.Lock()
	if enrollCancel != nil {
		enrollCancel()
	}
	enrollCancel = cancel
	enrollMu.Unlock()
	defer func() {
		enrollMu.Lock()
		if enrollCancel != nil {
			enrollCancel()
			enrollCancel = nil
		}
		enrollMu.Unlock()
	}()

	kit, err := nodeagent.EnrollDevice(ctx, nil, hub, node, name, group, func(f nodeagent.DeviceFlow) {
		if show != nil {
			show.Show(f.UserCode, f.ApproveURL, f.QRPNG)
		}
	})
	if err != nil {
		return err
	}
	if err := st.SaveKit(kit); err != nil {
		return err
	}
	// A previous membership's caches would name a hub this Kit may not
	// resolve; the first beat refetches them.
	for _, f := range []string{nodeagent.BundleFile, nodeagent.HubFile} {
		_ = os.Remove(filepath.Join(stateDir, f))
	}
	return nil
}

// CancelEnroll aborts a running Enroll (the user backed out); Enroll
// then returns context.Canceled.
func CancelEnroll() {
	enrollMu.Lock()
	defer enrollMu.Unlock()
	if enrollCancel != nil {
		enrollCancel()
		enrollCancel = nil
	}
}

// IsCancelled reports whether err (as Enroll returned it, stringified
// by gomobile) was CancelEnroll's doing rather than the hub's.
func IsCancelled(msg string) bool { return strings.Contains(msg, context.Canceled.Error()) }

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
