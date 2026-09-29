package server

import (
	"encoding/json"
	"errors"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"net/http"
	"os"
	"path/filepath"
)

func releasesDir() string {
	if dir := os.Getenv("HOMEFLEET_RELEASES"); dir != "" {
		return dir
	}
	return "dist/releases"
}
func currentAgentRelease() (*model.AgentRelease, error) {
	raw, err := os.ReadFile(filepath.Join(releasesDir(), "agent-release.json"))
	if err != nil {
		return nil, errors.New("尚未发布可更新的 Agent，请先构建发布包")
	}
	var r model.AgentRelease
	if err = json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if err = r.Validate(); err != nil {
		return nil, err
	}
	for _, asset := range r.Artifacts {
		info, err := os.Stat(filepath.Join(releasesDir(), "agents", r.Version, asset.Name))
		if err != nil || !info.Mode().IsRegular() || info.Size() != asset.Size {
			return nil, errors.New("Agent 发布包缺失或不完整")
		}
	}
	return &r, nil
}
func (a *Server) agentRelease(w http.ResponseWriter, r *http.Request) {
	release, err := currentAgentRelease()
	if err != nil {
		write(w, 200, map[string]any{"release": nil, "reason": err.Error()})
		return
	}
	write(w, 200, map[string]any{"release": release})
}
func (a *Server) versionedDownload(w http.ResponseWriter, r *http.Request) {
	version, name := r.PathValue("version"), r.PathValue("name")
	allowed := false
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "windows-amd64"} {
		if name == model.AgentFilename(platform) {
			allowed = true
		}
	}
	if !model.ValidVersion(version) || !allowed {
		fail(w, 404, "发布文件不存在")
		return
	}
	http.ServeFile(w, r, filepath.Join(releasesDir(), "agents", version, name))
}
