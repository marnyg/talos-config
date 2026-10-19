//go:build !linux

package main

// hideProc is Linux-only (see dumpable_linux.go); elsewhere /proc is
// not the exposure.
func hideProc() {}
