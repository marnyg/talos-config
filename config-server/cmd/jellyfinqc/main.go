// jellyfinqc is the Jellyfin front proxy that logs appliances in from
// their mesh identity (package jellyfinqc, talos-config-95la). It runs
// as a sidecar in the Jellyfin pod and is the backend of the
// jellyfin.gw Ingress.
//
//	jellyfinqc -gateway ed:<hex> [-gateway ...] [-group media]
//	           [-listen :8097] [-upstream http://127.0.0.1:8096]
//	           [-admin-user admin]
//
// JELLYFIN_ADMIN_PASSWORD (env) is the local admin's password, the
// same secret the configurator uses. At start it waits for Jellyfin,
// signs in and makes sure Quick Connect is enabled; the proxy serves
// from the first moment regardless, since forwarding needs no admin.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/marnyg/talos-config/config-server/jellyfinqc"
	"github.com/marnyg/talos-config/config-server/meshtoken"
	"github.com/marnyg/talos-config/protocol/cert"
)

type stringsFlag []string

func (s *stringsFlag) String() string     { return "" }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		gateways  stringsFlag
		group     = flag.String("group", "media", "member-cert group whose devices are logged in automatically")
		listen    = flag.String("listen", ":8097", "listen address")
		upstream  = flag.String("upstream", "http://127.0.0.1:8096", "Jellyfin listener")
		adminUser = flag.String("admin-user", "admin", "Jellyfin local administrator")
	)
	flag.Var(&gateways, "gateway", "gateway member id (ed:<hex>) whose X-Mesh-Token to trust (repeatable)")
	flag.Parse()

	password := os.Getenv("JELLYFIN_ADMIN_PASSWORD")
	if password == "" {
		log.Fatal("JELLYFIN_ADMIN_PASSWORD is not set")
	}
	u, err := url.Parse(*upstream)
	if err != nil || u.Host == "" {
		log.Fatalf("-upstream %q: not a URL", *upstream)
	}
	var ids []cert.ActorID
	for _, g := range gateways {
		ids = append(ids, cert.ActorID(g))
	}
	verifier, err := meshtoken.NewVerifier(ids...)
	if err != nil {
		log.Fatal(err)
	}
	jf := jellyfinqc.NewJellyfin(*upstream, *adminUser, password)

	go func() {
		ctx := context.Background()
		for !jf.Ready(ctx) {
			time.Sleep(5 * time.Second)
		}
		for {
			if err := jf.EnableQuickConnect(ctx); err == nil {
				break
			} else {
				log.Printf("jellyfin: not ready for admin calls yet: %v", err)
			}
			time.Sleep(10 * time.Second)
		}
		log.Printf("jellyfin: admin access ready, Quick Connect on")
	}()

	log.Printf("jellyfinqc: %d gateway(s) pinned, group %s, upstream %s, listening on %s", len(ids), *group, *upstream, *listen)
	log.Fatal(http.ListenAndServe(*listen, jellyfinqc.New(u, verifier, jf, *group)))
}
