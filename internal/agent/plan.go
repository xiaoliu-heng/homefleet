package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.@/-]*$`)
var commitID = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

func packageFor(a model.Action, manager string) (string, error) {
	if a.Operation == "upgrade_all" {
		return "", nil
	}
	name := a.Package
	if a.CatalogID != "" {
		found := false
		for _, entry := range model.Catalog {
			if entry.ID == a.CatalogID {
				name = entry.Packages[manager]
				found = true
				break
			}
		}
		if !found || name == "" {
			return "", errors.New("软件目录中没有该系统的包映射")
		}
	}
	if a.Operation != "upgrade_all" && !packageName.MatchString(name) {
		return "", errors.New("请填写准确的软件包名称，不支持参数或 Shell 表达式")
	}
	if strings.Contains(name, "..") {
		return "", errors.New("无效的软件包名称")
	}
	return name, nil
}
func (e *Engine) Preflight(ctx context.Context, a model.Action) (*model.Plan, error) {
	plan := &model.Plan{Steps: []model.Step{}, Warnings: []string{}, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	if e.Config.ReadOnly {
		return nil, errors.New("Agent 处于只读模式，暂不接受修改任务")
	}
	switch a.Kind {
	case "agent":
		return e.agentUpdatePlan(a, plan)
	case "package":
		return e.packagePlan(ctx, a, plan)
	case "project", "compose":
		return e.projectPlan(ctx, a, plan)
	default:
		return nil, errors.New("不支持的任务类型")
	}
}
func (e *Engine) packagePlan(ctx context.Context, a model.Action, p *model.Plan) (*model.Plan, error) {
	return e.packagePlanFor(ctx, a, p, Manager(), isElevated())
}
func (e *Engine) packagePlanFor(ctx context.Context, a model.Action, p *model.Plan, manager string, elevated bool) (*model.Plan, error) {
	if manager == "" {
		return nil, errors.New("没有可用的包管理器")
	}
	name, err := packageFor(a, manager)
	if err != nil {
		return nil, err
	}
	p.Package = name
	if a.Operation != "install" && a.Operation != "upgrade" && a.Operation != "upgrade_all" {
		return nil, errors.New("无效的软件操作")
	}
	identity := "system"
	if manager == "brew" || manager == "winget" && a.Scope == "user" {
		identity = "user"
		if err = e.userReady(); err != nil {
			return nil, err
		}
	} else if !elevated {
		return nil, errors.New("软件安装需要以管理员运行 Agent")
	}
	mk := func(label, program string, args ...string) model.Step {
		return model.Step{Name: label, Program: program, Args: args, Identity: identity, PackageTransaction: true}
	}
	if manager == "apt" {
		if err = e.checkPackageLock(ctx); err != nil {
			return nil, err
		}
		if name != "" {
			if _, err = e.capture(ctx, model.Step{Program: "apt-cache", Args: []string{"show", "--", name}, Identity: "system"}); err != nil {
				return nil, fmt.Errorf("软件源中未找到 %s，请先检查设备软件源", name)
			}
		}
		p.Steps = append(p.Steps, mk("刷新软件索引", "apt-get", "update"))
		if a.Operation == "upgrade_all" {
			p.Steps = append(p.Steps, mk("更新全部可升级软件", "apt-get", "-y", "-o", "Dpkg::Options::=--force-confold", "upgrade"))
			p.Warnings = append(p.Warnings, "更新系统软件可能重启相关服务；不会主动重启操作系统")
		} else {
			args := []string{"-y", "-o", "Dpkg::Options::=--force-confold", "install"}
			if a.Operation == "upgrade" {
				args = append(args, "--only-upgrade")
			}
			args = append(args, "--", name)
			p.Steps = append(p.Steps, mk("安装 / 更新 "+name, "apt-get", args...))
		}
		for i := range p.Steps {
			p.Steps[i].Env = map[string]string{"DEBIAN_FRONTEND": "noninteractive", "NEEDRESTART_MODE": "l"}
		}
	} else if manager == "pacman" {
		if _, err = os.Stat("/var/lib/pacman/db.lck"); err == nil {
			return nil, errors.New("pacman 正被其他进程使用")
		}
		if name != "" {
			if _, err = e.capture(ctx, model.Step{Program: "pacman", Args: []string{"-Si", "--", name}, Identity: "system"}); err != nil {
				return nil, errors.New("当前 pacman 软件源未找到该包；首版不管理 AUR")
			}
		}
		if a.Operation == "install" {
			if err := e.checkArchInstallState(ctx); err != nil {
				return nil, err
			}
			p.Steps = append(p.Steps, mk("安装 "+name+"（不刷新软件源、不全量升级）", "pacman", "-S", "--noconfirm", "--needed", "--", name))
			p.Warnings = append(p.Warnings, "使用本机已有的软件源索引，仅安装目标包及必需依赖；已满足版本要求的软件跳过。不会刷新索引或自动执行完整系统升级。若镜像已移除旧版本，请另行预览系统升级，不会自动扩大本次安装范围。")
		} else {
			args := []string{"-Syu", "--noconfirm", "--needed"}
			if name != "" {
				args = append(args, "--", name)
			}
			p.Steps = append(p.Steps, mk("完整系统升级"+map[bool]string{true: "并更新 " + name, false: ""}[name != ""], "pacman", args...))
			p.Warnings = append(p.Warnings, "Arch 更新会执行完整系统升级（pacman -Syu），包含其他待更新包；可能更新内核、驱动及连接相关服务。执行前请阅读 Arch 更新公告。不会自动重启。")
		}
	} else if manager == "brew" {
		if _, err = e.capture(ctx, model.Step{Program: "brew", Args: []string{"--prefix"}, Identity: "user"}); err != nil {
			return nil, fmt.Errorf("无法以用户 %s 运行 Homebrew（brew --prefix）: %w", e.Config.RunUser, err)
		}
		if name != "" {
			if _, err = e.capture(ctx, model.Step{Program: "brew", Args: []string{"info", "--json=v2", "--", name}, Identity: "user"}); err != nil {
				return nil, fmt.Errorf("Homebrew 查询软件 %s 失败: %w", name, err)
			}
		}
		p.Steps = append(p.Steps, mk("刷新 Homebrew 索引", "brew", "update"))
		op := "upgrade"
		if a.Operation == "install" {
			op = "install"
		}
		args := []string{op}
		if name != "" {
			args = append(args, "--", name)
		}
		step := mk("安装 / 更新 Homebrew 软件", "brew", args...)
		step.Env = map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1", "HOMEBREW_NO_INSTALL_CLEANUP": "1"}
		p.Steps = append(p.Steps, step)
		p.Warnings = append(p.Warnings, "Homebrew 可能同时更新依赖；需要交互或管理员授权的 cask 可能要求在设备端处理")
	} else {
		if identity == "system" {
			if _, err = e.capture(ctx, model.Step{Program: "powershell", Args: []string{"-NoProfile", "-NonInteractive", "-Command", "Import-Module Microsoft.WinGet.Client -ErrorAction Stop"}, Identity: "system"}); err != nil {
				return nil, errors.New("需要先安装 Microsoft.WinGet.Client PowerShell 模块")
			}
			if name != "" {
				check := "$ErrorActionPreference='Stop'; Import-Module Microsoft.WinGet.Client; $p=@(Find-WinGetPackage -Id '" + name + "' -MatchOption Equals -Source winget); if($p.Count -ne 1){throw '未找到唯一的软件包'}"
				if _, err = e.capture(ctx, model.Step{Program: "powershell", Script: check, Identity: "system"}); err != nil {
					return nil, fmt.Errorf("WinGet 无法解析准确的包 ID: %s", name)
				}
			}
			script := systemWinGetScript(a.Operation, name)
			p.Steps = append(p.Steps, model.Step{Name: "WinGet 系统级安装 / 更新", Program: "powershell", Script: script, Identity: "system", PackageTransaction: true})
		} else {
			if _, err = e.capture(ctx, model.Step{Program: "winget", Args: []string{"--version"}, Identity: "user"}); err != nil {
				return nil, errors.New("当前用户会话中 WinGet 不可用")
			}
			if name != "" {
				if _, err = e.capture(ctx, model.Step{Program: "winget", Args: []string{"show", "--id", name, "--exact", "--source", "winget", "--accept-source-agreements", "--disable-interactivity"}, Identity: "user"}); err != nil {
					return nil, errors.New("当前用户的 WinGet 未找到该软件")
				}
			}
			op := "upgrade"
			if a.Operation == "install" {
				op = "install"
			}
			args := []string{op, "--silent", "--disable-interactivity", "--accept-source-agreements", "--accept-package-agreements", "--scope", "user"}
			if a.Operation == "install" {
				args = append(args, "--no-upgrade")
			}
			if a.Operation == "upgrade_all" {
				args = append(args, "--all")
			} else {
				args = append(args, "--id", name, "--exact", "--source", "winget")
			}
			p.Steps = append(p.Steps, mk("WinGet 用户级安装 / 更新", "winget", args...))
		}
		p.Warnings = append(p.Warnings, "只执行支持静默安装的软件；用户级任务需要保持登录，锁屏不影响执行")
	}
	return p, nil
}
func (e *Engine) checkPackageLock(ctx context.Context) error {
	if binary("fuser") != "" {
		b, _ := e.capture(ctx, model.Step{Program: "fuser", Args: []string{"/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock"}, Identity: "system"})
		if strings.TrimSpace(string(b)) != "" {
			return errors.New("apt/dpkg 正被其他进程使用")
		}
	}
	return nil
}
func (e *Engine) projectPlan(ctx context.Context, a model.Action, p *model.Plan) (*model.Plan, error) {
	if err := e.userReady(); err != nil {
		return nil, err
	}
	if a.Project == nil {
		return nil, errors.New("缺少项目快照")
	}
	project := a.Project
	dir := project.Dir(runtime.GOOS)
	if !filepath.IsAbs(dir) {
		return nil, errors.New("请为该系统配置绝对路径的项目目录")
	}
	step := func(name, program string, args ...string) model.Step {
		return model.Step{Name: name, Program: program, Args: args, Directory: dir, Identity: "user", Env: project.Env}
	}
	if a.Kind == "compose" {
		if _, err := e.capture(ctx, step("", "docker", "compose", "version")); err != nil {
			return nil, errors.New("Docker Compose 不可用")
		}
		if _, err := e.capture(ctx, step("", "docker", "info", "--format", "{{.ServerVersion}}")); err != nil {
			return nil, errors.New("项目用户无法连接 Docker 引擎")
		}
		file := project.ComposeFile
		if file == "" {
			file = "compose.yaml"
		}
		if filepath.IsAbs(file) || strings.Contains(file, "..") {
			return nil, errors.New("Compose 文件需在项目目录内")
		}
		base := []string{"compose", "-f", file}
		if _, err := e.captureWithSecrets(ctx, step("", "docker", append(append([]string{}, base...), "config", "--quiet")...), a.Secrets); err != nil {
			return nil, errors.New("Compose 配置未通过检查")
		}
		switch a.Operation {
		case "status":
			p.Steps = append(p.Steps, step("查看容器状态", "docker", append(base, "ps", "--all", "--format", "json")...))
		case "logs":
			p.Steps = append(p.Steps, step("读取最近 200 行日志", "docker", append(base, "logs", "--no-color", "--tail", "200")...))
		case "stop":
			p.Steps = append(p.Steps, step("停止已登记应用", "docker", append(base, "stop")...))
		case "start", "update":
			if a.Operation == "update" {
				p.Steps = append(p.Steps, step("拉取应用镜像", "docker", append(append([]string{}, base...), "pull")...))
			}
			s := step("启动应用并等待健康状态", "docker", append(base, "up", "-d", "--wait", "--wait-timeout", "120")...)
			s.Health = true
			p.Steps = append(p.Steps, s)
			p.Warnings = append(p.Warnings, "Compose 可能重建发生变化的容器；保留已有数据卷，不执行 down -v 或删除孤立容器")
		default:
			return nil, errors.New("不支持的 Compose 操作")
		}
		return p, nil
	}
	platform, ok := project.Platforms[runtime.GOOS]
	if !ok {
		return nil, errors.New("项目未配置该操作系统的部署步骤")
	}
	if len(platform.Steps) == 0 {
		return nil, errors.New("请至少配置一个部署步骤")
	}
	if _, err := e.capture(ctx, model.Step{Program: "git", Args: []string{"--version"}, Identity: "user"}); err != nil {
		return nil, errors.New("项目运行用户的 Git 不可用")
	}
	_, statErr := os.Stat(dir)
	exists := statErr == nil
	if exists {
		root, err := e.capture(ctx, step("", "git", "rev-parse", "--show-toplevel"))
		actual := filepath.Clean(strings.TrimSpace(string(root)))
		want := filepath.Clean(dir)
		if resolved, err := filepath.EvalSymlinks(want); err == nil {
			want = resolved
		}
		if resolved, err := filepath.EvalSymlinks(actual); err == nil {
			actual = resolved
		}
		matches := actual == want
		if runtime.GOOS == "windows" {
			matches = strings.EqualFold(actual, want)
		}
		if err != nil || !matches {
			return nil, errors.New("项目目录必须是 Git 仓库根目录，子目录命令请在部署步骤中切换目录")
		}
		b, err := e.capture(ctx, step("", "git", "status", "--porcelain", "--untracked-files=normal"))
		if err != nil {
			return nil, errors.New("现有目录不是当前用户可访问的 Git 仓库")
		}
		if strings.TrimSpace(string(b)) != "" {
			return nil, errors.New("现有目录有未提交或未跟踪改动，已停止更新")
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	repo := project.Repository
	if exists {
		b, err := e.capture(ctx, step("", "git", "remote", "get-url", "origin"))
		if err != nil {
			return nil, errors.New("仓库缺少 origin")
		}
		actual := strings.TrimSpace(string(b))
		if repo != "" && actual != repo {
			return nil, errors.New("现有 origin 与登记的仓库地址不一致")
		}
		repo = actual
	}
	if repo == "" {
		return nil, errors.New("创建部署目录需要仓库地址")
	}
	ref := project.Ref
	if ref == "" {
		ref = "HEAD"
	}
	sha := ref
	if !commitID.MatchString(sha) {
		s := model.Step{Program: "git", Args: []string{"ls-remote", "--exit-code", repo, ref, ref + "^{}"}, Identity: "user"}
		b, err := e.capture(ctx, s)
		if err != nil {
			return nil, errors.New("无法读取远程 Git 引用，请检查运行用户的凭据和 ref")
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) == 2 && commitID.MatchString(fields[0]) {
				sha = fields[0]
			}
		}
		if !commitID.MatchString(sha) {
			return nil, errors.New("未解析出有效的 Git commit")
		}
	}
	p.ResolvedCommit = sha
	if !exists {
		s := step("创建专用部署目录", "git", "clone", "--no-checkout", "--", repo, dir)
		s.Directory = filepath.Dir(dir)
		if st, err := os.Stat(s.Directory); err != nil || !st.IsDir() {
			return nil, errors.New("部署目录的父目录需已存在并由运行用户可写")
		}
		p.Steps = append(p.Steps, s)
	}
	p.Steps = append(p.Steps, step("获取已确认的版本", "git", "fetch", "origin", sha), step("切换到已确认的 commit", "git", "checkout", "--detach", sha))
	for i, script := range platform.Steps {
		s := step(fmt.Sprintf("部署步骤 %d", i+1), "")
		s.Script = script
		p.Steps = append(p.Steps, s)
	}
	if strings.TrimSpace(platform.HealthCheck) != "" {
		s := step("验证服务健康状态", "")
		s.Script = platform.HealthCheck
		s.Health = true
		p.Steps = append(p.Steps, s)
	} else {
		p.Warnings = append(p.Warnings, "未配置健康检查：只能验证部署命令完成，不能声明服务健康")
	}
	p.Warnings = append(p.Warnings, "执行已保存的部署脚本；失败时保留现场，不自动回滚代码或数据")
	return p, nil
}
