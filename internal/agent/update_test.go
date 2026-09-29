package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func updateFixture(t *testing.T) (*Engine, updateState) {
	t.Helper()
	e, _ := newTestEngine(t)
	dir := t.TempDir()
	u := updateState{Schema: 1, TargetID: strings.Repeat("a", 32), FromVersion: model.Version, Version: "0.3.0", Executable: filepath.Join(dir, "agent"), Candidate: filepath.Join(dir, "candidate"), Backup: filepath.Join(dir, "backup"), Phase: "prepared", Deadline: time.Now().Add(10 * time.Minute)}
	for path, content := range map[string]string{u.Executable: "old binary", u.Backup: "old binary", u.Candidate: "new binary"} {
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	u.SHA256, _ = hashFile(u.Candidate)
	u.OldSHA256, _ = hashFile(u.Executable)
	if err := SaveJSON(updateStatePath(e.Config), u); err != nil {
		t.Fatal(err)
	}
	if err := e.save(&Journal{Assignment: model.Assignment{TargetID: u.TargetID, Mode: "execute", Action: model.Action{Kind: "agent", Operation: "self_update"}}, State: "updating"}); err != nil {
		t.Fatal(err)
	}
	return e, u
}

type testUpdateService struct {
	stop, start   func() error
	stops, starts int
}

func (s *testUpdateService) Stop(context.Context) error {
	s.stops++
	if s.stop != nil {
		return s.stop()
	}
	return nil
}
func (s *testUpdateService) Start(context.Context) error {
	s.starts++
	if s.start != nil {
		return s.start()
	}
	return nil
}
func TestUpdateTransactionAndRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "checksum", "start_failure", "timeout", "stop_failure", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			e, u := updateFixture(t)
			service := &testUpdateService{}
			wantState := "failed"
			wantFile := "old binary"
			service.start = func() error {
				if scenario == "start_failure" && service.starts == 1 {
					return errors.New("injected start failure")
				}
				if scenario == "success" {
					return SaveJSON(updateReadyPath(e.Config), updateReady{TargetID: u.TargetID, Version: u.Version, At: time.Now()})
				}
				return nil
			}
			switch scenario {
			case "success":
				wantState = "succeeded"
				wantFile = "new binary"
			case "checksum":
				os.WriteFile(u.Candidate, []byte("tampered"), 0755)
			case "stop_failure":
				wantState = "unknown"
				service.stop = func() error { return errors.New("injected stop failure") }
			case "cancelled":
				wantState = "cancelled" // Use the real control endpoint's response.
				old := e.Client.HTTP.Transport
				e.Client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/control") {
						w := httptest.NewRecorder()
						w.WriteString(`{"cancel":true}`)
						return w.Result(), nil
					}
					return old.RoundTrip(r)
				})
			}
			if err := e.applyUpdate(context.Background(), u, service, 20*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			j, err := e.load(u.TargetID)
			if err != nil || j.Result == nil || j.Result.State != wantState {
				t.Fatalf("result=%+v err=%v", j, err)
			}
			raw, _ := os.ReadFile(u.Executable)
			if string(raw) != wantFile {
				t.Fatalf("got executable %q", raw)
			}
			if (scenario == "checksum" || scenario == "cancelled") && service.stops != 0 {
				t.Fatal("stopped service without permission/checksum")
			}
			done, _ := readUpdate(e.Config)
			before := service.starts
			if err := e.applyUpdate(context.Background(), done, service, time.Millisecond); err == nil || service.starts != before {
				t.Fatal("duplicate update executed")
			}
			e.Runner = func(context.Context, model.Step, map[string]string, func(string, string)) (int, error) {
				t.Fatal("journal replay ran a command")
				return 0, nil
			}
			if err := e.Handle(context.Background(), j.Assignment); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadUsesConfiguredHTTPSAndRejectsCorruption(t *testing.T) {
	content := []byte("verified release")
	var requestPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		if r.Header.Get("Authorization") != "" {
			t.Error("credential sent to download")
		}
		w.Write(content)
	}))
	defer srv.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600)
	e, err := NewEngine(Config{HubURL: srv.URL, CACert: ca, Token: "private credential"})
	if err != nil {
		t.Fatal(err)
	}
	asset := model.AgentArtifact{Name: model.AgentFilename(runtime.GOOS + "-" + runtime.GOARCH), SHA256: fmt.Sprintf("%x", sha256.Sum256(content)), Size: int64(len(content))}
	release := model.AgentRelease{Version: "0.3.0", MinimumVersion: "0.2.0", Artifacts: map[string]model.AgentArtifact{runtime.GOOS + "-" + runtime.GOARCH: asset}}
	path := filepath.Join(dir, "candidate")
	if err = e.downloadAgent(context.Background(), release, asset, path); err != nil {
		t.Fatal(err)
	}
	if requestPath != "/downloads/agents/0.3.0/"+asset.Name {
		t.Fatal(requestPath)
	}
	os.Remove(path)
	for _, bad := range []string{"short", "verified releasE", "verified releaseX"} {
		content = []byte(bad)
		if err = e.downloadAgent(context.Background(), release, asset, path); err == nil {
			t.Fatal("corrupt download accepted")
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("corrupt candidate left behind")
		}
	}
}
func TestInterruptedUpdaterBecomesUnknownAndNeverRunsAgain(t *testing.T) {
	e, u := updateFixture(t)
	u.Phase = "replacing"
	u.Deadline = time.Now().Add(-time.Second)
	SaveJSON(updateStatePath(e.Config), u)
	if err := e.settleUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, _ := e.load(u.TargetID)
	if j.Result.State != "unknown" {
		t.Fatal(j.Result)
	}
	raw, _ := os.ReadFile(u.Executable)
	if string(raw) != "old binary" {
		t.Fatal("recovery mutated executable")
	}
	if cap := e.updateCapability(); cap.Available {
		t.Fatal("interrupted update advertised available")
	}
}
func TestReadOnlyAgentCannotSelfUpdate(t *testing.T) {
	e, _ := newTestEngine(t)
	e.Config.ReadOnly = true
	if _, err := e.Preflight(context.Background(), model.Action{Kind: "agent", Operation: "self_update"}); err == nil {
		t.Fatal("read-only updater accepted")
	}
}

