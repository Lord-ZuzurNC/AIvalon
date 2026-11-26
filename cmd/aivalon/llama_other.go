//go:build !linux && !windows

package main

import (
	"os"
	"syscall"
)

// Non-Linux Unix (macOS, BSD): same process-group approach as Linux, minus
// Pdeathsig (Linux-only). Compiles, but only Linux is actually tested.
func sidecarSysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func terminateGroup(p *os.Process) { syscall.Kill(-p.Pid, syscall.SIGTERM) }
func killGroup(p *os.Process)      { syscall.Kill(-p.Pid, syscall.SIGKILL) }
