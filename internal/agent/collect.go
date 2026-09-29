package agent

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/xiaoliu-heng/homefleet/internal/model"
)

func binary(name string) string {
	if p, e := exec.LookPath(name); e == nil {
		return p
	}
	extra := []string{"/opt/homebrew/bin/" + name, "/usr/local/bin/" + name, "/usr/bin/" + name, "/bin/" + name}
	if runtime.GOOS == "windows" {
		extra = append(extra, filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", name+".exe"), filepath.Join(os.Getenv("ProgramFiles"), "NVIDIA Corporation", "NVSMI", name+".exe"))
	}
	for _, p := range extra {
		if st, e := os.Stat(p); e == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
func Manager() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew"
	case "windows":
		return "winget"
	default:
		if binary("pacman") != "" {
			return "pacman"
		}
		if binary("apt-get") != "" {
			return "apt"
		}
	}
	return ""
}
func (e *Engine) Device() model.Device {
	name, _ := os.Hostname()
	d := model.Device{ID: e.Config.DeviceID, Name: e.Config.Name, Hostname: name, OS: runtime.GOOS, Arch: runtime.GOARCH, Kind: "agent", AgentVersion: model.Version, RunUser: e.Config.RunUser, Addresses: []model.Address{}, Capabilities: map[string]model.Capability{}}
	if d.Name == "" {
		d.Name = name
	}
	if info, err := host.Info(); err == nil {
		d.Platform = strings.TrimSpace(info.Platform + " " + info.PlatformVersion)
	}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, i := range ifaces {
			if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
				continue
			}
			addresses, _ := i.Addrs()
			for _, addr := range addresses {
				d.Addresses = append(d.Addresses, model.Address{Interface: i.Name, Address: addr.String()})
			}
		}
	}
	d.Capabilities["monitor"] = model.Capability{Available: true}
	d.Capabilities["agent_update"] = e.updateCapability()
	userErr := e.userReady()
	d.Capabilities["user"] = model.Capability{Available: userErr == nil}
	if userErr != nil {
		d.Capabilities["user"] = model.Capability{Reason: userErr.Error()}
	}
	manager := Manager()
	pkg := model.Capability{Available: manager != "" && !e.Config.ReadOnly, Reason: manager}
	if manager == "" {
		pkg.Reason = "未找到受支持的包管理器"
	} else if e.Config.ReadOnly {
		pkg.Reason = "Agent 以只读模式运行"
	} else if manager == "brew" && userErr != nil {
		pkg.Available = false
		pkg.Reason = userErr.Error()
	} else if manager != "brew" && !isElevated() {
		pkg.Available = false
		pkg.Reason = "系统级安装需要管理员 Agent"
	}
	d.Capabilities["packages"] = pkg
	if manager == "pacman" {
		d.Capabilities["pacman_cached_install"] = model.Capability{Available: true}
	}
	d.Capabilities["projects"] = model.Capability{Available: !e.Config.ReadOnly && userErr == nil}
	if e.Config.ReadOnly {
		d.Capabilities["projects"] = model.Capability{Reason: "Agent 以只读模式运行"}
	} else if userErr != nil {
		d.Capabilities["projects"] = model.Capability{Reason: userErr.Error()}
	}
	docker := model.Capability{}
	if userErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		_, err := e.capture(ctx, model.Step{Program: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}, Identity: "user"})
		cancel()
		docker.Available = err == nil
		if err != nil {
			docker.Reason = "用户 Docker 引擎不可用"
		}
	}
	d.Capabilities["docker"] = docker
	return d
}
func Collect(ctx context.Context) model.Sample {
	s := model.Sample{At: time.Now().UTC(), Disks: []model.Disk{}, GPUs: []model.GPU{}, Errors: map[string]string{}}
	if values, e := cpu.PercentWithContext(ctx, time.Second, false); e == nil && len(values) > 0 {
		s.CPU = &values[0]
	} else {
		s.Errors["cpu"] = fmt.Sprint(e)
	}
	if v, e := mem.VirtualMemoryWithContext(ctx); e == nil {
		s.MemoryPercent = &v.UsedPercent
		s.MemoryTotal = v.Total
		s.MemoryAvailable = v.Available
	} else {
		s.Errors["memory"] = e.Error()
	}
	parts, e := disk.PartitionsWithContext(ctx, false)
	if e != nil {
		s.Errors["disks"] = e.Error()
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Mountpoint] || strings.HasPrefix(p.Mountpoint, "/System/Volumes/") || strings.HasPrefix(p.Mountpoint, "/dev/") || strings.HasPrefix(p.Mountpoint, "/snap/") {
			continue
		}
		seen[p.Mountpoint] = true
		if p.Fstype == "tmpfs" || p.Fstype == "devfs" || p.Fstype == "overlay" || p.Fstype == "squashfs" {
			continue
		}
		v, e := disk.UsageWithContext(ctx, p.Mountpoint)
		if e != nil {
			s.Errors["disk:"+p.Mountpoint] = e.Error()
			continue
		}
		if v.Total == 0 {
			continue
		}
		s.Disks = append(s.Disks, model.Disk{Mount: p.Mountpoint, Device: p.Device, Filesystem: p.Fstype, Total: v.Total, Free: v.Free, UsedPercent: v.UsedPercent})
	}
	s.GPUs = CollectGPU(ctx)
	return s
}
func runRead(ctx context.Context, program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, program, args...).Output()
}
func floatp(v string) *float64 {
	f, e := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return nil
	}
	return &f
}
func ParseMacmon(raw string) *float64 {
	for _, line := range strings.Split(raw, "\n") {
		var data map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &data) != nil {
			continue
		}
		for _, key := range []string{"gpu_active_ratio", "gpu_usage", "gpu_scaled_ratio"} {
			value, ok := data[key]
			if !ok || string(value) == "null" {
				continue
			}
			var ratio float64
			if json.Unmarshal(value, &ratio) != nil {
				var pair []float64
				if key != "gpu_usage" || json.Unmarshal(value, &pair) != nil || len(pair) != 2 {
					continue
				}
				ratio = pair[1]
			}
			if ratio < 0 || ratio > 1 {
				continue
			}
			percent := ratio * 100
			return &percent
		}
	}
	return nil
}
func bytesp(v string) *uint64 {
	f := floatp(v)
	if f == nil {
		return nil
	}
	b := uint64(*f * 1024 * 1024)
	return &b
}
func ParseNVIDIA(raw string) []model.GPU {
	rows, e := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if e != nil {
		return []model.GPU{{Vendor: "NVIDIA", Source: "nvidia-smi", Error: "无法解析 NVIDIA 数据"}}
	}
	out := []model.GPU{}
	for _, r := range rows {
		if len(r) != 7 {
			continue
		}
		g := model.GPU{ID: strings.TrimSpace(r[0]), Name: strings.TrimSpace(r[1]), Vendor: "NVIDIA", Source: "nvidia-smi", Utilization: floatp(r[2]), MemoryUsed: bytesp(r[3]), MemoryTotal: bytesp(r[4]), Temperature: floatp(r[5])}
		if g.Utilization == nil {
			g.Error = "驱动未提供利用率"
		}
		out = append(out, g)
	}
	return out
}
func CollectGPU(ctx context.Context) []model.GPU {
	out := []model.GPU{}
	if p := binary("nvidia-smi"); p != "" {
		b, e := runRead(ctx, p, "--query-gpu=uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,driver_version", "--format=csv,noheader,nounits")
		if e != nil {
			out = append(out, model.GPU{Vendor: "NVIDIA", Source: "nvidia-smi", Error: e.Error()})
		} else {
			out = append(out, ParseNVIDIA(string(b))...)
		}
	}
	if runtime.GOOS == "linux" {
		cards, _ := filepath.Glob("/sys/class/drm/card[0-9]*")
		for _, card := range cards {
			if strings.Contains(filepath.Base(card), "-") {
				continue
			}
			vendor, _ := os.ReadFile(filepath.Join(card, "device/vendor"))
			v := strings.TrimSpace(string(vendor))
			if v == "0x10de" && len(out) > 0 {
				continue
			}
			g := model.GPU{ID: filepath.Base(card), Name: filepath.Base(card), Source: "sysfs"}
			switch v {
			case "0x1002":
				g.Vendor = "AMD"
				g.Name = "AMD " + g.ID
			case "0x8086":
				g.Vendor = "Intel"
				g.Error = "Intel 利用率采集器尚未验证"
			case "0x10de":
				g.Vendor = "NVIDIA"
				g.Error = "未找到 nvidia-smi"
			default:
				continue
			}
			if g.Vendor == "AMD" {
				b, e := os.ReadFile(filepath.Join(card, "device/gpu_busy_percent"))
				if e == nil {
					g.Utilization = floatp(string(b))
				} else {
					g.Error = "驱动未暴露 gpu_busy_percent"
				}
				for suffix, ptr := range map[string]**uint64{"mem_info_vram_used": &g.MemoryUsed, "mem_info_vram_total": &g.MemoryTotal} {
					b, e := os.ReadFile(filepath.Join(card, "device", suffix))
					if e == nil {
						v, e := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
						if e == nil {
							vv := v
							*ptr = &vv
						}
					}
				}
				// APU firmware reservations can exceed 1 GiB; size is not proof of discrete VRAM.
				g.UnifiedMemory = amdAPU(card, "/sys/class/kfd/kfd/topology/nodes")
				g.MemoryTotal = nil
				g.MemoryUsed = nil
				if !g.UnifiedMemory {
					g.MemoryNote = "AMD 内存类型尚未确认，暂不把固件预留容量显示为独立显存"
				}
			}
			out = append(out, g)
		}
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		g := model.GPU{ID: "apple-gpu", Name: "Apple Silicon GPU", Vendor: "Apple", Source: "macmon", UnifiedMemory: true}
		if p := binary("macmon"); p != "" {
			c, cancel := context.WithTimeout(ctx, 4*time.Second)
			cmd := exec.CommandContext(c, p, "pipe", "-s", "1")
			b, e := cmd.Output()
			cancel()
			g.Utilization = ParseMacmon(string(b))
			if g.Utilization == nil {
				g.Error = fmt.Sprintf("macmon 未提供利用率: %v", e)
			}
		} else {
			g.Error = "需要安装 macmon 才能读取 Apple GPU 指标"
		}
		out = append(out, g)
	}
	if len(out) == 0 {
		out = append(out, model.GPU{Source: "auto", Error: "未发现可用 GPU 采集器"})
	}
	return out
}

func amdAPU(card, topology string) bool {
	renders, _ := filepath.Glob(filepath.Join(card, "device/drm/renderD*"))
	nodes, _ := filepath.Glob(filepath.Join(topology, "*/properties"))
	for _, node := range nodes {
		raw, err := os.ReadFile(node)
		if err != nil {
			continue
		}
		values := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				values[fields[0]] = fields[1]
			}
		}
		cpuCount, _ := strconv.Atoi(values["cpu_cores_count"])
		if cpuCount <= 0 {
			continue
		}
		for _, render := range renders {
			if filepath.Base(render) == "renderD"+values["drm_render_minor"] {
				return true
			}
		}
	}
	return false
}
