package mesh

// Mesh control-channel HTTP: the hub's overlay HTTP surface, served on
// the nebula netstack. "/" is a hello (liveness through a real
// handshake), /config serves hub-composed machine configs to admin
// devices only — the route `nix run .#apply` fetches from.
//
// The /config gate is two layers deep. The nebula firewall admits
// tcp/80 only from certs carrying the admins group (HubConfig) — a
// predicate the CA signed, which a peer that merely reaches us cannot
// spoof. The handler below is the second layer, and under ADR-0012 it
// is fully cert-derived: no declared device list, no allowlist of
// admin addresses. For the peer that sourced the request, verify the
// live hostmap entry's cert has group=admins AND the cert's address
// equals DeviceIP(master, cert.Name). That structurally excludes
// machines (which carry group=machines) and any peer whose address
// does not match its own derivation — the property ADR-0007 carried
// from wg0's cryptokey routing onto the mesh, restated without the
// declared list.

import (
	"fmt"
	"log"
	"net/http"
	"net/netip"

	"github.com/slackhq/nebula"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

// isAdminPeer reports whether the peer sourcing this request holds an
// admin cert whose address matches its derived overlay address. Called
// per-request so newly-enrolled devices are admitted without hub
// restart, and revoked/expired peers stop being admitted the moment
// nebula drops the tunnel.
func isAdminPeer(master []byte, subnet netip.Prefix, peers []nebula.ControlHostInfo, src netip.Addr) bool {
	for _, hi := range peers {
		var match bool
		for _, a := range hi.VpnAddrs {
			if a == src {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		var isAdmin bool
		for _, g := range hi.Cert.Groups() {
			if g == GroupAdmins {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			return false
		}
		want, err := nebderive.DeviceIP(master, hi.Cert.Name(), subnet)
		if err != nil {
			return false
		}
		return want == src
	}
	return false
}

// serveMeshHTTP starts the overlay HTTP listener. The netstack owns
// only the hub's overlay address, so the wildcard listen cannot expose
// the routes anywhere but on the mesh.
func (m *Manager) serveMeshHTTP(svc *nebstack.Service, master []byte) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "hello from the mesh: %s\n", svc.OverlayAddr())
	})
	if m.TunnelConfig != nil {
		mux.Handle("GET /config", requireAdminPeer(master, m.subnet, svc.Peers, m.TunnelConfig))
	}

	listener, err := svc.Listen("tcp", ":80")
	if err != nil {
		return fmt.Errorf("mesh http listener: %w", err)
	}
	go func() {
		if err := http.Serve(listener, mux); err != nil {
			log.Printf("mesh http server: %v", err)
		}
	}()
	log.Printf("mesh http: hello + /config on %s:80 (admins gated per-request by cert group + derived address)", svc.OverlayAddr())
	return nil
}

// requireAdminPeer gates an overlay route to peers whose live cert is
// group=admins and whose address matches DeviceIP(master, cert.Name).
// The peer supplier is a function (svc.Peers) so revocations take
// effect the moment nebula drops the tunnel.
func requireAdminPeer(master []byte, subnet netip.Prefix, peers func() []nebula.ControlHostInfo, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		src := ap.Addr().Unmap()
		if !isAdminPeer(master, subnet, peers(), src) {
			log.Printf("mesh %s %s: refused non-admin peer %s", r.Method, r.URL.Path, src)
			http.Error(w, "forbidden: admin devices only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
