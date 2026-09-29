package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"os/exec"
	"strings"
	"time"
)

func (e *Engine) Inventory(ctx context.Context, scope string) model.Inventory {
	inv := model.Inventory{At: time.Now().UTC(), Packages: []model.Package{}}
	manager := Manager()
	var b []byte
	var err error
	identity := "system"
	switch manager {
	case "apt":
		b, err = e.capture(ctx, model.Step{Program: "dpkg-query", Args: []string{"-W", "-f=$" + "{binary:Package}\\t$" + "{Version}\\n"}, Identity: identity})
	case "pacman":
		b, err = e.capture(ctx, model.Step{Program: "pacman", Args: []string{"-Q"}, Identity: identity})
	case "brew":
		identity = "user"
		b, err = e.capture(ctx, model.Step{Program: "brew", Args: []string{"list", "--versions"}, Identity: identity})
	case "winget":
		script := "Import-Module Microsoft.WinGet.Client -ErrorAction Stop; ConvertTo-Json -InputObject @(Get-WinGetPackage | Select-Object Id,Name,InstalledVersion,AvailableVersions,IsUpdateAvailable) -Depth 6 -Compress"
		if scope == "user" {
			identity = "user"
		}
		b, err = e.capture(ctx, model.Step{Program: "powershell", Script: script, Identity: identity})
	default:
		err = errors.New("未找到受支持的包管理器")
	}
	if err != nil {
		inv.Error = err.Error()
		return inv
	}
	if manager == "winget" {
		var rows []struct {
			ID        string   `json:"Id"`
			Name      string   `json:"Name"`
			Version   string   `json:"InstalledVersion"`
			Available []string `json:"AvailableVersions"`
			Update    bool     `json:"IsUpdateAvailable"`
		}
		if err = json.Unmarshal(b, &rows); err != nil {
			inv.Error = "WinGet 返回格式无法解析"
			return inv
		}
		for _, r := range rows {
			v := ""
			if r.Update && len(r.Available) > 0 && r.Available[0] != r.Version {
				v = r.Available[0]
			}
			inv.Packages = append(inv.Packages, model.Package{ID: r.ID, Name: r.Name, Version: r.Version, AvailableVersion: v, Manager: manager, Scope: scope})
		}
		return inv
	}
	updates := map[string]string{}
	switch manager {
	case "apt":
		raw, updateErr := e.capture(ctx, model.Step{Program: "apt", Args: []string{"list", "--upgradable"}, Identity: "system"})
		if updateErr != nil {
			inv.Error = "可用更新查询失败: " + updateErr.Error()
		}
		for _, l := range strings.Split(string(raw), "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 && strings.Contains(f[0], "/") {
				updates[strings.Split(f[0], "/")[0]] = f[1]
			}
		}
	case "pacman":
		if binary("checkupdates") != "" {
			raw, updateErr := e.capture(ctx, model.Step{Program: "checkupdates", Identity: "system"})
			var exitErr *exec.ExitError
			if updateErr != nil && !(errors.As(updateErr, &exitErr) && exitErr.ExitCode() == 2) {
				inv.Error = "可用更新查询失败: " + updateErr.Error()
			}
			for _, l := range strings.Split(string(raw), "\n") {
				f := strings.Fields(l)
				if len(f) >= 4 {
					updates[f[0]] = f[3]
				}
			}
		} else {
			inv.Error = "缺少 checkupdates 工具（由 pacman-contrib 包提供）；已安装清单有效，可用更新尚未检查"
		}
	case "brew":
		raw, updateErr := e.capture(ctx, model.Step{Program: "brew", Args: []string{"outdated", "--json=v2"}, Identity: "user", Env: map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1"}})
		if updateErr != nil {
			inv.Error = "可用更新查询失败: " + updateErr.Error()
		}
		var data struct {
			Formulae []struct {
				Name    string `json:"name"`
				Current string `json:"current_version"`
			} `json:"formulae"`
			Casks []struct {
				Name    string `json:"name"`
				Current string `json:"current_version"`
			} `json:"casks"`
		}
		if json.Unmarshal(raw, &data) == nil {
			for _, r := range data.Formulae {
				updates[r.Name] = r.Current
			}
			for _, r := range data.Casks {
				updates[r.Name] = r.Current
			}
		} else if inv.Error == "" {
			inv.Error = "Homebrew 更新清单无法解析"
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		id := f[0]
		inv.Packages = append(inv.Packages, model.Package{ID: id, Name: id, Version: strings.Join(f[1:], ", "), AvailableVersion: updates[strings.Split(id, ":")[0]], Manager: manager, Scope: identity})
	}
	return inv
}
