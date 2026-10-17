//go:build !iroh

// Without the iroh build tag there is no transport to bind; the real
// main is main.go. Kept so `go build ./...` / `go vet` on an untagged
// tree has a package here rather than "build constraints exclude all
// Go files" (same as cmd/nodeagent), and so backoff.go's untagged test
// runs.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "gateway: built without -tags iroh; nothing to run")
	os.Exit(1)
}
