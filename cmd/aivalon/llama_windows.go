//go:build windows

package main

import (
	"os"
	"syscall"
)

// UNVERIFIED / UNTESTED ON REAL WINDOWS — a human must test this on Windows
// before relying on it.
//
// CREATE_NEW_PROCESS_GROUP keeps Ctrl+C in AIvalon's console from hitting
// llama-server directly, but Windows has no "kill the group" signal:
// Process.Kill only terminates llama-server itself, NOT any child processes
// it spawned (router mode's per-model instances), which can be orphaned.
// A real fix needs `taskkill /T /F /PID <pid>` or a job object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE — neither is implemented here.
func sidecarSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// No graceful signal on Windows; both steps are a hard kill of the leader only.
func terminateGroup(p *os.Process) { p.Kill() }
func killGroup(p *os.Process)      { p.Kill() }
