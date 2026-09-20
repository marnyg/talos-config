package nodeagent

import (
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// hubClient is the default client for hub HTTPS: 30 s per request over
// hubTransport. One constructor so enrollment and the beat loop cannot
// drift apart on the liveness settings.
func hubClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: hubTransport()}
}

// hubTransport is the default transport for hub HTTPS: the stock one
// plus HTTP/2 liveness pings. Without ReadIdleTimeout a pooled h2
// connection whose underlay vanished without a RST (a phone leaving
// Wi-Fi, 2026-09-20) is reused for every request and each one runs
// the client's full 30 s timeout; with it the dead connection is
// noticed within ~2× the interval and the next request dials fresh.
func hubTransport() http.RoundTripper {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if h2, err := http2.ConfigureTransports(tr); err == nil {
		h2.ReadIdleTimeout = 15 * time.Second
		h2.PingTimeout = 10 * time.Second
	}
	return tr
}
