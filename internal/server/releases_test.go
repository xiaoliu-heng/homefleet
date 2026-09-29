package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publishTestRelease(t *testing.T, dir, version string) model.AgentRelease {
	t.Helper()
	name := model.AgentFilename("linux-amd64")
	raw := []byte("test release " + version)
	path := filepath.Join(dir, "agents", version, name)
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, raw, 0755)
	release := model.AgentRelease{Version: version, MinimumVersion: "0.2.0", PublishedAt: time.Now(), Artifacts: map[string]model.AgentArtifact{"linux-amd64": {Name: name, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Size: int64(len(raw))}}}
	body, _ := json.Marshal(release)
	os.WriteFile(filepath.Join(dir, "agent-release.json"), body, 0644)
	return release
}
func TestAgentReleaseIsPinnedAndBrowserCannotChooseArtifact(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMEFLEET_RELEASES", dir)
	release := publishTestRelease(t, dir, "0.3.0")
	a, s := fixture(t)
	cookie := login(t, a)
	if r := request(a, "GET", "/api/v1/agent-release", nil, nil, ""); r.Code != 401 {
		t.Fatal("release API missing admin authentication")
	}
	token, _ := s.NewEnrollment()
	d, _, _ := s.Enroll(token, model.Device{Name: "updatable", AgentVersion: "0.2.0", Capabilities: map[string]model.Capability{"agent_update": {Available: true}}})
	forged := release
	forged.Artifacts = map[string]model.AgentArtifact{"linux-amd64": {Name: "evil", SHA256: strings.Repeat("0", 64), Size: 123}}
	action := model.Action{Kind: "agent", Operation: "self_update", AgentVersion: "0.3.0", AgentRelease: &forged}
	res := request(a, "POST", "/api/v1/jobs/preview", map[string]any{"device_ids": []string{d.ID}, "action": action}, cookie, "")
	if res.Code != 201 {
		t.Fatal(res.Body.String())
	}
	assignment, err := s.Claim(d.ID)
	if err != nil || assignment == nil {
		t.Fatalf("%v %v", assignment, err)
	}
	if assignment.Action.AgentRelease.Artifacts["linux-amd64"] != release.Artifacts["linux-amd64"] {
		t.Fatal("trusted browser supplied artifact")
	}
	// A later publication must not rewrite an already-approved task.
	publishTestRelease(t, dir, "0.4.0")
	plan := &model.Plan{AgentUpdate: &model.AgentUpdatePlan{FromVersion: "0.2.0", Version: "0.3.0", Artifact: release.Artifacts["linux-amd64"]}}
	if err = s.Finish(d.ID, assignment.TargetID, model.Result{State: "succeeded", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	job, err := s.Execute(assignment.JobID, []string{d.ID})
	if err != nil {
		t.Fatal(err)
	}
	if job.Action.AgentRelease.Version != "0.3.0" || job.Targets[0].Plan.AgentUpdate.Version != "0.3.0" {
		t.Fatal("approved version changed")
	}
	res = request(a, "POST", "/api/v1/jobs/preview", map[string]any{"device_ids": []string{d.ID}, "action": action}, cookie, "")
	if res.Code != 400 {
		t.Fatal("stale latest-version request accepted")
	}
	if res = request(a, "GET", "/downloads/agents/0.3.0/"+model.AgentFilename("linux-amd64"), nil, nil, ""); res.Code != 200 || res.Body.String() != "test release 0.3.0" {
		t.Fatal("old pinned artifact unavailable")
	}
	if res = request(a, "GET", "/downloads/agents/0.3.0/agent.json", nil, nil, ""); res.Code != 404 {
		t.Fatal("non-artifact downloaded")
	}
}
func TestOldAgentBlockedBeforeUnsupportedTaskDelivery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMEFLEET_RELEASES", dir)
	publishTestRelease(t, dir, "0.3.0")
	a, s := fixture(t)
	cookie := login(t, a)
	token, _ := s.NewEnrollment()
	d, _, _ := s.Enroll(token, model.Device{Name: "old agent", AgentVersion: "0.1.0"})
	res := request(a, "POST", "/api/v1/jobs/preview", map[string]any{"device_ids": []string{d.ID}, "action": model.Action{Kind: "agent", Operation: "self_update", AgentVersion: "0.3.0"}}, cookie, "")
	var job model.Job
	json.Unmarshal(res.Body.Bytes(), &job)
	if res.Code != 201 || len(job.Targets) != 1 || job.Targets[0].State != "blocked" || !strings.Contains(job.Targets[0].Reason, "安装") {
		t.Fatal(res.Body.String())
	}
	if next, err := s.Claim(d.ID); err != nil || next != nil {
		t.Fatal("old agent was given an unsupported task")
	}
}
