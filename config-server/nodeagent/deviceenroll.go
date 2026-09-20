package nodeagent

// Headless device-flow enrollment (talos-config-4ps; first consumer the
// gateway pod, 359.9.3): a member with no wallet and no browser of its
// own starts a flow at the hub, shows the approve URL wherever it can
// (its log), and polls until the Owner has signed on /status. The
// wallet's signature covers the NodeId this member minted (ADR-0012 v2
// message), so what comes back is this member's Kit and nobody else's.
//
// Dual-plane baggage: the hub's device flow still mints a nebula config
// too, so the flow needs an X25519 pubkey. A throwaway is minted per
// attempt and its config discarded — the identity plane is the only
// plane a headless member joins. Phase 4 deletes the parameter.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/marnyg/talos-config/config-server/devkey"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Device-flow paths on the hub (nebenroll.go, oauth.go).
const (
	DeviceEnrollPath = "/mesh/enroll/device"
	DeviceTokenPath  = "/token"
	DeviceConfigPath = "/mesh/enroll/config"
	deviceGrantType  = "urn:ietf:params:oauth:grant-type:device_code"
)

// ErrEnrollDenied: the Owner denied the flow. Terminal for this
// process: retrying would nag.
var ErrEnrollDenied = errors.New("nodeagent: device enrollment denied")

// ErrEnrollExpired: the flow expired unsigned (nobody was at /status).
// A fresh flow, later, is the answer.
var ErrEnrollExpired = errors.New("nodeagent: device enrollment expired unsigned")

// DeviceFlow is what the hub answers when a flow starts: what to show
// the Owner, and how to poll.
type DeviceFlow struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	ApproveURL string `json:"verification_uri_complete"`
	// QRPNG is ApproveURL as a QR code (PNG, base64): what a screen
	// with no keyboard shows (the TV app). Empty for a log-only member.
	QRPNG     string `json:"qr_png_base64"`
	ExpiresIn int    `json:"expires_in"`
	Interval  int    `json:"interval"`
}

// EnrollDevice runs one device flow to completion: start, show, poll,
// redeem, verify. name and group are proposals — the approver picks
// the final values on /status. show is called once with the flow the
// Owner must act on. c nil ⇒ hubClient (30 s timeout, h2 liveness
// pings — the poll runs on a phone that may change networks). Returns ErrHubSealed (the hub answered 503:
// retry later), ErrEnrollDenied, ErrEnrollExpired, or the transport's
// error.
func EnrollDevice(ctx context.Context, c *http.Client, base string, node cert.ActorID, name, group string, show func(DeviceFlow)) (issuer.Kit, error) {
	if c == nil {
		c = hubClient()
	}
	flow, err := startDeviceFlow(ctx, c, base, node, name, group)
	if err != nil {
		return issuer.Kit{}, err
	}
	if show != nil {
		show(flow)
	}
	token, err := pollDeviceFlow(ctx, c, base, flow)
	if err != nil {
		return issuer.Kit{}, err
	}
	return redeemDeviceFlow(ctx, c, base, node, token)
}

func startDeviceFlow(ctx context.Context, c *http.Client, base string, node cert.ActorID, name, group string) (DeviceFlow, error) {
	_, xpub, err := devkey.Generate()
	if err != nil {
		return DeviceFlow{}, err
	}
	form := url.Values{
		"pubkey":         {hex.EncodeToString(xpub[:])},
		"node":           {string(node)},
		"proposed_name":  {name},
		"proposed_group": {group},
	}
	raw, status, err := postForm(ctx, c, base+DeviceEnrollPath, form)
	if err != nil {
		return DeviceFlow{}, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusServiceUnavailable:
		return DeviceFlow{}, fmt.Errorf("%w: %s", ErrHubSealed, strings.TrimSpace(string(raw)))
	default:
		return DeviceFlow{}, fmt.Errorf("nodeagent: device enroll: %d %s", status, strings.TrimSpace(string(raw)))
	}
	var flow DeviceFlow
	if err := json.Unmarshal(raw, &flow); err != nil || flow.DeviceCode == "" || flow.UserCode == "" {
		return DeviceFlow{}, fmt.Errorf("nodeagent: device enroll: malformed answer: %s", strings.TrimSpace(string(raw)))
	}
	if flow.Interval <= 0 {
		flow.Interval = 5
	}
	return flow, nil
}

// pollDeviceFlow polls /token at the hub's interval until a token, a
// terminal error, or ctx ends. slow_down widens the interval.
func pollDeviceFlow(ctx context.Context, c *http.Client, base string, flow DeviceFlow) (string, error) {
	interval := time.Duration(flow.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(flow.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		if flow.ExpiresIn > 0 && time.Now().After(deadline) {
			return "", fmt.Errorf("%w: user code %s", ErrEnrollExpired, flow.UserCode)
		}
		raw, status, err := postForm(ctx, c, base+DeviceTokenPath, url.Values{
			"grant_type":  {deviceGrantType},
			"device_code": {flow.DeviceCode},
		})
		if err != nil {
			return "", err
		}
		var body struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return "", fmt.Errorf("nodeagent: token: %d %s", status, strings.TrimSpace(string(raw)))
		}
		switch {
		case status == http.StatusOK && body.AccessToken != "":
			return body.AccessToken, nil
		case body.Error == "authorization_pending":
		case body.Error == "slow_down":
			interval += 5 * time.Second
		case body.Error == "access_denied":
			return "", fmt.Errorf("%w: user code %s", ErrEnrollDenied, flow.UserCode)
		case body.Error == "expired_token", body.Error == "invalid_grant":
			return "", fmt.Errorf("%w: user code %s (%s)", ErrEnrollExpired, flow.UserCode, body.Error)
		default:
			return "", fmt.Errorf("nodeagent: token: %d %s", status, strings.TrimSpace(string(raw)))
		}
	}
}

// redeemDeviceFlow fetches the approved {config, kit} and keeps the Kit.
func redeemDeviceFlow(ctx context.Context, c *http.Client, base string, node cert.ActorID, token string) (issuer.Kit, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+DeviceConfigPath, nil)
	if err != nil {
		return issuer.Kit{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.Do(req)
	if err != nil {
		return issuer.Kit{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return issuer.Kit{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return issuer.Kit{}, fmt.Errorf("nodeagent: redeem: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Kit json.RawMessage `json:"kit"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Kit) == 0 {
		return issuer.Kit{}, errors.New("nodeagent: redeem: the hub did not answer with {config, kit} (identity plane not served?)")
	}
	kit, err := issuer.DecodeKit(env.Kit)
	if err != nil {
		return issuer.Kit{}, fmt.Errorf("nodeagent: redeem: kit: %w", err)
	}
	if err := CheckKit(kit, node); err != nil {
		return issuer.Kit{}, err
	}
	return kit, nil
}

func postForm(ctx context.Context, c *http.Client, u string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return raw, resp.StatusCode, err
}
