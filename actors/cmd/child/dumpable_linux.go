//go:build linux

package main

import (
	"log/slog"
	"syscall"
)

// hideProc makes this process non-dumpable: /proc/self/{environ,mem,
// maps,fd,…} become root-only, so the agent pi runs (same uid, every
// capability dropped by the driver's pod spec) cannot read the forge
// token out of /proc/1/environ. os.Unsetenv alone does not help — the
// kernel's view of environ is the block exec placed, not Go's copy.
// The first real run did exactly this (62a0ec24, 2026-10-19). pi and
// git are exec'd children and get dumpable back; they never held the
// secret in their initial environment (git takes it as GIT_CONFIG_*
// for one short-lived call while pi is not running).
func hideProc() {
	const prSetDumpable = 4
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); e != 0 {
		slog.Warn("child: prctl(PR_SET_DUMPABLE, 0)", "err", e)
	}
}
