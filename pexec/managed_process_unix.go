//go:build linux || darwin

package pexec

import (
	"os"
	"syscall"
	"time"

	"github.com/pkg/errors"
)

func sigStr(sig syscall.Signal) string {
	//nolint:exhaustive
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return "<UNKNOWN>"
	}
}

var knownSignals = []syscall.Signal{
	syscall.SIGHUP,
	syscall.SIGINT,
	syscall.SIGQUIT,
	syscall.SIGABRT,
	syscall.SIGUSR1,
	syscall.SIGUSR2,
	syscall.SIGTERM,
}

func parseSignal(sigStr, name string) (syscall.Signal, error) {
	switch sigStr {
	case "":
		return 0, nil
	case "HUP", "SIGHUP", "hangup", "1":
		return syscall.SIGHUP, nil
	case "INT", "SIGINT", "interrupt", "2":
		return syscall.SIGINT, nil
	case "QUIT", "SIGQUIT", "quit", "3":
		return syscall.SIGQUIT, nil
	case "ABRT", "SIGABRT", "aborted", "abort", "6":
		return syscall.SIGABRT, nil
	case "KILL", "SIGKILL", "killed", "kill", "9":
		return syscall.SIGKILL, nil
	case "TERM", "SIGTERM", "terminated", "terminate", "15":
		return syscall.SIGTERM, nil
	default:
		return 0, errors.Errorf("unknown %q name", sigStr)
	}
}

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func (p *managedProcess) kill() (bool, error) {
	p.logger.Infof("stopping process %d with signal %s", p.cmd.Process.Pid, p.stopSig)
	// First let's try to directly signal the process.
	if err := p.cmd.Process.Signal(p.stopSig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return false, errors.Wrapf(err, "error signaling process %d with signal %s", p.cmd.Process.Pid, p.stopSig)
	}

	// In case the process didn't stop, or left behind any orphan children in its process group,
	// we now send a signal to everything in the process group after a brief wait.
	timer := time.NewTimer(p.stopWaitInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		p.logger.Infof("stopping entire process group %d with signal %s", p.cmd.Process.Pid, p.stopSig)
		if err := syscall.Kill(-p.cmd.Process.Pid, p.stopSig); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return false, errors.Wrapf(err, "error signaling process group %d with signal %s", p.cmd.Process.Pid, p.stopSig)
		}
	case <-p.managingCh:
		timer.Stop()
	}

	// Lastly, kill everything in the process group that remains after a longer wait
	var forceKilled bool
	timer2 := time.NewTimer(p.stopWaitInterval * 2)
	defer timer2.Stop()
	select {
	case <-timer2.C:
		p.logger.Infof("killing entire process group %d", p.cmd.Process.Pid)
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return false, errors.Wrapf(err, "error killing process group %d", p.cmd.Process.Pid)
		}
		forceKilled = true
	case <-p.managingCh:
		timer2.Stop()
	}

	return forceKilled, nil
}

func isWaitErrUnknown(err string, _ bool) bool {
	// TODO: ensure processes handle interrupts gracefully.
	//  For now, ignore signals caused by improper handling of interrupts since they provide no exit code, exception, or
	//  other status information.
	switch err {
	case "signal: interrupt", "signal: terminated", "signal: killed", "signal: segmentation fault":
		return true
	default:
		return false
	}
}
