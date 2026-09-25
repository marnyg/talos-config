package irohtransport

import "github.com/marnyg/talos-config/iroh-go/iroh"

// SetLogLevel sets iroh's process-wide log level by name — trace,
// debug, info or warn — as the binaries read it from their log env var
// (P0_LOG, SAP_LOG). "" or an unknown name changes nothing and reports
// false: logging stays off, which is iroh's default.
func SetLogLevel(name string) bool {
	l, ok := map[string]iroh.LogLevel{
		"trace": iroh.LogLevelTrace,
		"debug": iroh.LogLevelDebug,
		"info":  iroh.LogLevelInfo,
		"warn":  iroh.LogLevelWarn,
	}[name]
	if ok {
		iroh.SetLogLevel(l)
	}
	return ok
}
