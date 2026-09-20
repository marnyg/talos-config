package mesh

// Mesh control-channel HTTP: the hub's overlay HTTP surface, served on
// the nebula netstack. What is left is "/", a hello — liveness through
// a real handshake, which is what the e2e tests and `nebup` probe.
//
// The routes this listener used to carry are all gone: /config left
// 2026-09-19 (359.8.2.4; admins fetch composed configs over the
// hub-http facet on the identity plane), and /hosts + /policy left
// 2026-09-20 (ri3b) once the Android/TV app — their last consumer —
// ran the v3 APK (359.9.4). Neither has an identity-plane successor:
// the name map rides the Issuer's beat reply (decision mdv), and
// policy compiles to grants (ADR-0017), which are pulled, not polled.
// Phase 4 (359.11.2) deletes this file with the rest of neb*.go.

import (
	"fmt"
	"log"
	"net/http"

	"github.com/marnyg/talos-config/config-server/nebstack"
)

// serveMeshHTTP starts the overlay HTTP listener. The netstack owns
// only the hub's overlay address, so the wildcard listen cannot expose
// the routes anywhere but on the mesh.
func (m *Manager) serveMeshHTTP(svc *nebstack.Service) error {
	mux := http.NewServeMux()
	// Exact root only: a "GET /" catch-all answered every unknown
	// path with 200, hiding a removed route behind a greeting.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "hello from the mesh: %s\n", svc.OverlayAddr())
	})

	listener, err := svc.Listen("tcp", ":80")
	if err != nil {
		return fmt.Errorf("mesh http listener: %w", err)
	}
	go func() {
		if err := http.Serve(listener, mux); err != nil {
			log.Printf("mesh http server: %v", err)
		}
	}()
	log.Printf("mesh http: hello on %s:80", svc.OverlayAddr())
	return nil
}
