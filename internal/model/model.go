package model

import "time"

// Override at build time with -X github.com/xiaoliu-heng/homefleet/internal/model.Version=X.Y.Z.
var Version = "0.2.3"

type Capability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type Address struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
}
type Device struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Group         string                `json:"group"`
	Kind          string                `json:"kind"`
	OS            string                `json:"os"`
	Platform      string                `json:"platform"`
	Arch          string                `json:"arch"`
	Hostname      string                `json:"hostname"`
	AgentVersion  string                `json:"agent_version"`
	RunUser       string                `json:"run_user"`
	Addresses     []Address             `json:"addresses"`
	Capabilities  map[string]Capability `json:"capabilities"`
	LastSeen      time.Time             `json:"last_seen"`
	CreatedAt     time.Time             `json:"created_at"`
	Revoked       bool                  `json:"revoked"`
	Online        bool                  `json:"online"`
	ManagementURL string                `json:"management_url,omitempty"`
	Probe         *Probe                `json:"probe,omitempty"`
	Latest        *Sample               `json:"latest,omitempty"`
}
type Probe struct {
	Type      string    `json:"type"`
	Target    string    `json:"target"`
	LastError string    `json:"last_error,omitempty"`
	LatencyMS float64   `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
}
type Disk struct {
	Mount       string  `json:"mount"`
	Device      string  `json:"device"`
	Filesystem  string  `json:"filesystem"`
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}
type GPU struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Vendor        string   `json:"vendor"`
	Source        string   `json:"source"`
	Utilization   *float64 `json:"utilization"`
	MemoryUsed    *uint64  `json:"memory_used"`
	MemoryTotal   *uint64  `json:"memory_total"`
	Temperature   *float64 `json:"temperature"`
	UnifiedMemory bool     `json:"unified_memory"`
	MemoryNote    string   `json:"memory_note,omitempty"`
	Error         string   `json:"error,omitempty"`
}
type Sample struct {
	At              time.Time         `json:"at"`
	CPU             *float64          `json:"cpu"`
	MemoryPercent   *float64          `json:"memory_percent"`
	MemoryTotal     uint64            `json:"memory_total"`
	MemoryAvailable uint64            `json:"memory_available"`
	Disks           []Disk            `json:"disks"`
	GPUs            []GPU             `json:"gpus"`
	Errors          map[string]string `json:"errors"`
}
type Package struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	AvailableVersion string `json:"available_version,omitempty"`
	Manager          string `json:"manager"`
	Scope            string `json:"scope,omitempty"`
}
type Inventory struct {
	Packages []Package `json:"packages"`
	At       time.Time `json:"at"`
	Error    string    `json:"error,omitempty"`
}
type CatalogEntry struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Packages    map[string]string `json:"packages"`
}

var Catalog = []CatalogEntry{
	{"git", "Git", "版本控制与项目部署", map[string]string{"apt": "git", "pacman": "git", "brew": "git", "winget": "Git.Git"}},
	{"node", "Node.js", "JavaScript 运行时", map[string]string{"apt": "nodejs", "pacman": "nodejs", "brew": "node", "winget": "OpenJS.NodeJS.LTS"}},
	{"python", "Python", "Python 开发环境（版本由软件源决定）", map[string]string{"apt": "python3", "pacman": "python", "brew": "python", "winget": "Python.Python.3.13"}},
	{"curl", "curl", "HTTP 与文件传输工具", map[string]string{"apt": "curl", "pacman": "curl", "brew": "curl", "winget": "cURL.cURL"}},
}

type PlatformConfig struct {
	Steps       []string `json:"steps"`
	HealthCheck string   `json:"health_check"`
}
type Project struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	Version          int                       `json:"version"`
	Repository       string                    `json:"repository"`
	Ref              string                    `json:"ref"`
	Directory        string                    `json:"directory"`
	Directories      map[string]string         `json:"directories,omitempty"`
	Platforms        map[string]PlatformConfig `json:"platforms"`
	Env              map[string]string         `json:"env"`
	SecretEnv        map[string]string         `json:"secret_env,omitempty"`
	SecretKeys       []string                  `json:"secret_keys,omitempty"`
	EncryptedSecrets string                    `json:"-"`
	ComposeFile      string                    `json:"compose_file,omitempty"`
	UpdatedAt        time.Time                 `json:"updated_at"`
}

func (p Project) Dir(os string) string {
	if d := p.Directories[os]; d != "" {
		return d
	}
	return p.Directory
}

type Action struct {
	AgentVersion   string            `json:"agent_version,omitempty"`
	AgentRelease   *AgentRelease     `json:"agent_release,omitempty"`
	SecretEnvelope string            `json:"secret_envelope,omitempty"`
	Kind           string            `json:"kind"`
	Operation      string            `json:"operation"`
	Package        string            `json:"package,omitempty"`
	CatalogID      string            `json:"catalog_id,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	ProjectID      string            `json:"project_id,omitempty"`
	Project        *Project          `json:"project,omitempty"`
	Secrets        map[string]string `json:"secrets,omitempty"`
}
type Step struct {
	Name               string            `json:"name"`
	Program            string            `json:"program"`
	Args               []string          `json:"args"`
	Script             string            `json:"script,omitempty"`
	Directory          string            `json:"directory,omitempty"`
	Identity           string            `json:"identity"`
	Env                map[string]string `json:"env,omitempty"`
	Health             bool              `json:"health"`
	PackageTransaction bool              `json:"package_transaction"`
}
type Plan struct {
	AgentUpdate    *AgentUpdatePlan `json:"agent_update,omitempty"`
	Steps          []Step           `json:"steps"`
	Warnings       []string         `json:"warnings"`
	ResolvedCommit string           `json:"resolved_commit,omitempty"`
	Package        string           `json:"package,omitempty"`
	ExpiresAt      time.Time        `json:"expires_at"`
}
type Job struct {
	ID        string    `json:"id"`
	Mode      string    `json:"mode"`
	Action    Action    `json:"action"`
	CreatedAt time.Time `json:"created_at"`
	SourceID  string    `json:"source_id,omitempty"`
	Status    string    `json:"status"`
	Targets   []Target  `json:"targets"`
}
type Target struct {
	ID              string     `json:"id"`
	JobID           string     `json:"job_id"`
	DeviceID        string     `json:"device_id"`
	DeviceName      string     `json:"device_name"`
	State           string     `json:"state"`
	Reason          string     `json:"reason,omitempty"`
	Plan            *Plan      `json:"plan,omitempty"`
	Result          *Result    `json:"result,omitempty"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	CancelRequested bool       `json:"cancel_requested"`
}
type Result struct {
	AgentVersion string     `json:"agent_version,omitempty"`
	State        string     `json:"state"`
	Reason       string     `json:"reason,omitempty"`
	Plan         *Plan      `json:"plan,omitempty"`
	Inventory    *Inventory `json:"inventory,omitempty"`
	ExitCode     int        `json:"exit_code"`
	Commit       string     `json:"commit,omitempty"`
	Health       string     `json:"health,omitempty"`
	FinishedAt   time.Time  `json:"finished_at"`
}
type Assignment struct {
	JobID    string `json:"job_id"`
	TargetID string `json:"target_id"`
	Mode     string `json:"mode"`
	Action   Action `json:"action"`
	Plan     *Plan  `json:"plan,omitempty"`
}
type Log struct {
	Seq    int       `json:"seq"`
	At     time.Time `json:"at"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
}

func Terminal(s string) bool {
	return s == "succeeded" || s == "failed" || s == "blocked" || s == "cancelled"
}
