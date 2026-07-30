package mesh

// Mesh control-channel HTTP: the hub's overlay HTTP surface, served on
// the nebula netstack. "/" is a hello (liveness through a real
// handshake), /config serves hub-composed machine configs to admin
// devices only — the route `nix run .#apply` fetches from.
//
// The auth carries ADR-0003's property onto the mesh, with one layer
// added. Nebula's firewall admits tcp/80 only from certs carrying the
// admins group (HubConfig) — a predicate the CA signed, which a
// peer that merely reaches us cannot spoof. The source-IP gate below is
// the second layer: a cert's overlay address is bound at mint time to a
// wallet-derived identity, so "request from a derived admin address"
// still maps to exactly one wallet-authorized device — ADR-0007, which
// carries ADR-0003's property from wg0's cryptokey routing onto the
// mesh's CA-signed certs.

import (
	"fmt"
	"log"
	"net/http"
	"net/netip"

	"github.com/marnyg/talos-config/config-server/nebderive"
	"github.com/marnyg/talos-config/config-server/nebstack"
)

// adminMeshIPs derives the overlay addresses of the admins-group
// devices — the set the mesh /config gate admits. Media devices are
// left out on purpose: the firewall already drops them at tcp/80, and
// this gate must agree with it, never widen it. Machines are absent
// for the same reason as on wg0 — served configs carry other machines'
// secrets, and a machine must never read a config it didn't earn a
// device-flow token for.
func (m *Manager) adminMeshIPs(master []byte) (map[netip.Addr]bool, error) {
	ips := map[netip.Addr]bool{}
	for _, d := range m.devices {
		if d.Group != GroupAdmins {
			continue
		}
		ip, err := nebderive.DeviceIP(master, d.Name, m.subnet)
		if err != nil {
			return nil, fmt.Errorf("deriving mesh address for admin device %q: %w", d.Name, err)
		}
		ips[ip] = true
	}
	return ips, nil
}

// serveMeshHTTP starts the overlay HTTP listener. The netstack owns
// only the hub's overlay address, so the wildcard listen cannot expose
// the routes anywhere but on the mesh.
func (m *Manager) serveMeshHTTP(svc *nebstack.Service, master []byte) error {
	admins, err := m.adminMeshIPs(master)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "hello from the mesh: %s\n", svc.OverlayAddr())
	})
	if m.TunnelConfig != nil {
		mux.Handle("GET /config", requireAdminPeer(admins, m.TunnelConfig))
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
	log.Printf("mesh http: hello + /config on %s:80, %d admin address(es)", svc.OverlayAddr(), len(admins))
	return nil
}

// requireAdminPeer gates an overlay route to admin device source
// addresses. Machines are mesh members too, and served configs carry
// other machines' secrets — a machine must never read a config it
// didn't earn a device-flow token for.
func requireAdminPeer(admins map[netip.Addr]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || !admins[ap.Addr().Unmap()] {
			log.Printf("mesh %s %s: refused non-admin peer %s", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, "forbidden: admin devices only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
