//go:build linux

package main

import (
	"os"
	"syscall"
)

// Setpgid: own process group, so -pid reaches llama-server AND anything it
// spawns (router mode forks per-model instances). Pdeathsig: the kernel
// SIGTERMs the leader if AIvalon dies without running stop() (SIGKILL,
// OOM, panic) — belt and braces for the no-orphan guarantee.
func sidecarSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

func terminateGroup(p *os.Process) { syscall.Kill(-p.Pid, syscall.SIGTERM) }
func killGroup(p *os.Process)      { syscall.Kill(-p.Pid, syscall.SIGKILL) }
