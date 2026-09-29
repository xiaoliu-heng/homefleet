//go:build !windows && !darwin

package agent

import (
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

func configureRootUser(cmd *exec.Cmd, u *user.User, uid, gid uint32) error {
	ids, err := u.GroupIds()
	if err != nil {
		return fmt.Errorf("无法读取执行用户的用户组: %w", err)
	}
	groups := make([]uint32, 0, len(ids))
	for _, id := range ids {
		v, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return fmt.Errorf("无效的执行用户组 GID: %w", err)
		}
		groups = append(groups, uint32(v))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: groups}}
	return nil
}
