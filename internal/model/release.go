package model

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MinimumUpdaterVersion = "0.2.0"

type AgentArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type AgentRelease struct {
	Version        string                   `json:"version"`
	MinimumVersion string                   `json:"minimum_version"`
	PublishedAt    time.Time                `json:"published_at"`
	Artifacts      map[string]AgentArtifact `json:"artifacts"`
}
type AgentUpdatePlan struct {
	FromVersion string        `json:"from_version"`
	Version     string        `json:"version"`
	Artifact    AgentArtifact `json:"artifact"`
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidVersion(v string) bool { return versionPattern.MatchString(v) }
func CompareVersions(a, b string) (int, error) {
	if !ValidVersion(a) || !ValidVersion(b) {
		return 0, errors.New("版本必须为 X.Y.Z")
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range aa {
		x, _ := strconv.Atoi(aa[i])
		y, _ := strconv.Atoi(bb[i])
		if x < y {
			return -1, nil
		}
		if x > y {
			return 1, nil
		}
	}
	return 0, nil
}
func AgentFilename(platform string) string {
	switch platform {
	case "linux-amd64", "linux-arm64", "darwin-arm64":
		return "homefleet-agent-" + platform
	case "windows-amd64":
		return "homefleet-agent-windows-amd64.exe"
	default:
		return ""
	}
}
func (r AgentRelease) Validate() error {
	if !ValidVersion(r.Version) || !ValidVersion(r.MinimumVersion) || len(r.Artifacts) == 0 {
		return errors.New("无效的 Agent 发布版本")
	}
	if cmp, _ := CompareVersions(r.MinimumVersion, r.Version); cmp > 0 {
		return errors.New("最低更新器版本不能高于发布版本")
	}
	for platform, asset := range r.Artifacts {
		if AgentFilename(platform) == "" || asset.Name != AgentFilename(platform) || !shaPattern.MatchString(asset.SHA256) || asset.Size <= 0 || asset.Size > 128<<20 {
			return fmt.Errorf("无效的 Agent 发布文件: %s", platform)
		}
	}
	return nil
}
func (r AgentRelease) DownloadPath(asset AgentArtifact) string {
	return "/downloads/agents/" + r.Version + "/" + asset.Name
}
