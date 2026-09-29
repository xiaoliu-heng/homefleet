//go:build !windows

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func lockAgent(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("已有 Agent 正在使用该数据目录: %w", err)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
func isElevated() bool { return os.Geteuid() == 0 }
func (e *Engine) userReady() error {
	if e.Config.RunUser == "" {
		return fmt.Errorf("尚未配置项目运行用户")
	}
	u, err := user.Lookup(e.Config.RunUser)
	if err != nil {
		return err
	}
	if u.Uid == "0" {
		return fmt.Errorf("项目运行用户必须是普通用户")
	}
	if os.Geteuid() != 0 && strconv.Itoa(os.Geteuid()) != u.Uid {
		return fmt.Errorf("Agent 无法切换到指定用户")
	}
	return nil
}
func configureIdentity(cmd *exec.Cmd, identity, username string) error {
	if identity != "user" {
		return nil
	}
	u, err := user.Lookup(username)
	if err != nil {
		return err
	}
	if u.Uid == "0" {
		return fmt.Errorf("用户任务不可使用 root")
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("无效的执行用户 UID: %w", err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("无效的执行用户 GID: %w", err)
	}
	if os.Geteuid() == 0 {
		if err := configureRootUser(cmd, u, uint32(uid), uint32(gid)); err != nil {
			return err
		}
	} else if uint64(os.Geteuid()) != uid {
		return fmt.Errorf("不能切换执行用户")
	}
	// Keep project PATH overrides while making Homebrew available to a system daemon.
	path := "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	filtered := []string{}
	for _, value := range cmd.Env {
		key, val, _ := strings.Cut(value, "=")
		if key == "PATH" {
			path += ":" + val
		}
		if key != "HOME" && key != "USER" && key != "LOGNAME" && key != "PATH" {
			filtered = append(filtered, value)
		}
	}
	cmd.Env = append(filtered, "HOME="+u.HomeDir, "USER="+u.Username, "LOGNAME="+u.Username, "PATH="+path)
	if cmd.Dir == "" {
		cmd.Dir = u.HomeDir
	}
	return nil
}
