package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

var ErrUpdateDispatched = errors.New("Agent 更新已交给独立更新进程")
var updateIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type updateState struct {
	Schema      int       `json:"schema"`
	TargetID    string    `json:"target_id"`
	FromVersion string    `json:"from_version"`
	Version     string    `json:"version"`
	SHA256      string    `json:"sha256"`
	OldSHA256   string    `json:"old_sha256"`
	Size        int64     `json:"size"`
	Executable  string    `json:"executable"`
	Candidate   string    `json:"candidate"`
	Backup      string    `json:"backup"`
	Helper      string    `json:"helper"`
	Phase       string    `json:"phase"`
	Deadline    time.Time `json:"deadline"`
	Reason      string    `json:"reason,omitempty"`
}
type updateReady struct {
	TargetID string    `json:"target_id"`
	Version  string    `json:"version"`
	At       time.Time `json:"at"`
}

func updateStatePath(c Config) string { return filepath.Join(c.DataDir, "updates", "state.json") }
func updateReadyPath(c Config) string { return filepath.Join(c.DataDir, "updates", "ready.json") }
func readUpdate(c Config) (updateState, error) {
	var u updateState
	raw, err := os.ReadFile(updateStatePath(c))
	if err != nil {
		return u, err
	}
	err = json.Unmarshal(raw, &u)
	if err == nil && (u.Schema != 1 || !updateIDPattern.MatchString(u.TargetID)) {
		err = errors.New("无效的本地升级记录")
	}
	return u, err
}
func updateFinal(phase string) bool {
	return phase == "complete" || phase == "rolled_back" || phase == "failed" || phase == "unknown" || phase == "aborted"
}
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (e *Engine) updateCapability() model.Capability {
	if e.Config.ReadOnly {
		return model.Capability{Reason: "只读 Agent 禁止自更新"}
	}
	if !isElevated() {
		return model.Capability{Reason: "Agent 自更新需要 root / SYSTEM 常驻服务"}
	}
	exe, config, service := managedPaths()
	actual, err := os.Executable()
	if err != nil {
		return model.Capability{Reason: err.Error()}
	}
	if e.Config.Service != service || !samePath(actual, exe) || !samePath(e.Config.ConfigPath, config) {
		return model.Capability{Reason: "需用新版安装命令安装为系统服务后启用后台更新"}
	}
	if u, err := readUpdate(e.Config); err == nil && (!updateFinal(u.Phase) || u.Phase == "unknown") {
		return model.Capability{Reason: "已有升级记录未完成或待核实"}
	} else if err != nil && !os.IsNotExist(err) {
		return model.Capability{Reason: "本地升级记录损坏，需要核实"}
	}
	for _, program := range updateTools() {
		if binary(program) == "" {
			return model.Capability{Reason: "缺少更新工具: " + program}
		}
	}
	return model.Capability{Available: true}
}
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func (e *Engine) agentUpdatePlan(a model.Action, p *model.Plan) (*model.Plan, error) {
	if cap := e.updateCapability(); !cap.Available {
		return nil, errors.New(cap.Reason)
	}
	if a.Operation != "self_update" || a.AgentRelease == nil || a.AgentVersion != a.AgentRelease.Version {
		return nil, errors.New("缺少固定的 Agent 发布版本")
	}
	r := *a.AgentRelease
	if err := r.Validate(); err != nil {
		return nil, err
	}
	minimum, err := model.CompareVersions(model.Version, r.MinimumVersion)
	if err != nil || minimum < 0 {
		return nil, errors.New("当前更新器版本过旧，需要先手动安装新版 Agent")
	}
	cmp, err := model.CompareVersions(model.Version, r.Version)
	if err != nil {
		return nil, err
	}
	if cmp > 0 {
		return nil, errors.New("不允许通过后台降级 Agent")
	}
	asset, ok := r.Artifacts[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok {
		return nil, errors.New("没有对应系统和架构的发布文件")
	}
	p.AgentUpdate = &model.AgentUpdatePlan{FromVersion: model.Version, Version: r.Version, Artifact: asset}
	if cmp == 0 {
		p.Warnings = append(p.Warnings, "已是目标版本，无需下载或重启")
		return p, nil
	}
	exe, _, _ := managedPaths()
	for _, dir := range []string{filepath.Dir(exe), e.Config.DataDir} {
		usage, err := disk.Usage(dir)
		if err != nil {
			return nil, fmt.Errorf("无法检查升级空间: %w", err)
		}
		if usage.Free < uint64(asset.Size*3+(16<<20)) {
			return nil, errors.New("磁盘空间不足，需容纳新版、备份和独立更新器")
		}
		f, err := os.CreateTemp(dir, ".homefleet-write-check-*")
		if err != nil {
			return nil, errors.New("Agent 安装目录或状态目录不可写")
		}
		name := f.Name()
		f.Close()
		os.Remove(name)
	}
	p.Steps = []model.Step{{Name: "下载 Agent " + r.Version + " 并校验 SHA-256", Program: "homefleet-agent", Args: []string{"self-update", "download", r.Version}, Identity: "system"}, {Name: "独立更新器替换文件并重启 Agent", Program: "homefleet-agent", Args: []string{"self-update", "replace"}, Identity: "system"}, {Name: "等待新版本回连，失败时恢复原文件", Program: "homefleet-agent", Args: []string{"self-update", "verify"}, Identity: "system", Health: true}}
	p.Warnings = append(p.Warnings, "仅更新 Agent，保留设备身份、凭据、配置和任务记录；执行前等本机其他任务结束", "Agent 会短暂离线；文件校验通过后才替换，等待新版回连成功才报告完成", "切换文件后会完成更新或回退，不能通过取消强行中断；Windows 用户执行器同步重启，注销时等待下次登录")
	return p, nil
}
func (e *Engine) downloadAgent(ctx context.Context, r model.AgentRelease, asset model.AgentArtifact, path string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	want, ok := r.Artifacts[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok || want != asset {
		return errors.New("发布文件与当前平台不匹配")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", e.Config.HubURL+r.DownloadPath(asset), nil)
	if err != nil {
		return err
	}
	// Use this device's configured domain/IP and CA; never send credentials to a mirror.
	client := *e.Client.HTTP
	client.Timeout = 8 * time.Minute
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("下载发布文件失败: HTTP %d", res.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	okay := false
	defer func() {
		f.Close()
		if !okay {
			os.Remove(path)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, asset.Size+1))
	if err != nil {
		return err
	}
	if n != asset.Size || fmt.Sprintf("%x", h.Sum(nil)) != asset.SHA256 {
		return errors.New("Agent 大小或 SHA-256 不匹配，未替换现有文件")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(path, 0755); err != nil {
		return err
	}
	okay = true
	return nil
}
func verifyCandidate(path, version, config string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("新版无法运行: %w", err)
	}
	if strings.TrimSpace(string(raw)) != version {
		return errors.New("二进制内置版本与发布版本不匹配")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()
	if out, err := exec.CommandContext(ctx2, path, "self-check", "--config", config).CombinedOutput(); err != nil {
		return fmt.Errorf("新版配置自检失败: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
func (e *Engine) prepareUpdate(ctx context.Context, a model.Assignment, emit func(string, string)) (updateState, error) {
	var u updateState
	if a.Plan == nil || a.Plan.AgentUpdate == nil || a.Action.AgentRelease == nil || !updateIDPattern.MatchString(a.TargetID) {
		return u, errors.New("缺少已批准升级计划")
	}
	p, r := a.Plan.AgentUpdate, *a.Action.AgentRelease
	asset, ok := r.Artifacts[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok || p.Version != r.Version || p.FromVersion != model.Version || p.Artifact != asset {
		return u, errors.New("预览后的版本或发布文件发生变化，请重新预览")
	}
	exe, _, _ := managedPaths()
	dir := filepath.Join(e.Config.DataDir, "updates", a.TargetID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return u, err
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	u = updateState{Schema: 1, TargetID: a.TargetID, FromVersion: model.Version, Version: r.Version, SHA256: asset.SHA256, Size: asset.Size, Executable: exe, Candidate: filepath.Join(filepath.Dir(exe), ".homefleet-next-"+a.TargetID+suffix), Backup: filepath.Join(filepath.Dir(exe), ".homefleet-previous-"+a.TargetID+suffix), Helper: filepath.Join(dir, "updater"+suffix), Phase: "prepared", Deadline: time.Now().UTC().Add(10 * time.Minute)}
	ready := false
	defer func() {
		if !ready {
			os.Remove(u.Candidate)
			os.Remove(u.Backup)
			os.Remove(u.Helper)
		}
	}()
	emit("system", "下载 Agent "+r.Version+"；SHA-256: "+asset.SHA256)
	if err := e.downloadAgent(ctx, r, asset, u.Candidate); err != nil {
		return u, err
	}
	if err := verifyCandidate(u.Candidate, r.Version, e.Config.ConfigPath); err != nil {
		return u, err
	}
	var err error
	if u.OldSHA256, err = hashFile(exe); err != nil {
		return u, err
	}
	if err = copyExecutable(exe, u.Backup); err != nil {
		return u, err
	}
	if err = copyExecutable(exe, u.Helper); err != nil {
		return u, err
	}
	cancel, err := e.cancelled(ctx, a.TargetID)
	if err != nil {
		return u, err
	}
	if cancel {
		return u, context.Canceled
	}
	u.Deadline = time.Now().UTC().Add(10 * time.Minute)
	if err = SaveJSON(updateStatePath(e.Config), u); err != nil {
		return u, err
	}
	ready = true
	emit("system", "下载、版本和配置自检通过；已保存原文件，准备交给独立更新器")
	return u, nil
}

// Only the independent helper owns the journal after the main process hands it off.
func (e *Engine) finishUpdate(u *updateState, phase, state, reason, version string) error {
	j, err := e.load(u.TargetID)
	if err != nil {
		return err
	}
	if j.Result == nil {
		j.Logs = append(j.Logs, model.Log{Seq: len(j.Logs) + 1, At: time.Now().UTC(), Stream: "system", Text: reason})
		j.State = state
		j.Result = &model.Result{State: state, Reason: reason, AgentVersion: version, ExitCode: -1, FinishedAt: time.Now().UTC()}
		if state == "succeeded" {
			j.Result.ExitCode = 0
			j.Result.Health = "healthy"
		}
		if err = e.save(j); err != nil {
			return err
		}
	}
	u.Phase = phase
	u.Reason = reason
	return SaveJSON(updateStatePath(e.Config), u)
}

func (e *Engine) settleUpdate(ctx context.Context) error {
	for {
		u, err := readUpdate(e.Config)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if updateFinal(u.Phase) {
			return nil
		}
		if u.Phase == "waiting" && model.Version == u.Version {
			// An actual accepted heartbeat proves the new process can use the saved identity and CA.
			if _, err = e.Client.Request(ctx, "POST", "/agent/v1/heartbeat", map[string]any{"device": e.Device(), "sample": Collect(ctx)}, nil); err == nil {
				if err = SaveJSON(updateReadyPath(e.Config), updateReady{TargetID: u.TargetID, Version: model.Version, At: time.Now().UTC()}); err != nil {
					return err
				}
			}
		}
		if time.Now().After(u.Deadline) {
			unlock, lockErr := lockAgent(filepath.Join(e.Config.DataDir, "updates", "helper.lock"))
			if lockErr == nil {
				latest, readErr := readUpdate(e.Config)
				if readErr == nil && !updateFinal(latest.Phase) {
					readErr = e.finishUpdate(&latest, "unknown", "unknown", "独立更新器未确认结果，请核实设备版本和服务状态；未自动重跑", "")
				}
				unlock()
				return readErr
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

type updateService interface {
	Stop(context.Context) error
	Start(context.Context) error
}

// applyUpdate is separated from OS service commands so tests exercise the real file transaction.
func (e *Engine) applyUpdate(ctx context.Context, u updateState, service updateService, readyTimeout time.Duration) error {
	if u.Phase != "prepared" || time.Now().After(u.Deadline) {
		return errors.New("升级记录已过期或已经处理，禁止重复执行")
	}
	for path, want := range map[string]string{u.Executable: u.OldSHA256, u.Backup: u.OldSHA256, u.Candidate: u.SHA256} {
		h, err := hashFile(path)
		if err != nil || h != want {
			return e.finishUpdate(&u, "failed", "failed", "替换前文件校验失败，保留原 Agent", u.FromVersion)
		}
	}
	cancel, err := e.cancelled(ctx, u.TargetID)
	if err != nil || cancel {
		return e.finishUpdate(&u, "aborted", "cancelled", "无法确认执行许可或任务已取消，未替换文件", u.FromVersion)
	}
	u.Phase = "replacing"
	if err = SaveJSON(updateStatePath(e.Config), u); err != nil {
		return err
	}
	if err = service.Stop(ctx); err != nil {
		return e.finishUpdate(&u, "unknown", "unknown", "无法确认 Agent 服务已停止，未替换文件: "+err.Error(), "")
	}
	rollback := func(cause error) error {
		// Never copy over a still-running Windows executable or a service of uncertain state.
		if err := service.Stop(ctx); err != nil {
			return e.finishUpdate(&u, "unknown", "unknown", "更新失败，停止新版服务失败，需核实: "+err.Error(), "")
		}
		restore := u.Candidate
		os.Remove(restore)
		if err := copyExecutable(u.Backup, restore); err != nil {
			return e.finishUpdate(&u, "unknown", "unknown", "创建恢复文件失败: "+err.Error(), "")
		}
		if err := replaceFile(restore, u.Executable); err != nil {
			return e.finishUpdate(&u, "unknown", "unknown", "恢复原文件失败: "+err.Error(), "")
		}
		// Persist a terminal attempt before starting the previous process, which will replay it.
		if err := e.finishUpdate(&u, "rolled_back", "failed", "新版未完成回连，已恢复原文件: "+cause.Error(), u.FromVersion); err != nil {
			return err
		}
		if err := service.Start(ctx); err != nil {
			// The failed result remains honest about file rollback; service recovery still needs attention.
			j, loadErr := e.load(u.TargetID)
			if loadErr != nil {
				return loadErr
			}
			j.Result = nil
			j.Reported = false
			if err = e.save(j); err != nil {
				return err
			}
			return e.finishUpdate(&u, "unknown", "unknown", "原文件已恢复，但服务启动失败，请手动核实: "+err.Error(), u.FromVersion)
		}
		return nil
	}
	if err = replaceFile(u.Candidate, u.Executable); err != nil {
		return rollback(err)
	}
	os.Remove(updateReadyPath(e.Config))
	u.Phase = "waiting"
	if err = SaveJSON(updateStatePath(e.Config), u); err != nil {
		return rollback(err)
	}
	if err = service.Start(ctx); err != nil {
		return rollback(err)
	}
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ready updateReady
		raw, err := os.ReadFile(updateReadyPath(e.Config))
		if err == nil && json.Unmarshal(raw, &ready) == nil && ready.TargetID == u.TargetID && ready.Version == u.Version && ready.At.After(u.Deadline.Add(-10*time.Minute)) {
			return e.finishUpdate(&u, "complete", "succeeded", "Agent "+u.Version+" 已启动并成功回连控制台", u.Version)
		}
		select {
		case <-ctx.Done():
			return rollback(ctx.Err())
		case <-timer.C:
			return rollback(errors.New("等待新版回连超时"))
		case <-ticker.C:
		}
	}
}

func ApplyUpdate(c Config, id string) error {
	if !isElevated() || !updateIDPattern.MatchString(id) {
		return errors.New("独立更新器需要系统权限和有效任务 ID")
	}
	e, err := NewEngine(c)
	if err != nil {
		return err
	}
	unlock, err := lockAgent(filepath.Join(c.DataDir, "updates", "helper.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	u, err := readUpdate(c)
	if err != nil {
		return err
	}
	exe, config, service := managedPaths()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	if u.TargetID != id || c.Service != service || !samePath(c.ConfigPath, config) || !samePath(u.Executable, exe) || !samePath(u.Candidate, filepath.Join(filepath.Dir(exe), ".homefleet-next-"+id+suffix)) || !samePath(u.Backup, filepath.Join(filepath.Dir(exe), ".homefleet-previous-"+id+suffix)) {
		return errors.New("升级路径或服务与安装记录不匹配")
	}
	defer cleanupUpdater(c, id)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	err = e.applyUpdate(ctx, u, managedService{}, 2*time.Minute)
	// Reporting here also covers rollback where the service could not restart.
	if j, loadErr := e.load(id); loadErr == nil && j.Result != nil {
		report, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		e.flush(report, j)
	}
	return err
}

func MarkService(c Config) error {
	exe, config, service := managedPaths()
	actual, err := os.Executable()
	if err != nil {
		return err
	}
	if !isElevated() || !samePath(c.ConfigPath, config) || !samePath(actual, exe) {
		return errors.New("只能由标准安装脚本登记系统服务")
	}
	unlock, err := lockAgent(filepath.Join(c.DataDir, "agent.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err = os.MkdirAll(filepath.Join(c.DataDir, "updates"), 0700); err != nil {
		return err
	}
	release, err := lockAgent(filepath.Join(c.DataDir, "updates", "helper.lock"))
	if err != nil {
		return errors.New("独立更新器仍在运行，请等待完成后再安装")
	}
	defer release()
	if u, readErr := readUpdate(c); readErr == nil && (!updateFinal(u.Phase) || u.Phase == "unknown") {
		e, engineErr := NewEngine(c)
		if engineErr != nil {
			return engineErr
		}
		if err = e.finishUpdate(&u, "aborted", "unknown", "已手动安装 Agent，原升级任务仍需核实；未自动重跑", model.Version); err != nil {
			return err
		}
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	c.Service = service
	return SaveJSON(c.ConfigPath, c)
}
