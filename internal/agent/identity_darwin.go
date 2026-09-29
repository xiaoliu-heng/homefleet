package agent

import (
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
)

func configureRootUser(cmd *exec.Cmd, u *user.User, uid, gid uint32) error {
	if uid == 0 {
		return fmt.Errorf("用户任务不可使用 root")
	}
	// Darwin's setgroups rejects more than NGROUPS_MAX (16) entries, even
	// though a user can belong to more groups. Let native sudo/initgroups
	// establish the user's membership cache instead of truncating memberships
	// or retaining the root daemon's groups. The command environment has
	// already been filtered by localCommand; -E preserves project variables
	// without putting their values (including secrets) in the argument list.
	program := cmd.Path
	args := append([]string{}, cmd.Args[1:]...)
	cmd.Path = "/usr/bin/sudo"
	cmd.Args = append([]string{cmd.Path, "-n", "-H", "-E", "-u", "#" + strconv.FormatUint(uint64(uid), 10), "-g", "#" + strconv.FormatUint(uint64(gid), 10), "--", program}, args...)
	return nil
}
