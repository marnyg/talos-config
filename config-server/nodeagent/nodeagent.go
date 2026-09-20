// Package nodeagent is the contract between the hub and the node agent
// (the Talos system extension, talos-config-359.8.3): the config
// document the hub injects into a served machine config, and the
// boot-enrollment wire (ADR-0015) the agent redeems it on.
//
// The hub renders (Patch); the agent parses (Load) and redeems (Enroll*).
// Nothing here is the agent's runtime — that is cmd/nodeagent.
package nodeagent

import (
	"fmt"
	"os"

	"github.com/marnyg/talos-config/protocol/cert"
	"gopkg.in/yaml.v3"
)

// Service is the extension service name (talos/extensions/p0agent/
// rootfs/usr/local/etc/containers/p0agent.yaml `name:`). Talos binds an
// ExtensionServiceConfig document to a service by this name.
const Service = "p0agent"

// ConfigPath is where Talos mounts the document's file inside the
// service container; the agent reads it at start.
const ConfigPath = "/usr/local/etc/p0agent/agent.yaml"

// Config is the agent's file: where the hub is, and the one-shot token
// that buys the machine's member cert.
type Config struct {
	// Hub is the hub's HTTPS base URL: /.well-known/talos-hub/* and
	// EnrollPath. First fetch trusts web PKI (invariant 3's stated
	// exception, decision bjg); everything after is wallet-rooted.
	Hub string `yaml:"hub"`
	// Relay is the iroh home relay URL (the hub's own, ADR-0022).
	Relay string `yaml:"relay"`
	// Token is the ADR-0015 boot token. Redeemed once for a Kit; a
	// persisted Kit makes it inert. TTL boottoken.TTL from serve.
	Token string `yaml:"token"`
}

// Validate checks the fields the agent cannot run without.
func (c Config) Validate() error {
	switch {
	case c.Hub == "":
		return fmt.Errorf("nodeagent config: hub is empty")
	case c.Relay == "":
		return fmt.Errorf("nodeagent config: relay is empty")
	}
	return nil
}

// Load reads and validates the agent's config file.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("nodeagent config %s: %w", path, err)
	}
	return c, c.Validate()
}

// extSvcYAML is Talos's ExtensionServiceConfig document, the one
// document the hub adds to a served machine config.
type extSvcYAML struct {
	APIVersion  string           `yaml:"apiVersion"`
	Kind        string           `yaml:"kind"`
	Name        string           `yaml:"name"`
	ConfigFiles []configFileYAML `yaml:"configFiles"`
}

type configFileYAML struct {
	Content   string `yaml:"content"`
	MountPath string `yaml:"mountPath"`
}

// Patch renders the ExtensionServiceConfig document that carries c to
// the p0agent service — one document, appended to the served machine
// config after a `---` by machines.BuildConfig.
func Patch(c Config) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if c.Token == "" {
		return "", fmt.Errorf("nodeagent config: token is empty")
	}
	body, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	doc := extSvcYAML{
		APIVersion:  "v1alpha1",
		Kind:        "ExtensionServiceConfig",
		Name:        Service,
		ConfigFiles: []configFileYAML{{Content: string(body), MountPath: ConfigPath}},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// EnrollPath is the hub's boot-enrollment endpoint: POST EnrollRequest
// as JSON, receive the Kit (issuer.EncodeKit) or an error status —
// 401 for a token that does not verify, 409 for one this hub process
// already redeemed, 503 while the hub is sealed (retry).
const EnrollPath = "/mesh/enroll/node"

// EnrollRequest is what the agent posts: its self-minted NodeId and
// the token from its Config. The hub verifies the token (MAC-bound,
// TTL, single-use per process) and mints a member cert to Node with
// the MAC's git-declared name in group `machines`.
type EnrollRequest struct {
	Node  cert.ActorID `json:"node"`
	Token string       `json:"token"`
}
