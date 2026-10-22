// guacjson is the Guacamole front proxy that logs a device in from its
// mesh identity (package guacjson, talos-config-5is2, ADR-0045). It
// runs as a sidecar in the Guacamole pod and is the backend of the
// guac.gw Ingress.
//
//	guacjson -gateway ed:<hex> [-gateway ...] [-group admins]
//	         [-listen :8081] [-upstream http://127.0.0.1:8080]
//	         [-tokens-path /guacamole/api/tokens]
//	         -connection win2k25 -protocol rdp
//	         -param hostname=… [-param k=v ...]
//
// GUACJSON_SECRET_KEY (env) is the 32-hex-char AES key the webapp
// holds as JSON_SECRET_KEY; GUACJSON_PASSWORD (env), if set, becomes
// the connection's `password` parameter — so the guest's password is
// never on the command line. Every other RDP parameter is a -param,
// visible in the manifest.
package main

import (
	"encoding/hex"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/guacjson"
	"github.com/marnyg/talos-config/config-server/meshtoken"
	"github.com/marnyg/talos-config/protocol/cert"
)

type stringsFlag []string

func (s *stringsFlag) String() string     { return "" }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		gateways, params stringsFlag
		group            = flag.String("group", "admins", "member-cert group whose devices are logged in")
		listen           = flag.String("listen", ":8081", "listen address")
		upstream         = flag.String("upstream", "http://127.0.0.1:8080", "Guacamole webapp listener")
		tokensPath       = flag.String("tokens-path", guacjson.DefaultTokensPath, "the webapp's token endpoint")
		connName         = flag.String("connection", "win2k25", "connection name shown in Guacamole")
		protocol         = flag.String("protocol", "rdp", "connection protocol")
	)
	flag.Var(&gateways, "gateway", "gateway member id (ed:<hex>) whose X-Mesh-Token to trust (repeatable)")
	flag.Var(&params, "param", "connection parameter k=v (repeatable)")
	flag.Parse()

	key, err := hex.DecodeString(strings.TrimSpace(os.Getenv("GUACJSON_SECRET_KEY")))
	if err != nil || len(key) != 16 {
		log.Fatal("GUACJSON_SECRET_KEY must be 32 hex characters (the webapp's JSON_SECRET_KEY)")
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

	conn := guacjson.Connection{Name: *connName, Protocol: *protocol, Parameters: map[string]string{}}
	for _, p := range params {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			log.Fatalf("-param %q: want k=v", p)
		}
		conn.Parameters[k] = v
	}
	if pw := os.Getenv("GUACJSON_PASSWORD"); pw != "" {
		conn.Parameters["password"] = pw
	}
	minter, err := guacjson.NewMinter(key, conn)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("guacjson: %d gateway(s) pinned, group %s, connection %s/%s, upstream %s, listening on %s",
		len(ids), *group, conn.Name, conn.Protocol, *upstream, *listen)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           guacjson.New(u, verifier, minter, *group, *tokensPath),
		ReadHeaderTimeout: 10 * time.Second,
		// No Write/IdleTimeout: the WebSocket tunnel is a long-lived
		// stream; the gateway's ConnMaxAge is its ceiling.
	}
	log.Fatal(srv.ListenAndServe())
}
