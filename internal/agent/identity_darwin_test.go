package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type identityObservation struct {
	UID, GID                           int
	Home, User, Logname, Path, Project string
	Directory, Argument                string
}

func TestDarwinIdentityHelper(t *testing.T) {
	if os.Getenv("HOMEFLEET_IDENTITY_HELPER") != "1" {
		return
	}
	dir, _ := os.Getwd()
	json.NewEncoder(os.Stdout).Encode(identityObservation{
		UID: os.Getuid(), GID: os.Getgid(), Home: os.Getenv("HOME"), User: os.Getenv("USER"), Logname: os.Getenv("LOGNAME"),
		Path: os.Getenv("PATH"), Project: os.Getenv("PROJECT_VALUE"), Directory: dir, Argument: os.Args[len(os.Args)-1],
	})
	os.Exit(0)
}

func TestDarwinNativeUserLaunch(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if u.Uid == "0" {
		t.Skip("run as a normal macOS user; native sudo permits execution as the same user without a password")
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	argument := "literal $(exit 42); 'quoted' argument"
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestDarwinIdentityHelper$", "--", argument)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"HOMEFLEET_IDENTITY_HELPER=1", "PROJECT_VALUE=fixture secret\nwith spaces", "PATH=/custom/project/bin", "HOME=/var/root", "USER=root", "LOGNAME=root"}
	if err := configureIdentity(cmd, "user", u.Username); err != nil {
		t.Fatal(err)
	}
	// Exercise the exact launcher used by the root daemon, targeting ourselves
	// so this real process test needs no admin password or system changes.
	if err := configureRootUser(cmd, u, uint32(uid), uint32(gid)); err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Credential != nil {
		t.Fatal("Darwin must not pass a potentially oversized group vector to setgroups")
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "fixture secret") {
		t.Fatal("environment secrets leaked into command arguments")
	}
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native user launcher: %v: %s", err, raw)
	}
	var got identityObservation
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if got.UID != int(uid) || got.GID != int(gid) || got.Home != u.HomeDir || got.User != u.Username || got.Logname != u.Username {
		t.Fatalf("wrong execution identity: %+v", got)
	}
	if !strings.Contains(got.Path, "/opt/homebrew/bin") || !strings.Contains(got.Path, "/custom/project/bin") || got.Project != "fixture secret\nwith spaces" || got.Argument != argument {
		t.Fatalf("lost execution context: %+v", got)
	}
	wantDir, err := os.Stat(cmd.Dir)
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := os.Stat(got.Directory)
	if err != nil || !os.SameFile(wantDir, gotDir) {
		t.Fatalf("wrong working directory: %s: %v", got.Directory, err)
	}
	if err := configureRootUser(exec.Command("/usr/bin/true"), &user.User{Uid: "0"}, 0, 0); err == nil {
		t.Fatal("user launcher accepted root as the target")
	}
}

func TestHomebrewPreflightReportsActualFailure(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if u.Uid == "0" {
		t.Skip("test requires a normal Homebrew user")
	}
	for _, failingArg := range []string{"--prefix", "info"} {
		t.Run(failingArg, func(t *testing.T) {
			e := &Engine{Config: Config{RunUser: u.Username}, Runner: func(_ context.Context, step model.Step, _ map[string]string, emit func(string, string)) (int, error) {
				if step.Identity != "user" {
					t.Fatal("Homebrew must run as the configured normal user")
				}
				if step.Args[0] == failingArg {
					emit("stderr", "fixture failure detail")
					return -1, syscall.EINVAL
				}
				return 0, nil
			}}
			_, err := e.packagePlanFor(context.Background(), model.Action{Operation: "install", Package: "git"}, &model.Plan{}, "brew", true)
			if !errors.Is(err, syscall.EINVAL) || !strings.Contains(err.Error(), "fixture failure detail") {
				t.Fatalf("underlying failure was hidden: %v", err)
			}
			if failingArg == "--prefix" && !strings.Contains(err.Error(), u.Username) {
				t.Fatalf("execution user missing: %v", err)
			}
		})
	}
}
