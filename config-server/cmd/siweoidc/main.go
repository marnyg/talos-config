// Command siweoidc runs the SIWE→OIDC bridge (package siweoidc) as a
// standalone in-cluster service. Everything it knows arrives as flags
// from the k8s manifest — clients, admins, issuer, the gateway ids
// whose identity tokens it trusts — so the deployed configuration is
// exactly what git declares (invariant 2). It shares
// the hub's Go module for ethsig but deploys separately: SSO must stay
// up regardless of the hub's seal state, and a hub redeploy must not
// log the cluster out.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/config-server/siweoidc"
	"github.com/marnyg/talos-config/protocol/cert"
)

// stringsFlag collects a repeatable string flag.
type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error {
	if v == "" {
		return fmt.Errorf("empty value")
	}
	*s = append(*s, v)
	return nil
}

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

// adminsFlag collects repeated -admin 0xaddr=username:group[,group]
// declarations. Groups are explicit per wallet and drawn from the
// closed device-group vocabulary (admins|media): a typo would silently
// mint a group no relying party maps, so it fails here at pod start
// instead.
type adminsFlag map[string]siweoidc.Admin

func (a adminsFlag) String() string { return fmt.Sprintf("%v", map[string]siweoidc.Admin(a)) }

func (a adminsFlag) Set(v string) error {
	addr, rest, ok := strings.Cut(v, "=")
	name, groupList, ok2 := strings.Cut(rest, ":")
	if !ok || !ok2 || name == "" || groupList == "" {
		return fmt.Errorf("want 0xaddress=username:group[,group], got %q", v)
	}
	norm, err := ethsig.NormalizeAddress(addr)
	if err != nil {
		return err
	}
	groups := strings.Split(groupList, ",")
	for _, g := range groups {
		if !policy.DeviceGroup(g) {
			return fmt.Errorf("admin %s: group %q is not one of %s/%s", norm, g, policy.GroupAdmins, policy.GroupMedia)
		}
	}
	a[norm] = siweoidc.Admin{Username: name, Groups: groups}
	return nil
}

func main() {
	var (
		clients      clientsFlag
		admins       = adminsFlag{}
		gateways     stringsFlag
		tokenClients stringsFlag
		issuer       = flag.String("issuer", "", "externally visible base URL, no trailing slash (e.g. http://auth.cp1.mesh.internal)")
		listen       = flag.String("listen", ":8080", "listen address")
	)
	flag.Var(&clients, "client", "OIDC client as id=redirect_uri[,redirect_uri...] (repeatable)")
	flag.Var(&admins, "admin", "allowlisted wallet as 0xaddress=username:group[,group] (repeatable; groups admins|media)")
	flag.Var(&gateways, "gateway", "gateway member id (ed:<hex>) whose X-Mesh-Token to trust for /authz and device login (repeatable)")
	flag.Var(&tokenClients, "token-client", "client id that accepts device login by token on /authorize, no wallet page (repeatable)")
	flag.Parse()

	var gwIDs []cert.ActorID
	for _, g := range gateways {
		gwIDs = append(gwIDs, cert.ActorID(g))
	}
	for _, id := range tokenClients {
		i := slices.IndexFunc(clients, func(c siweoidc.Client) bool { return c.ID == id })
		if i < 0 {
			log.Fatalf("-token-client %q names no -client", id)
		}
		clients[i].Token = true
	}

	p, err := siweoidc.New(*issuer, clients, admins, gwIDs)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("siwe-oidc: issuer %s, %d client(s) (%d by token), %d admin wallet(s), %d gateway(s) pinned, listening on %s",
		p.Issuer(), len(clients), len(tokenClients), len(admins), len(gwIDs), *listen)
	log.Fatal(http.ListenAndServe(*listen, p.Handler()))
}