// Starts the actual newly-built Agent after an actual atomic file replacement.
// OS service-manager behavior is separately left for hardware acceptance.
func TestNewAgentProcessMustHeartbeatBeforeUpdateSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("build/process integration")
	}
	if runtime.GOOS == "windows" {
		t.Skip("native Unix process fixture")
	}
	e, u := updateFixture(t)
	u.Version = "0.3.0"
	if err := os.Remove(u.Candidate); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-ldflags=-X github.com/xiaoliu-heng/homefleet/internal/model.Version="+u.Version, "-o", u.Candidate, "./cmd/agent")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build candidate: %v %s", err, out)
	}
	u.SHA256, _ = hashFile(u.Candidate)
	var heartbeat, reported atomic.Bool
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-update-identity" {
			http.Error(w, "wrong identity", 401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			var body struct {
				Device model.Device `json:"device"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Device.AgentVersion != u.Version || body.Device.ID != "stable-device" {
				http.Error(w, "wrong version or identity", 400)
				return
			}
			heartbeat.Store(true)
			w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/control"):
			w.Write([]byte(`{"cancel":false,"state":"running"}`))
		case strings.HasSuffix(r.URL.Path, "/result"):
			var result model.Result
			json.NewDecoder(r.Body).Decode(&result)
			if result.State == "succeeded" && result.AgentVersion == u.Version && heartbeat.Load() {
				reported.Store(true)
			}
			w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/claim"):
			time.Sleep(20 * time.Millisecond)
			w.WriteHeader(204)
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer hub.Close()
	e.Config.HubURL = hub.URL
	e.Config.Token = "test-update-identity"
	e.Config.DeviceID = "stable-device"
	e.Config.ConfigPath = filepath.Join(t.TempDir(), "agent.json")
	if err = SaveJSON(e.Config.ConfigPath, e.Config); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(e.Config.ConfigPath)
	e.Client, _ = NewClient(e.Config)
	if err = verifyCandidate(u.Candidate, u.Version, e.Config.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if err = verifyCandidate(u.Candidate, "9.9.9", e.Config.ConfigPath); err == nil {
		t.Fatal("wrong embedded version accepted")
	}
	SaveJSON(updateStatePath(e.Config), u)
	var child *exec.Cmd
	var done chan error
	stop := func() error {
		if child == nil {
			return nil
		}
		child.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			child.Process.Kill()
			<-done
		}
		child = nil
		return nil
	}
	defer stop()
	service := &testUpdateService{stop: stop, start: func() error {
		child = exec.Command(u.Executable, "run", "--config", e.Config.ConfigPath)
		done = make(chan error, 1)
		if err := child.Start(); err != nil {
			return err
		}
		go func(cmd *exec.Cmd) { done <- cmd.Wait() }(child)
		return nil
	}}
	if err = e.applyUpdate(context.Background(), u, service, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(5 * time.Second)
	for !reported.Load() && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	if !reported.Load() {
		t.Fatal("new Agent did not heartbeat and replay durable result")
	}
	after, _ := os.ReadFile(e.Config.ConfigPath)
	if string(original) != string(after) {
		t.Fatal("update changed identity/configuration")
	}
}
