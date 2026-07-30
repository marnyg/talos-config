// Command siweoidc runs the SIWE→OIDC bridge (package siweoidc) as a
// standalone in-cluster service. Everything it knows arrives as flags
// from the k8s manifest — clients, admins, issuer — so the deployed
// configuration is exactly what git declares (invariant 2). It shares
// the hub's Go module for ethsig but deploys separately: SSO must stay
// up regardless of the hub's seal state, and a hub redeploy must not
// log the cluster out.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/siweoidc"
)

// clientsFlag collects repeated -client id=uri[,uri...] declarations.
type clientsFlag []siweoidc.Client

func (c *clientsFlag) String() string { return fmt.Sprintf("%v", []siweoidc.Client(*c)) }

func (c *clientsFlag) Set(v string) error {
	id, uris, ok := strings.Cut(v, "=")
	if !ok || id == "" || uris == "" {
		return fmt.Errorf("want id=redirect_uri[,redirect_uri...], got %q", v)
	}
	*c = append(*c, siweoidc.Client{ID: id, RedirectURIs: strings.Split(uris, ",")})
	return nil
}

// adminsFlag collects repeated -admin 0xaddr=username declarations.
type adminsFlag map[string]string

func (a adminsFlag) String() string { return fmt.Sprintf("%v", map[string]string(a)) }

func (a adminsFlag) Set(v string) error {
	addr, name, ok := strings.Cut(v, "=")
	if !ok || name == "" {
		return fmt.Errorf("want 0xaddress=username, got %q", v)
	}
	norm, err := ethsig.NormalizeAddress(addr)
	if err != nil {
		return err
	}
	a[norm] = name
	return nil
}

func main() {
	var (
		clients clientsFlag
		admins  = adminsFlag{}
		issuer  = flag.String("issuer", "", "externally visible base URL, no trailing slash (e.g. http://auth.cp1.mesh.internal)")
		listen  = flag.String("listen", ":8080", "listen address")
	)
	flag.Var(&clients, "client", "OIDC client as id=redirect_uri[,redirect_uri...] (repeatable)")
	flag.Var(&admins, "admin", "allowlisted wallet as 0xaddress=username (repeatable)")
	flag.Parse()

	p, err := siweoidc.New(*issuer, clients, admins)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("siwe-oidc: issuer %s, %d client(s), %d admin wallet(s), listening on %s",
		p.Issuer(), len(clients), len(admins), *listen)
	log.Fatal(http.ListenAndServe(*listen, p.Handler()))
}
