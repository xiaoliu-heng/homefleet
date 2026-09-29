package agent

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGPUParsersPreserveMissingAndSharedMemory(t *testing.T) {
	gs := ParseNVIDIA("GPU-a, RTX 4090, 38, 2048, 24576, 55, 555.1\nGPU-b, RTX 3060, [N/A], [N/A], 12288, [N/A], 555.1\n")
	if len(gs) != 2 || *gs[0].MemoryUsed != 2048*1024*1024 || *gs[0].Utilization != 38 {
		t.Fatalf("%+v", gs)
	}
	if gs[1].Utilization != nil || gs[1].Temperature != nil || gs[1].MemoryUsed != nil || gs[1].Error == "" {
		t.Fatal("missing metrics must not be zero")
	}
	for _, input := range []string{`{"gpu_active_ratio":0.42,"gpu_scaled_ratio":0.1}`, `{"gpu_usage":[500,0.42]}`, `{"gpu_scaled_ratio":0.42}`} {
		v := ParseMacmon(input)
		if v == nil || *v != 42 {
			t.Fatalf("macmon %s = %v", input, v)
		}
	}
	for _, input := range []string{`{"gpu_active_ratio":-1}`, `{"gpu_usage":"unsupported"}`, `{"gpu_usage":null}`, `{"gpu_usage":999}`} {
		if ParseMacmon(input) != nil {
			t.Fatalf("unsupported value accepted: %s", input)
		}
	}
	if floatp("NaN") != nil || bytesp("-1") != nil {
		t.Fatal("invalid telemetry accepted")
	}
}
func TestPackageNamesRejectCommandInjection(t *testing.T) {
	if name, err := packageFor(model.Action{Operation: "upgrade_all", CatalogID: "git", Package: "git"}, "brew"); err != nil || name != "" {
		t.Fatal("update all was narrowed to selected package")
	}
	for _, name := range []string{"git;touch /tmp/bad", "--force", "$(id)", "git\ncurl", "../git", "git'"} {
		if _, err := packageFor(model.Action{Operation: "install", Package: name}, "apt"); err == nil {
			t.Fatal(name)
		}
	}
	name, err := packageFor(model.Action{Operation: "install", CatalogID: "git"}, "winget")
	if err != nil || name != "Git.Git" {
		t.Fatal("catalog mapping failed")
	}
}
func TestArchSeparatesInstallationFromSystemUpgrade(t *testing.T) {
	var checks []model.Step
	e := &Engine{Runner: func(_ context.Context, step model.Step, _ map[string]string, _ func(string, string)) (int, error) {
		checks = append(checks, step)
		if step.Args[0] == "-Qu" {
			return 1, errors.New("exit status 1") // no outdated packages
		}
		return 0, nil
	}}
	for _, op := range []string{"install", "upgrade", "upgrade_all"} {
		checks = nil
		p, err := e.packagePlanFor(context.Background(), model.Action{Operation: op, Package: "git"}, &model.Plan{}, "pacman", true)
		want := "-Syu"
		if op == "install" {
			want = "-S"
		}
		if err != nil || len(p.Steps) != 1 || p.Steps[0].Args[0] != want || !p.Steps[0].PackageTransaction || len(p.Warnings) == 0 {
			t.Fatalf("%+v %v", p, err)
		}
		for _, check := range checks {
			if check.Program != "pacman" || (check.Args[0] != "-Si" && check.Args[0] != "-Qu") {
				t.Fatalf("preflight must not refresh repositories or modify packages: %+v", check)
			}
		}
		if op == "install" && (len(checks) != 2 || checks[1].Args[0] != "-Qu") {
			t.Fatalf("cached install skipped consistency check: %+v", checks)
		}
	}
	for _, manager := range []string{"apt", "pacman"} {
		if _, err := e.packagePlanFor(context.Background(), model.Action{Operation: "install", Package: "git"}, &model.Plan{}, manager, false); err == nil {
			t.Fatal("unprivileged system installation")
		}
	}
}

