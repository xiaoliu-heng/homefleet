//go:build windows

package agent

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
)

func lockAgent(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	overlap := &windows.Overlapped{}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlap); err != nil {
		f.Close()
		return nil, fmt.Errorf("已有 Agent 正在使用该数据目录: %w", err)
	}
	return func() { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlap); f.Close() }, nil
}

func replaceFile(src, dst string) error {
	from, e := windows.UTF16PtrFromString(src)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(dst)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func isElevated() bool             { return windows.GetCurrentProcessToken().IsElevated() }
func (e *Engine) userReady() error { return e.workerReady() }
func configureIdentity(cmd *exec.Cmd, identity, username string) error {
	if identity == "user" {
		return fmt.Errorf("Windows 用户任务必须通过用户执行器")
	}
	return nil
}
