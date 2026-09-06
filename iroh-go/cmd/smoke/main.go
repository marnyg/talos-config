package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	relay := flag.String("relay", "", "relay URL (e.g. http://127.0.0.1:3340); empty = RelayMode::Disabled, direct dial")
	timeout := flag.Duration("timeout", 30*time.Second, "per-phase timeout")
	flag.Parse()

	if err := Run(Options{RelayURL: *relay, Timeout: *timeout, Log: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, "smoke: FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("smoke: PASS")
}