func TestArchInstallBlocksPartialUpgradeAndQueryFailures(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr string
		code                 int
		err                  error
	}{
		{name: "failed full upgrade left new indexes", stdout: "qemu-common 11.0.1-1 -> 11.1.1-4\nlinux 7.0.12-1 -> 7.1.1-1"},
		{name: "query failed", stderr: "error: could not open local database", code: 1, err: errors.New("exit status 1")},
		{name: "missing sync database", stderr: "warning: database file for extra does not exist"},
		{name: "process cannot start", code: -1, err: errors.New("permission denied")},
		{name: "unexpected exit", code: 2, err: errors.New("exit status 2")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{Runner: func(_ context.Context, step model.Step, _ map[string]string, emit func(string, string)) (int, error) {
				if step.Args[0] == "-Si" {
					return 0, nil
				}
				if step.Args[0] != "-Qu" {
					t.Fatalf("unexpected command: %+v", step)
				}
				if tc.stdout != "" {
					emit("stdout", tc.stdout)
				}
				if tc.stderr != "" {
					emit("stderr", tc.stderr)
				}
				return tc.code, tc.err
			}}
			p, err := e.packagePlanFor(context.Background(), model.Action{Operation: "install", Package: "pacman-contrib"}, &model.Plan{}, "pacman", true)
			if err == nil || p != nil {
				t.Fatalf("unsafe install accepted: %+v, %v", p, err)
			}
			if tc.stdout != "" && (!strings.Contains(err.Error(), "2 项待更新") || !strings.Contains(err.Error(), "qemu-common")) {
				t.Fatalf("partial-upgrade reason missing: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &Engine{Runner: func(context.Context, model.Step, map[string]string, func(string, string)) (int, error) {
		return 1, context.Canceled
	}}
	if err := e.checkArchInstallState(ctx); err == nil {
		t.Fatal("cancelled query was treated as no updates")
	}
}
func TestWinGetSystemContextUsesSupportedModuleScope(t *testing.T) {
	e := &Engine{Runner: func(context.Context, model.Step, map[string]string, func(string, string)) (int, error) { return 0, nil }}
	p, err := e.packagePlanFor(context.Background(), model.Action{Operation: "install", Package: "Git.Git"}, &model.Plan{}, "winget", true)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Steps[0]
	if s.Identity != "system" || !strings.Contains(s.Script, "-Scope System") || strings.Contains(s.Script, "-Scope Machine") || !strings.Contains(s.Script, "Get-WinGetPackage") {
		t.Fatalf("%+v", s)
	}
}

type fakeHub struct {
	mu         sync.Mutex
	result     model.Result
	logs       map[int]model.Log
	cancel     bool
	failResult bool
}

func newTestEngine(t *testing.T) (*Engine, *fakeHub) {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	username := u.Username
	if u.Uid == "0" {
		username = "nobody"
	}
	f := &fakeHub{logs: map[int]model.Log{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/health":
			json.NewEncoder(w).Encode(map[string]string{"user": username})
		case strings.HasSuffix(r.URL.Path, "/control"):
			json.NewEncoder(w).Encode(map[string]any{"cancel": f.cancel, "state": "running"})
		case strings.HasSuffix(r.URL.Path, "/logs"):
			var logs []model.Log
			json.NewDecoder(r.Body).Decode(&logs)
			for _, l := range logs {
				f.logs[l.Seq] = l
			}
			w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/result"):
			if f.failResult {
				http.Error(w, "temporary failure", 503)
				return
			}
			json.NewDecoder(r.Body).Decode(&f.result)
			w.Write([]byte("{}"))
		default:
			w.Write([]byte("{}"))
		}
	}))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	tokenFile := filepath.Join(root, "worker.token")
	os.WriteFile(tokenFile, []byte(strings.Repeat("x", 40)), 0600)
	e, err := NewEngine(Config{HubURL: srv.URL, DataDir: root, RunUser: username, WorkerURL: srv.URL, WorkerTokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	return e, f
}
func assignment(t *testing.T) model.Assignment {
	t.Helper()
	sha := strings.Repeat("a", 40)
	project := &model.Project{Repository: "https://example.test/app.git", Directory: t.TempDir(), Ref: sha, Platforms: map[string]model.PlatformConfig{runtime.GOOS: {Steps: []string{"safe test script"}}}}
	return model.Assignment{TargetID: "test-target", JobID: "test-job", Mode: "execute", Action: model.Action{Kind: "project", Operation: "deploy", Project: project}, Plan: &model.Plan{ResolvedCommit: sha, Steps: []model.Step{{Name: "test deployment", Script: "safe test script", Identity: "user"}}}}
}
func preflightOutput(step model.Step, emit func(string, string)) {
	if strings.Join(step.Args, " ") == "rev-parse --show-toplevel" {
		emit("stdout", step.Directory)
	}
	if strings.Join(step.Args, " ") == "remote get-url origin" {
		emit("stdout", "https://example.test/app.git")
	}
	if strings.Join(step.Args, " ") == "rev-parse HEAD" {
		emit("stdout", strings.Repeat("b", 40))
	}
}
func TestDurableDuplicateDeliveryAndSecretRedaction(t *testing.T) {
	e, f := newTestEngine(t)
	a := assignment(t)
	secret := "my-private-token-987654"
	a.Action.Secrets = map[string]string{"TOKEN": secret}
	count := 0
	e.Runner = func(_ context.Context, s model.Step, secrets map[string]string, emit func(string, string)) (int, error) {
		preflightOutput(s, emit)
		if s.Script != "" {
			count++
			if secrets["TOKEN"] != secret {
				t.Fatal("secret not delivered")
			}
			emit("stdout", "token="+secret)
		}
		return 0, nil
	}
	if err := e.Handle(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	// Simulate a hub restored from a snapshot before these acknowledgements.
	f.mu.Lock()
	f.logs = map[int]model.Log{}
	f.result = model.Result{}
	f.mu.Unlock()
	if err := e.Handle(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("executed %d times", count)
	}
	if len(f.logs) == 0 {
		t.Fatal("duplicate delivery failed to restore server logs")
	}
	raw, _ := os.ReadFile(e.journalPath(a.TargetID))
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret persisted in journal")
	}
	if f.result.State != "succeeded" || f.result.Commit != strings.Repeat("b", 40) {
		t.Fatalf("must report actual commit, not intended ref: %+v", f.result)
	}
	for _, l := range f.logs {
		if strings.Contains(l.Text, secret) {
			t.Fatal("secret sent to hub")
		}
	}
}
func TestCrashRecoveryNeverReplaysCommands(t *testing.T) {
	e, f := newTestEngine(t)
	a := assignment(t)
	if err := e.save(&Journal{Assignment: a, State: "running", Logs: []model.Log{{Seq: 1, Text: "before crash"}}}); err != nil {
		t.Fatal(err)
	}
	e.Runner = func(context.Context, model.Step, map[string]string, func(string, string)) (int, error) {
		t.Fatal("replayed command")
		return 0, nil
	}
	if err := e.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.result.State != "unknown" || len(f.logs) != 1 {
		t.Fatalf("%+v", f.result)
	}
	if err := e.Handle(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}
func TestInterruptedResultDeliveryResumesLogsWithoutExecution(t *testing.T) {
	e, f := newTestEngine(t)
	a := assignment(t)
	count := 0
	f.failResult = true
	e.Runner = func(_ context.Context, s model.Step, _ map[string]string, emit func(string, string)) (int, error) {
		preflightOutput(s, emit)
		if s.Script != "" {
			count++
			emit("stdout", "real output")
		}
		return 0, nil
	}
	e.Handle(context.Background(), a)
	j, _ := e.load(a.TargetID)
	if j.Reported || j.Result == nil || j.Ack == 0 {
		t.Fatal("outbox not persisted")
	}
	f.mu.Lock()
	f.failResult = false
	f.mu.Unlock()
	restarted, err := NewEngine(e.Config)
	if err != nil {
		t.Fatal(err)
	}
	restarted.Runner = func(context.Context, model.Step, map[string]string, func(string, string)) (int, error) {
		t.Fatal("replayed on restart")
		return 0, nil
	}
	if err := restarted.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count != 1 || f.result.State != "succeeded" || len(f.logs) != len(j.Logs) {
		t.Fatal("recovery lost or repeated work")
	}
}
func TestCancellationFinishesCurrentTransactionAndSkipsNext(t *testing.T) {
	e, f := newTestEngine(t)
	a := assignment(t)
	a.Plan.Steps[0].PackageTransaction = true
	a.Plan.Steps = append(a.Plan.Steps, model.Step{Name: "must not run", Script: "second"})
	count := 0
	e.Runner = func(ctx context.Context, s model.Step, _ map[string]string, emit func(string, string)) (int, error) {
		preflightOutput(s, emit)
		if s.Script != "" {
			count++
			if ctx.Done() != nil {
				t.Fatal("transaction has cancellable context")
			}
			f.mu.Lock()
			f.cancel = true
			f.mu.Unlock()
		}
		return 0, nil
	}
	e.Handle(context.Background(), a)
	if count != 1 || f.result.State != "cancelled" {
		t.Fatalf("%d %+v", count, f.result)
	}
}
func TestUncertainExecutionIsNotRetryableFailure(t *testing.T) {
	e, f := newTestEngine(t)
	a := assignment(t)
	e.Runner = func(_ context.Context, s model.Step, _ map[string]string, emit func(string, string)) (int, error) {
		preflightOutput(s, emit)
		if s.Script != "" {
			return -1, ErrUncertain
		}
		return 0, nil
	}
	e.Handle(context.Background(), a)
	if f.result.State != "unknown" {
		t.Fatalf("%+v", f.result)
	}
}
func TestDirtyGitDirectoryBlocksBeforeMutation(t *testing.T) {
	e, _ := newTestEngine(t)
	a := assignment(t)
	e.Runner = func(_ context.Context, s model.Step, _ map[string]string, emit func(string, string)) (int, error) {
		preflightOutput(s, emit)
		if len(s.Args) > 0 && s.Args[0] == "status" {
			emit("stdout", " M modified.txt")
		}
		if len(s.Args) > 0 && (s.Args[0] == "fetch" || s.Args[0] == "checkout") {
			t.Fatal("mutated git during preflight")
		}
		return 0, nil
	}
	if _, err := e.Preflight(context.Background(), a.Action); err == nil || !strings.Contains(err.Error(), "改动") {
		t.Fatalf("%v", err)
	}
}
func TestComposeSecretsUsedForCheckButNeverSavedInPlan(t *testing.T) {
	e, _ := newTestEngine(t)
	a := assignment(t)
	a.Action.Kind = "compose"
	a.Action.Operation = "update"
	a.Action.Secrets = map[string]string{"TOKEN": "secret-value"}
	checked := false
	e.Runner = func(_ context.Context, s model.Step, secrets map[string]string, _ func(string, string)) (int, error) {
		if strings.Contains(strings.Join(s.Args, " "), "config --quiet") {
			checked = true
			if secrets["TOKEN"] != "secret-value" {
				return 1, errors.New("missing secret")
			}
		}
		return 0, nil
	}
	p, err := e.Preflight(context.Background(), a.Action)
	if err != nil || !checked {
		t.Fatalf("%v", err)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "secret-value") {
		t.Fatal("secret leaked in plan")
	}
	if len(p.Steps) != 2 || strings.Join(p.Steps[0].Args, " ") != "compose -f compose.yaml pull" || !strings.Contains(strings.Join(p.Steps[1].Args, " "), "up -d --wait") {
		t.Fatal("unsafe compose plan")
	}
}
func TestWorkerDisconnectIsUnknown(t *testing.T) {
	e, _ := newTestEngine(t)
	u, _ := user.Current()
	e.Config.RunUser = u.Username
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]string{"user": u.Username})
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"stream":"stdout","text":"started"}`)
	}))
	defer srv.Close()
	e.Config.WorkerURL = srv.URL
	_, err := e.workerCommand(context.Background(), model.Step{}, nil, func(string, string) {})
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("%v", err)
	}
}
func TestOnlyOneAgentCanUseStateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.lock")
	unlock, err := lockAgent(path)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := lockAgent(path); err == nil {
		release()
		t.Fatal("duplicate agent acquired lock")
	}
	unlock()
	release, err := lockAgent(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestReadonlyRejectsExecution(t *testing.T) {
	e, f := newTestEngine(t)
	e.Config.ReadOnly = true
	var calls atomic.Int32
	e.Runner = func(context.Context, model.Step, map[string]string, func(string, string)) (int, error) {
		calls.Add(1)
		return 0, nil
	}
	e.Handle(context.Background(), assignment(t))
	if calls.Load() != 0 || f.result.State != "blocked" {
		t.Fatal("readonly agent executed")
	}
}
func TestHealthCheckFailureIsSeparateFromCommands(t *testing.T) {
	e, f := newTestEngine(t)
	a := assignment(t)
	a.Plan.Steps = append(a.Plan.Steps, model.Step{Name: "health", Health: true, Script: "health"})
	e.Runner = func(_ context.Context, s model.Step, _ map[string]string, emit func(string, string)) (int, error) {
		preflightOutput(s, emit)
		if s.Health {
			return 7, errors.New("unhealthy")
		}
		return 0, nil
	}
	e.Handle(context.Background(), a)
	if f.result.Health != "failed" || f.result.State != "failed" || f.result.ExitCode != 7 {
		t.Fatalf("%+v", f.result)
	}
}
func TestHTTPSRequiredOutsideLoopback(t *testing.T) {
	if _, err := NewClient(Config{HubURL: "http://192.168.1.2"}); err == nil {
		t.Fatal("insecure connection accepted")
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) }))
	defer srv.Close()
	c, err := NewClient(Config{HubURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Request(context.Background(), "GET", "/", nil, nil); err == nil {
		t.Fatal("untrusted CA accepted")
	}
	certFile := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600)
	c, err = NewClient(Config{HubURL: srv.URL, CACert: certFile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Request(context.Background(), "GET", "/", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAPUDetectionDoesNotInferFromVRAMReservation(t *testing.T) {
	root := t.TempDir()
	card := filepath.Join(root, "card0")
	nodes := filepath.Join(root, "nodes")
	os.MkdirAll(filepath.Join(card, "device/drm/renderD128"), 0700)
	os.MkdirAll(filepath.Join(nodes, "0"), 0700)
	properties := filepath.Join(nodes, "0/properties")
	os.WriteFile(properties, []byte("cpu_cores_count 8\ndrm_render_minor 128\n"), 0600)
	if !amdAPU(card, nodes) {
		t.Fatal("APU not identified")
	}
	os.WriteFile(properties, []byte("cpu_cores_count 0\ndrm_render_minor 128\n"), 0600)
	if amdAPU(card, nodes) {
		t.Fatal("unverified memory type reported as unified")
	}
}

func TestRealLocalGitDeploymentAndHealthCheck(t *testing.T) {
	if runtime.GOOS == "windows" || isElevated() {
		t.Skip("native ordinary-user Unix deployment; Windows executor needs an interactive session")
	}
	if binary("git") == "" {
		t.Skip("git unavailable")
	}
	e, f := newTestEngine(t)
	root := t.TempDir()
	repo := filepath.Join(root, "source")
	dest := filepath.Join(root, "deployed")
	os.Mkdir(repo, 0700)
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init")
	os.WriteFile(filepath.Join(repo, "app.txt"), []byte("test project"), 0600)
	git("add", "app.txt")
	git("-c", "user.name=HomeFleet Test", "-c", "user.email=test@example.invalid", "-c", "core.hooksPath=/dev/null", "commit", "-m", "test fixture")
	sha := git("rev-parse", "HEAD")
	a := model.Action{Kind: "project", Operation: "deploy", Project: &model.Project{Directory: dest, Repository: repo, Ref: "HEAD", Platforms: map[string]model.PlatformConfig{runtime.GOOS: {Steps: []string{"git rev-parse HEAD"}, HealthCheck: "test -f app.txt"}}}}
	plan, err := e.Preflight(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("preflight changed destination")
	}
	if err = e.Handle(context.Background(), model.Assignment{TargetID: "real-git-test", Mode: "execute", Action: a, Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if f.result.State != "succeeded" || f.result.Commit != sha || f.result.Health != "healthy" {
		t.Fatalf("%+v", f.result)
	}
	// Local edits made after a preview must stop execution before a fetch/checkout.
	os.WriteFile(filepath.Join(dest, "app.txt"), []byte("user edit"), 0600)
	if _, err = e.Preflight(context.Background(), a); err == nil {
		t.Fatal("dirty checkout accepted")
	}
}

func TestCommandTimeoutDoesNotHangOnInheritedPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell process fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := localCommand(ctx, model.Step{Script: "sleep 3 & wait", Identity: "current"}, nil, "", func(string, string) {})
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout hung on a child process pipe")
	}
}
