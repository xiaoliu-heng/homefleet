package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type Runner func(context.Context, model.Step, map[string]string, func(string, string)) (int, error)

// A lost executor response must never become a retryable failure.
var ErrUncertain = errors.New("执行结果待核实，禁止自动重跑")

type Engine struct {
	Config    Config
	Client    *Client
	Runner    Runner
	journalMu sync.Mutex
}
type Journal struct {
	Assignment model.Assignment `json:"assignment"`
	State      string           `json:"state"`
	Step       int              `json:"step"`
	Result     *model.Result    `json:"result,omitempty"`
	Logs       []model.Log      `json:"logs"`
	Ack        int              `json:"ack"`
	Reported   bool             `json:"reported"`
}

func NewEngine(c Config) (*Engine, error) {
	client, e := NewClient(c)
	if e != nil {
		return nil, e
	}
	return &Engine{Config: c, Client: client}, nil
}
func (e *Engine) capture(ctx context.Context, step model.Step) ([]byte, error) {
	return e.captureWithSecrets(ctx, step, nil)
}
func (e *Engine) captureWithSecrets(ctx context.Context, step model.Step, secrets map[string]string) ([]byte, error) {
	var output bytes.Buffer
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := e.command(ctx, step, secrets, func(stream, text string) {
		if stream == "stdout" {
			output.WriteString(text + "\n")
		} else {
			stderr.WriteString(text + "\n")
		}
	})
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return output.Bytes(), fmt.Errorf("%w: %s", err, detail)
		}
		return output.Bytes(), err
	}
	return output.Bytes(), nil
}
func (e *Engine) command(ctx context.Context, s model.Step, secrets map[string]string, emit func(string, string)) (int, error) {
	if e.Runner != nil {
		return e.Runner(ctx, s, secrets, emit)
	}
	if runtime.GOOS == "windows" && s.Identity == "user" {
		return e.workerCommand(ctx, s, secrets, emit)
	}
	return localCommand(ctx, s, secrets, e.Config.RunUser, emit)
}
func localCommand(ctx context.Context, s model.Step, secrets map[string]string, username string, emit func(string, string)) (int, error) {
	program := s.Program
	args := append([]string{}, s.Args...)
	if s.Script != "" {
		if runtime.GOOS == "windows" {
			program = "powershell"
			args = []string{"-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); $OutputEncoding=[Console]::OutputEncoding; $ErrorActionPreference='Stop'; " + s.Script + "; if ($LASTEXITCODE) { exit $LASTEXITCODE }"}
		} else {
			program = "bash"
			args = []string{"-e", "-o", "pipefail", "-c", s.Script}
		}
	}
	if p := binary(program); p != "" {
		program = p
	}
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = s.Directory
	// Explicit maps deduplicate inherited variables so configured values really win.
	env := map[string]string{}
	for _, v := range os.Environ() {
		k, val, ok := strings.Cut(v, "=")
		if ok {
			if s.Identity == "user" && isElevated() && k != "PATH" && k != "LANG" && k != "LC_ALL" && k != "TZ" {
				continue
			}
			env[k] = val
		}
	}
	for k, v := range s.Env {
		env[k] = v
	}
	for k, v := range secrets {
		env[k] = v
	}
	env["GIT_TERMINAL_PROMPT"] = "0"
	env["GCM_INTERACTIVE"] = "Never"
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if err := configureIdentity(cmd, s.Identity, username); err != nil {
		return -1, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err = cmd.Start(); err != nil {
		return -1, err
	}
	// A grandchild can retain inherited pipes after CommandContext kills its
	// parent. Close readers on timeout so the agent can report an unknown outcome.
	readDone := make(chan struct{})
	defer close(readDone)
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				stdout.Close()
				stderr.Close()
			case <-readDone:
			}
		}()
	}
	var wg sync.WaitGroup
	var outputMu sync.Mutex
	read := func(stream string, r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 4096), 1024*1024)
		for sc.Scan() {
			text := sc.Text()
			if len(text) > 60000 {
				text = text[:60000] + " … [truncated]"
			}
			outputMu.Lock()
			emit(stream, text)
			outputMu.Unlock()
		}
		if err := sc.Err(); err != nil {
			outputMu.Lock()
			emit("stderr", "读取输出失败: "+err.Error())
			outputMu.Unlock()
		}
	}
	wg.Add(2)
	go read("stdout", stdout)
	go read("stderr", stderr)
	wg.Wait()
	err = cmd.Wait()
	if ctx.Err() != nil {
		return -1, fmt.Errorf("%w: %v", ErrUncertain, ctx.Err())
	}
	code := 0
	if err != nil {
		code = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
	}
	if s.Program == "winget" && len(s.Args) > 0 && (s.Args[0] == "install" || s.Args[0] == "upgrade") && (uint32(code) == 0x8A15002B || uint32(code) == 0x8A150061) {
		emit("stdout", "软件已满足要求，无需变更")
		return 0, nil
	}
	return code, err
}
func Redact(text string, secrets map[string]string) string {
	values := []string{}
	for _, value := range secrets {
		if value != "" {
			values = append(values, value)
			for _, line := range strings.Split(value, "\n") {
				if line != "" {
					values = append(values, line)
				}
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[REDACTED]")
	}
	return text
}
func (e *Engine) journalPath(id string) string {
	return filepath.Join(e.Config.DataDir, "journal", id+".json")
}
func (e *Engine) save(j *Journal) error { return SaveJSON(e.journalPath(j.Assignment.TargetID), j) }
func (e *Engine) load(id string) (*Journal, error) {
	raw, err := os.ReadFile(e.journalPath(id))
	if err != nil {
		return nil, err
	}
	var j Journal
	err = json.Unmarshal(raw, &j)
	return &j, err
}
func (e *Engine) recover(ctx context.Context) error {
	paths, err := filepath.Glob(filepath.Join(e.Config.DataDir, "journal", "*.json"))
	if err != nil {
		return err
	}
	known := []string{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var j Journal
		if err = json.Unmarshal(raw, &j); err != nil {
			return fmt.Errorf("损坏的执行记录 %s: %w; 停止领取任务", p, err)
		}
		known = append(known, j.Assignment.TargetID)
		if j.Result == nil {
			state := "unknown"
			if j.Assignment.Mode != "execute" {
				state = "failed"
			}
			j.Result = &model.Result{State: state, Reason: "Agent 在任务中途重启，未自动重新执行", ExitCode: -1, FinishedAt: time.Now().UTC()}
			j.State = state
			if err = e.save(&j); err != nil {
				return err
			}
		}
		if !j.Reported {
			e.flush(ctx, &j)
		}
	}
	_, err = e.Client.Request(ctx, "POST", "/agent/v1/reconcile", map[string]any{"known": known}, nil)
	return err
}
func (e *Engine) flush(ctx context.Context, j *Journal) error {
	for j.Ack < len(j.Logs) {
		end, size := j.Ack, 2
		for end < len(j.Logs) && end-j.Ack < 200 {
			raw, err := json.Marshal(j.Logs[end])
			if err != nil {
				return err
			}
			if end > j.Ack && size+len(raw)+1 > 1<<20 {
				break
			}
			size += len(raw) + 1
			end++
		}
		if _, err := e.Client.Request(ctx, "POST", "/agent/v1/targets/"+j.Assignment.TargetID+"/logs", j.Logs[j.Ack:end], nil); err != nil {
			return err
		}
		j.Ack = end
		if err := e.save(j); err != nil {
			return err
		}
	}
	if j.Result != nil && !j.Reported {
		if _, err := e.Client.Request(ctx, "POST", "/agent/v1/targets/"+j.Assignment.TargetID+"/result", j.Result, nil); err != nil {
			return err
		}
		j.Reported = true
		return e.save(j)
	}
	return nil
}
func (e *Engine) cancelled(ctx context.Context, id string) (bool, error) {
	var v struct {
		Cancel bool   `json:"cancel"`
		State  string `json:"state"`
	}
	_, err := e.Client.Request(ctx, "GET", "/agent/v1/targets/"+id+"/control", nil, &v)
	return v.Cancel || model.Terminal(v.State), err
}
func (e *Engine) Handle(ctx context.Context, a model.Assignment) error {
	// A duplicate assignment can only replay its recorded outcome, never its commands.
	if old, err := e.load(a.TargetID); err == nil {
		// A restored hub can have lost acknowledgements that this agent has kept.
		// Re-send the existing attempt, including logs, without running it again.
		old.Ack = 0
		old.Reported = false
		if old.Result == nil {
			old.Result = &model.Result{State: "unknown", Reason: "已有未完成的本地记录，未重复执行", FinishedAt: time.Now().UTC()}
			old.State = "unknown"
		}
		if err := e.save(old); err != nil {
			return err
		}
		return e.flush(ctx, old)
	} else if !os.IsNotExist(err) {
		return err
	}
	saved := a
	saved.Action.Secrets = nil
	j := &Journal{Assignment: saved, State: "accepted", Logs: []model.Log{}}
	if err := e.save(j); err != nil {
		return err
	}
	secrets := a.Action.Secrets
	var jm sync.Mutex
	stopped := make(chan struct{})
	flushed := make(chan struct{})
	var stopOnce sync.Once
	stopFlush := func() { stopOnce.Do(func() { close(stopped) }); <-flushed }
	var journalErr error
	logBytes := 0
	emit := func(stream, text string) {
		jm.Lock()
		defer jm.Unlock()
		text = Redact(text, secrets)
		if logBytes > 8<<20 {
			return
		}
		logBytes += len(text)
		j.Logs = append(j.Logs, model.Log{Seq: len(j.Logs) + 1, At: time.Now().UTC(), Stream: stream, Text: text})
		if err := e.save(j); err != nil {
			journalErr = err
		}
	}
	go func() {
		defer close(flushed)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ticker.C:
				jm.Lock()
				c, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				e.flush(c, j)
				cancel()
				jm.Unlock()
			}
		}
	}()
	var result model.Result
	handedOff := false
	finish := func() {
		stopFlush()
		if handedOff {
			return
		}
		jm.Lock()
		defer jm.Unlock()
		if journalErr != nil {
			result.State = "unknown"
			result.Reason = "无法持久化执行日志，请核实设备结果"
		}
		result.Reason = Redact(result.Reason, secrets)
		result.FinishedAt = time.Now().UTC()
		j.Result = &result
		j.State = result.State
		if err := e.save(j); err != nil {
			log.Print(err)
			return
		}
		e.flush(ctx, j)
	}
	defer finish()
	if a.Mode == "inspect" {
		inv := e.Inventory(ctx, a.Action.Scope)
		result = model.Result{State: "succeeded", Inventory: &inv}
		if len(inv.Packages) == 0 && inv.Error != "" {
			result.State = "failed"
			result.Reason = inv.Error
		}
		return nil
	}
	if a.Mode == "preview" {
		emit("system", "正在检查设备能力、执行账号和操作范围")
		p, err := e.Preflight(ctx, a.Action)
		if err != nil {
			result = model.Result{State: "blocked", Reason: err.Error()}
			return nil
		}
		result = model.Result{State: "succeeded", Plan: p}
		emit("system", "预检查通过；等待控制台确认")
		return nil
	}
	if a.Mode != "execute" || a.Plan == nil {
		result = model.Result{State: "blocked", Reason: "缺少已确认计划"}
		return nil
	}
	if e.Config.ReadOnly {
		result = model.Result{State: "blocked", Reason: "只读 Agent 禁止修改"}
		return nil
	}

	// Revalidate local state, but execute only the immutable, approved commands.
	checkAction := a.Action
	if checkAction.Kind == "project" && checkAction.Project != nil {
		p := *checkAction.Project
		p.Ref = a.Plan.ResolvedCommit
		checkAction.Project = &p
	}
	fresh, err := e.Preflight(ctx, checkAction)
	if err != nil {
		result = model.Result{State: "blocked", Reason: "执行前复检失败: " + err.Error()}
		return nil
	}
	if checkAction.Kind == "package" && checkAction.Operation == "install" && Manager() == "pacman" && !reflect.DeepEqual(fresh.Steps, a.Plan.Steps) {
		result = model.Result{State: "blocked", Reason: "Arch 安装方式或执行范围已变化，请重新预览；未执行旧计划中的系统升级"}
		return nil
	}
	result = model.Result{State: "succeeded", Health: "not_configured"}
	if a.Action.Kind == "agent" {
		if a.Plan.AgentUpdate == nil {
			result.State = "blocked"
			result.Reason = "缺少已批准 Agent 更新计划"
			return nil
		}
		if a.Plan.AgentUpdate.Version == model.Version {
			result.AgentVersion = model.Version
			result.Reason = "已是目标版本，无需变更"
			return nil
		}
		u, err := e.prepareUpdate(ctx, a, emit)
		if err != nil {
			result.State = "failed"
			result.Reason = err.Error()
			if errors.Is(err, context.Canceled) {
				result.State = "cancelled"
				result.Reason = "更新在替换文件前已取消"
			}
			return nil
		}
		stopFlush()
		jm.Lock()
		j.State = "updating"
		err = e.save(j)
		jm.Unlock()
		if err != nil || journalErr != nil {
			u.Phase = "aborted"
			SaveJSON(updateStatePath(e.Config), u)
			result.State = "unknown"
			result.Reason = "无法持久化更新交接记录，未启动更新器"
			return nil
		}
		handedOff = true
		if err = dispatchUpdater(e.Config, u); err != nil {
			// Dispatch can time out after the OS has started the helper. Only abort while holding its lock.
			if unlock, lockErr := lockAgent(filepath.Join(e.Config.DataDir, "updates", "helper.lock")); lockErr == nil {
				latest, readErr := readUpdate(e.Config)
				if readErr == nil && latest.Phase == "prepared" {
					e.finishUpdate(&latest, "aborted", "failed", "无法启动独立更新器，未替换文件: "+err.Error(), model.Version)
				}
				unlock()
			}
		}
		return ErrUpdateDispatched
	}
	cloned := false
	for i, step := range a.Plan.Steps {
		cancel, err := e.cancelled(ctx, a.TargetID)
		if err != nil {
			result.State = "unknown"
			result.Reason = "无法确认控制台状态，停止后续步骤"
			break
		}
		if cancel {
			result.State = "cancelled"
			result.Reason = "已完成当前步骤，停止后续操作"
			break
		}
		if !cloned && a.Action.Kind == "project" && step.Program == "git" && len(step.Args) > 0 && step.Args[0] == "checkout" {
			b, checkErr := e.capture(context.Background(), model.Step{Program: "git", Args: []string{"status", "--porcelain", "--untracked-files=normal"}, Directory: step.Directory, Identity: "user"})
			if checkErr != nil || strings.TrimSpace(string(b)) != "" {
				result.State = "blocked"
				result.Reason = "切换 commit 前检测到工作目录发生改动或无法检查，已停止"
				break
			}
		}
		jm.Lock()
		j.State = "running"
		j.Step = i
		err = e.save(j)
		jm.Unlock()
		if err != nil {
			result.State = "unknown"
			result.Reason = "执行前无法写入本地记录"
			break
		}
		emit("system", fmt.Sprintf("[%d/%d] %s", i+1, len(a.Plan.Steps), step.Name))
		// Package transactions are not killed on cancellation or network loss.
		runCtx := context.Background()
		var cancelRun context.CancelFunc = func() {}
		if !step.PackageTransaction {
			runCtx, cancelRun = context.WithTimeout(context.Background(), 30*time.Minute)
		}
		code, err := e.command(runCtx, step, secrets, emit)
		cancelRun()
		result.ExitCode = code
		if err != nil {
			result.State = "failed"
			if errors.Is(err, ErrUncertain) {
				result.State = "unknown"
			}
			result.Reason = fmt.Sprintf("%s: %v", step.Name, err)
			if step.Health {
				result.Health = "failed"
			}
			emit("stderr", result.Reason)
			break
		}
		if step.Health {
			result.Health = "healthy"
		}
		if step.Program == "git" && len(step.Args) > 0 && step.Args[0] == "clone" {
			cloned = true
		}
		emit("system", step.Name+" 完成")
		jm.Lock()
		j.Step = i + 1
		saveErr := e.save(j)
		jm.Unlock()
		if saveErr != nil {
			result.State = "unknown"
			result.Reason = "步骤完成后无法持久化记录"
			break
		}
	}
	if a.Action.Kind == "project" && a.Action.Project != nil {
		b, err := e.capture(context.Background(), model.Step{Program: "git", Args: []string{"rev-parse", "HEAD"}, Directory: a.Action.Project.Dir(runtime.GOOS), Identity: "user"})
		if actual := strings.TrimSpace(string(b)); err == nil && commitID.MatchString(actual) {
			result.Commit = actual
		}
	}
	return nil
}
func (e *Engine) Run(ctx context.Context) error {
	if e.Config.Token == "" {
		return errors.New("Agent 尚未注册")
	}
	if err := os.MkdirAll(e.Config.DataDir, 0700); err != nil {
		return err
	}
	unlock, err := lockAgent(filepath.Join(e.Config.DataDir, "agent.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err := e.settleUpdate(ctx); err != nil {
		return err
	}
	if err := e.recover(ctx); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			sample := Collect(ctx)
			device := e.Device()
			if _, err := e.Client.Request(ctx, "POST", "/agent/v1/heartbeat", map[string]any{"device": device, "sample": sample}, nil); err != nil {
				log.Printf("heartbeat: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		// Retry durable unsent outcomes after connectivity recovers.
		paths, _ := filepath.Glob(filepath.Join(e.Config.DataDir, "journal", "*.json"))
		for _, p := range paths {
			j, err := e.load(strings.TrimSuffix(filepath.Base(p), ".json"))
			if err != nil {
				return err
			}
			if !j.Reported && j.Result != nil {
				if err = e.flush(ctx, j); err != nil {
					log.Printf("outbox: %v", err)
				}
			}
		}
		var assignment model.Assignment
		status, err := e.Client.Request(ctx, "GET", "/agent/v1/claim", nil, &assignment)
		if err != nil {
			log.Printf("claim: %v", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if status == 204 {
			continue
		}
		if err = e.Handle(ctx, assignment); err != nil {
			if errors.Is(err, ErrUpdateDispatched) {
				if err = e.settleUpdate(ctx); err != nil {
					return err
				}
				if err = e.recover(ctx); err != nil {
					return err
				}
				continue
			}
			return err
		}
	}
}
