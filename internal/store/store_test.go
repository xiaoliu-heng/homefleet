package store

import (
	"encoding/json"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "fleet.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func enrolled(t *testing.T, s *Store) (model.Device, string) {
	t.Helper()
	token, e := s.NewEnrollment()
	if e != nil {
		t.Fatal(e)
	}
	d, key, e := s.Enroll(token, model.Device{Name: "test-host", OS: "linux", Capabilities: map[string]model.Capability{}})
	if e != nil {
		t.Fatal(e)
	}
	return d, key
}
func previewReady(t *testing.T, s *Store, d model.Device) model.Job {
	t.Helper()
	j, e := s.NewJob("preview", model.Action{Kind: "package", Operation: "install", Package: "git"}, []string{d.ID}, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Claim(d.ID)
	if e != nil || a == nil {
		t.Fatalf("claim: %v %v", a, e)
	}
	e = s.Finish(d.ID, a.TargetID, model.Result{State: "succeeded", Plan: &model.Plan{Steps: []model.Step{{Name: "install git", Program: "apt-get", Args: []string{"install", "git"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Job(j.ID)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func TestEnrollmentSingleUseAndRevocation(t *testing.T) {
	s := testStore(t)
	token, _ := s.NewEnrollment()
	d, key, e := s.Enroll(token, model.Device{Name: "host"})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Enroll(token, model.Device{Name: "duplicate"}); e == nil {
		t.Fatal("token reused")
	}
	if id, e := s.AgentID(key); e != nil || id != d.ID {
		t.Fatal("credential rejected")
	}
	if e = s.Revoke(d.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AgentID(key); e == nil {
		t.Fatal("revoked credential accepted")
	}
}

func TestArchInstallRequiresNewCapabilityBeforeTaskDelivery(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	d.AgentVersion = "0.2.1"
	d.Capabilities["packages"] = model.Capability{Available: true, Reason: "pacman"}
	if err := s.Heartbeat(d.ID, d, model.Sample{}); err != nil {
		t.Fatal(err)
	}
	action := model.Action{Kind: "package", Operation: "install", Package: "pacman-contrib"}
	blocked, err := s.NewJob("preview", action, []string{d.ID}, "", nil)
	if err != nil || blocked.Targets[0].State != "blocked" || !strings.Contains(blocked.Targets[0].Reason, "0.2.2") {
		t.Fatalf("legacy agent was not blocked: %+v %v", blocked, err)
	}
	if next, err := s.Claim(d.ID); err != nil || next != nil {
		t.Fatalf("legacy agent received an install task: %+v %v", next, err)
	}
	d.AgentVersion = "0.2.2"
	d.Capabilities["pacman_cached_install"] = model.Capability{Available: true}
	if err := s.Heartbeat(d.ID, d, model.Sample{}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.NewJob("preview", action, []string{d.ID}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	check, err := s.Claim(d.ID)
	if err != nil || check == nil || check.Mode != "preview" {
		t.Fatalf("new agent cannot preview: %+v %v", check, err)
	}
	plan := &model.Plan{Steps: []model.Step{{Program: "pacman", Args: []string{"-S", "--noconfirm", "--needed", "--", "pacman-contrib"}, Identity: "system", PackageTransaction: true}}}
	if err := s.Finish(d.ID, check.TargetID, model.Result{State: "succeeded", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(preview.ID, []string{d.ID}); err != nil {
		t.Fatal(err)
	}
	execution, err := s.Claim(d.ID)
	if err != nil || execution == nil || execution.Mode != "execute" || execution.Plan.Steps[0].Args[0] != "-S" {
		t.Fatalf("approved cached install changed: %+v %v", execution, err)
	}
}
func TestHeartbeatStableIdentityAndServerClock(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	d.Name = "custom name"
	d.Group = "living room"
	s.SaveDevice(d)
	forged := model.Device{Name: "changed hostname", ID: "another-id", OS: "darwin", Addresses: []model.Address{{Interface: "en0", Address: "192.168.1.80/24"}}}
	n := 33.0
	e := s.Heartbeat(d.ID, forged, model.Sample{At: time.Now().Add(100 * time.Hour), CPU: &n})
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Device(d.ID)
	if e != nil || got.ID != d.ID || got.Name != "custom name" || got.Group != "living room" || !got.Online {
		t.Fatalf("%+v %v", got, e)
	}
	if time.Since(got.Latest.At) > time.Second || got.Latest.At.After(time.Now().Add(time.Second)) {
		t.Fatal("trusted agent clock")
	}
	all, _ := s.Devices()
	if len(all) != 1 {
		t.Fatal("IP change created duplicate")
	}
}
func TestPreviewRequiredAndIdempotentSubmit(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	bad, _ := s.NewJob("preview", model.Action{}, []string{d.ID}, "", nil)
	if _, e := s.Execute(bad.ID, []string{d.ID}); e == nil {
		t.Fatal("uninspected execution accepted")
	}
	a, _ := s.Claim(d.ID)
	s.Finish(d.ID, a.TargetID, model.Result{State: "blocked"})
	p := previewReady(t, s, d)
	j, e := s.Execute(p.ID, []string{d.ID})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Execute(p.ID, []string{d.ID})
	if e != nil || again.ID != j.ID {
		t.Fatal("duplicate execution")
	}
	assignment, e := s.Claim(d.ID)
	if e != nil || assignment == nil || assignment.Plan.Steps[0].Program != "apt-get" {
		t.Fatalf("lost approved plan: %v", e)
	}
	if a, _ := s.Claim(d.ID); a != nil {
		t.Fatal("same host claimed twice")
	}
	if e = s.Finish(d.ID, assignment.TargetID, model.Result{State: "succeeded"}); e != nil {
		t.Fatal(e)
	}
	done, _ := s.Job(j.ID)
	if done.Status != "succeeded" || done.Targets[0].Plan == nil {
		t.Fatal("lost audit plan")
	}
}
func TestExpiredPreviewAndOfflineTargetRejected(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	p := previewReady(t, s, d)
	target := p.Targets[0]
	target.Plan.ExpiresAt = time.Now().Add(-time.Minute)
	s.DB.Exec("UPDATE targets SET body=? WHERE id=?", encode(target), target.ID)
	if _, e := s.Execute(p.ID, []string{d.ID}); e == nil {
		t.Fatal("expired preview accepted")
	}
	p = previewReady(t, s, d)
	s.DB.Exec("UPDATE devices SET last_seen=? WHERE id=?", stamp(time.Now().Add(-time.Hour)), d.ID)
	if _, e := s.Execute(p.ID, []string{d.ID}); e == nil {
		t.Fatal("offline execution accepted")
	}
}
func TestGlobalConcurrencyAndRecovery(t *testing.T) {
	s := testStore(t)
	devices := []model.Device{}
	for i := 0; i < 4; i++ {
		d, _ := enrolled(t, s)
		devices = append(devices, d)
		s.NewJob("inspect", model.Action{Kind: "inventory"}, []string{d.ID}, "", nil)
	}
	assignments := []*model.Assignment{}
	for i, d := range devices {
		a, e := s.Claim(d.ID)
		if e != nil {
			t.Fatal(e)
		}
		if i < 3 && a == nil {
			t.Fatal("slot unavailable")
		}
		if i == 3 && a != nil {
			t.Fatal("concurrency limit exceeded")
		}
		if a != nil {
			assignments = append(assignments, a)
		}
	}
	if e := s.Reconcile(devices[0].ID, nil); e != nil {
		t.Fatal(e)
	}
	target, _ := s.Target(assignments[0].TargetID)
	if target.State != "unknown" {
		t.Fatal("unrecorded assignment was not marked unknown")
	}
	a, e := s.Claim(devices[3].ID)
	if e != nil || a == nil {
		t.Fatal("slot did not release")
	}
	// The same attempt can deliver its actual outcome after a disconnection.
	if e = s.Finish(devices[0].ID, target.ID, model.Result{State: "succeeded"}); e != nil {
		t.Fatal(e)
	}
}
func TestUnknownBlocksMutationsButAllowsInspection(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	p := previewReady(t, s, d)
	j, _ := s.Execute(p.ID, []string{d.ID})
	a, _ := s.Claim(d.ID)
	s.Finish(d.ID, a.TargetID, model.Result{State: "unknown", Reason: "lost connection"})
	next, _ := s.NewJob("execute", j.Action, []string{d.ID}, "", map[string]*model.Plan{d.ID: p.Targets[0].Plan})
	if a, _ := s.Claim(d.ID); a != nil {
		t.Fatal("unknown job allowed another mutation")
	}
	inspect, _ := s.NewJob("inspect", model.Action{Kind: "inventory"}, []string{d.ID}, "", nil)
	a, _ = s.Claim(d.ID)
	if a == nil || a.JobID != inspect.ID {
		t.Fatal("inspection blocked")
	}
	s.Finish(d.ID, a.TargetID, model.Result{State: "succeeded"})
	if err := s.Resolve(j.Targets[0].ID, "failed", "核对进程已退出，安装未完成"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Claim(d.ID)
	if a == nil || a.JobID != next.ID {
		t.Fatal("verified target never released")
	}
}
func TestCancellationDoesNotKillRunningTransaction(t *testing.T) {
	s := testStore(t)
	d1, _ := enrolled(t, s)
	d2, _ := enrolled(t, s)
	j, _ := s.NewJob("inspect", model.Action{Kind: "inventory"}, []string{d1.ID, d2.ID}, "", nil)
	a, _ := s.Claim(d1.ID)
	if err := s.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Job(j.ID)
	if got.Targets[0].State != "running" || !got.Targets[0].CancelRequested || got.Targets[1].State != "cancelled" {
		t.Fatalf("%+v", got.Targets)
	}
	s.Finish(d1.ID, a.TargetID, model.Result{State: "succeeded"})
	if other, _ := s.Claim(d2.ID); other != nil {
		t.Fatal("cancelled target dispatched")
	}
}
func TestLogsOrderedDeduplicatedAndScoped(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	other, _ := enrolled(t, s)
	j, _ := s.NewJob("inspect", model.Action{Kind: "inventory"}, []string{d.ID}, "", nil)
	a, _ := s.Claim(d.ID)
	batch := []model.Log{{Seq: 2, Text: "second"}, {Seq: 1, Text: "first"}}
	if err := s.AddLogs(other.ID, a.TargetID, batch); err == nil {
		t.Fatal("cross-device log write")
	}
	s.AddLogs(d.ID, a.TargetID, batch)
	s.AddLogs(d.ID, a.TargetID, batch)
	logs, _ := s.Logs(j.Targets[0].ID, 0)
	if len(logs) != 2 || logs[0].Seq != 1 || logs[1].Seq != 2 {
		t.Fatalf("%+v", logs)
	}
}
func TestSecretsSnapshotEncryptedAndBackupRestorable(t *testing.T) {
	s := testStore(t)
	secret := "private-value-not-in-database"
	p, e := s.SaveProject(model.Project{Name: "app", Directory: "/srv/app", SecretEnv: map[string]string{"TOKEN": secret}})
	if e != nil {
		t.Fatal(e)
	}
	var body, encrypted string
	s.DB.QueryRow("SELECT body,secrets FROM projects WHERE id=?", p.ID).Scan(&body, &encrypted)
	if strings.Contains(body, secret) || strings.Contains(encrypted, secret) {
		t.Fatal("plaintext at rest")
	}
	got, e := s.Project(p.ID, true)
	if e != nil || got.SecretEnv["TOKEN"] != secret {
		t.Fatal("decryption failed")
	}
	public, _ := s.Projects()
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), secret) {
		t.Fatal("public response leaked secret")
	}
	root := t.TempDir()
	out := filepath.Join(root, "backup.db")
	if e = s.Backup(out); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "master.key"), s.Key, 0600)
	restored, e := Open(out)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	got, e = restored.Project(p.ID, true)
	if e != nil || got.SecretEnv["TOKEN"] != secret {
		t.Fatal("backup cannot decrypt")
	}
}
func TestRetentionAndReopen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fleet.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	d, key := enrolled(t, s)
	s.DB.Exec("INSERT INTO metrics VALUES(?,?,?)", d.ID, stamp(time.Now().Add(-8*24*time.Hour)), encode(model.Sample{}))
	if e = s.Cleanup(); e != nil {
		t.Fatal(e)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM metrics").Scan(&n)
	if n != 0 {
		t.Fatal("expired samples retained")
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if id, e := s.AgentID(key); e != nil || id != d.ID {
		t.Fatal("registration lost after restart")
	}
}
func TestMetricsDownsampleRetainsFirstAndLatest(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	start := time.Now().UTC().Add(-time.Hour)
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1500; i++ {
		m := model.Sample{At: start.Add(time.Duration(i) * time.Second)}
		if _, err = tx.Exec("INSERT INTO metrics VALUES(?,?,?)", d.ID, stamp(m.At), encode(m)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	points, err := s.Metrics(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) > 721 || !points[0].At.Equal(start) || !points[len(points)-1].At.Equal(start.Add(1499*time.Second)) {
		t.Fatalf("history lost endpoints: %d", len(points))
	}
}

func TestAgentUpdateKeepsSlotDuringRestartButEventuallyRequiresVerification(t *testing.T) {
	s := testStore(t)
	d, _ := enrolled(t, s)
	d.Capabilities["agent_update"] = model.Capability{Available: true}
	s.SaveDevice(d)
	j, err := s.NewJob("preview", model.Action{Kind: "agent", Operation: "self_update", AgentVersion: "0.3.0"}, []string{d.ID}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	assignment, _ := s.Claim(d.ID)
	s.Finish(d.ID, assignment.TargetID, model.Result{State: "succeeded", Plan: &model.Plan{AgentUpdate: &model.AgentUpdatePlan{Version: "0.3.0"}}})
	execution, err := s.Execute(j.ID, []string{d.ID})
	if err != nil {
		t.Fatal(err)
	}
	assignment, _ = s.Claim(d.ID)
	if assignment == nil {
		t.Fatal("no update claimed")
	}
	// Last-seen is server-owned; age this fixture to simulate service restart.
	s.DB.Exec("UPDATE devices SET last_seen=? WHERE id=?", stamp(time.Now().Add(-time.Minute)), d.ID)
	if err = s.MarkDisconnected(); err != nil {
		t.Fatal(err)
	}
	target, _ := s.Target(execution.Targets[0].ID)
	if target.State != "running" {
		t.Fatal("normal update restart lost its concurrency slot")
	}
	then := time.Now().Add(-16 * time.Minute)
	target.ClaimedAt = &then
	s.DB.Exec("UPDATE targets SET body=? WHERE id=?", encode(target), target.ID)
	s.MarkDisconnected()
	target, _ = s.Target(target.ID)
	if target.State != "unknown" {
		t.Fatal("orphan update did not require verification")
	}
	if err = s.Finish(d.ID, target.ID, model.Result{State: "succeeded", AgentVersion: "0.3.0"}); err != nil {
		t.Fatal(err)
	}
	target, _ = s.Target(target.ID)
	if target.Result.AgentVersion != "0.3.0" || target.State != "succeeded" {
		t.Fatal("late durable update outcome not accepted")
	}
}
