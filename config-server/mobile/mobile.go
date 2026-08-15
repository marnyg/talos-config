// Package mobile is the gomobile bind surface for the Android TV/phone
// app (task 2e1bef85): device-side RFC 8628 mesh enrollment plus the
// nebula tunnel runner. The app is the third enrollment client after
// nebup and Mobile Nebula, and it holds the same property (ADR-0012):
// the keypair is device-born, only the pubkey travels, and the config
// the hub returns is completed locally by splicing the key in.
//
// API shape: gomobile binds only a narrow type set (no maps, no slices
// of structs), so anything structured crosses the boundary as a JSON
// string — the mobile_nebula precedent. Kotlin parses; Go owns all
// crypto and protocol logic.
package mobile

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/slackhq/nebula/cert"
	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/config-server/devkey"
)

// httpClient is package-level so tests can shorten timeouts; the app
// talks to one hub over links as slow as hotel wifi.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Keypair is a device identity as it crosses into Kotlin. PrivHex is
// what the app persists (app-private storage); it never travels.
type keypairJSON struct {
	PrivHex string `json:"privHex"`
	PubHex  string `json:"pubHex"`
}

// GenerateKeypair returns a fresh X25519 device identity as JSON
// {"privHex","pubHex"}. Generate once, persist privHex; renewals
// re-enroll the same key so the device's derived address never moves.
func GenerateKeypair() (string, error) {
	priv, pub, err := devkey.Generate()
	if err != nil {
		return "", err
	}
	return marshal(keypairJSON{
		PrivHex: hex.EncodeToString(priv[:]),
		PubHex:  hex.EncodeToString(pub[:]),
	})
}

// PubkeyFromPriv rederives the pubkey hex from a persisted private
// key, so the app stores exactly one secret.
func PubkeyFromPriv(privHex string) (string, error) {
	_, pub, err := devkey.ParsePrivHex(privHex)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(pub[:]), nil
}

// StartEnroll begins the device-flow enrollment: POST
// /mesh/enroll/device with this device's pubkey and proposals. Returns
// the hub's JSON verbatim — device_code, user_code, verification_uri,
// verification_uri_complete, qr_png_base64, expires_in, interval,
// fingerprint — which is everything the enroll screen renders. The
// operator scans the QR, checks the fingerprint, and signs at /status.
func StartEnroll(hubURL, privHex, proposedName, proposedGroup string) (string, error) {
	pubHex, err := PubkeyFromPriv(privHex)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.PostForm(strings.TrimRight(hubURL, "/")+"/mesh/enroll/device", url.Values{
		"pubkey":         {pubHex},
		"proposed_name":  {proposedName},
		"proposed_group": {proposedGroup},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hub: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

// Poll statuses as the Kotlin side sees them. "pending" and
// "slow_down" both mean keep polling (slow_down: back off); "ok"
// carries the access token for FetchConfig; "denied" and "expired"
// are terminal — restart the flow.
type pollJSON struct {
	Status      string `json:"status"`
	AccessToken string `json:"accessToken,omitempty"`
}

// PollEnroll polls the hub's RFC 8628 token endpoint once with the
// device_code from StartEnroll. The app calls this on the interval the
// hub stated. Returns JSON {"status":..., "accessToken":...}.
func PollEnroll(hubURL, deviceCode string) (string, error) {
	resp, err := httpClient.PostForm(strings.TrimRight(hubURL, "/")+"/token", url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var got struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		return "", fmt.Errorf("hub token response is not JSON: %s", strings.TrimSpace(string(body)))
	}
	switch {
	case got.AccessToken != "":
		return marshal(pollJSON{Status: "ok", AccessToken: got.AccessToken})
	case got.Error == "authorization_pending":
		return marshal(pollJSON{Status: "pending"})
	case got.Error == "slow_down":
		return marshal(pollJSON{Status: "slow_down"})
	case got.Error == "access_denied":
		return marshal(pollJSON{Status: "denied"})
	case got.Error == "expired_token":
		return marshal(pollJSON{Status: "expired"})
	default:
		return "", fmt.Errorf("hub token endpoint: %s (%s)", got.Error, resp.Status)
	}
}

// FetchConfig redeems the access token at /mesh/enroll/config
// (single-use: the hub burns the token on delivery) and splices the
// device's private key into the returned config. The result is the
// complete nebula config the app persists and runs — the only copy
// anywhere that contains the key.
func FetchConfig(hubURL, accessToken, privHex string) (string, error) {
	priv, _, err := devkey.ParsePrivHex(privHex)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(hubURL, "/")+"/mesh/enroll/config", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hub: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	spliced, err := devkey.SpliceKeyInline(body, priv)
	if err != nil {
		return "", err
	}
	return string(spliced), nil
}

// configInfoJSON is what the VpnService builder needs from a completed
// config: the device's own address (from its cert — the yaml never
// states it), the route it should claim, the hub's overlay address
// (/hosts endpoint), the magic split-DNS resolver to advertise via
// addDnsServer (see dnsshim.go), the zone it serves, and the MTU.
type configInfoJSON struct {
	Name      string `json:"name"`
	OwnIP     string `json:"ownIP"`
	PrefixLen int    `json:"prefixLen"`
	HubIP     string `json:"hubIP"`
	DNSIP     string `json:"dnsIP"`
	DNSZone   string `json:"dnsZone"`
	MTU       int    `json:"mtu"`
}

// ConfigInfo parses a completed config for the fields Kotlin needs to
// build the VpnService (addAddress, addRoute, addDnsServer, setMtu).
func ConfigInfo(cfgYAML string) (string, error) {
	var doc struct {
		PKI struct {
			Cert string `yaml:"cert"`
		} `yaml:"pki"`
		Lighthouse struct {
			Hosts []string `yaml:"hosts"`
		} `yaml:"lighthouse"`
		Tun struct {
			MTU int `yaml:"mtu"`
		} `yaml:"tun"`
	}
	if err := yaml.Unmarshal([]byte(cfgYAML), &doc); err != nil {
		return "", fmt.Errorf("config is not valid YAML: %w", err)
	}
	if doc.PKI.Cert == "" {
		return "", fmt.Errorf("config has no pki.cert")
	}
	c, _, err := cert.UnmarshalCertificateFromPEM([]byte(doc.PKI.Cert))
	if err != nil {
		return "", fmt.Errorf("parsing device cert: %w", err)
	}
	nets := c.Networks()
	if len(nets) == 0 {
		return "", fmt.Errorf("device cert carries no network")
	}
	magic, err := dnsMagicIP(nets[0])
	if err != nil {
		return "", fmt.Errorf("deriving DNS resolver address: %w", err)
	}
	info := configInfoJSON{
		Name:      c.Name(),
		OwnIP:     nets[0].Addr().String(),
		PrefixLen: nets[0].Bits(),
		DNSIP:     magic.String(),
		DNSZone:   strings.TrimSuffix(meshDNSZone, "."),
		MTU:       doc.Tun.MTU,
	}
	if len(doc.Lighthouse.Hosts) > 0 {
		if ip, err := netip.ParseAddr(doc.Lighthouse.Hosts[0]); err == nil {
			info.HubIP = ip.String()
		}
	}
	if info.HubIP == "" {
		return "", fmt.Errorf("config has no lighthouse host (need the hub's overlay address for DNS)")
	}
	return marshal(info)
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
