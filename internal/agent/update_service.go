package agent

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func managedPaths() (string, string, string) {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("ProgramFiles"), "HomeFleet", "homefleet-agent.exe"), filepath.Join(os.Getenv("ProgramData"), "HomeFleet", "agent.json"), "HomeFleetAgent"
	case "darwin":
		return "/usr/local/libexec/homefleet/homefleet-agent", "/Library/Application Support/HomeFleet/agent.json", "com.homefleet.agent"
	default:
		return "/usr/local/libexec/homefleet/homefleet-agent", "/etc/homefleet/agent.json", "homefleet-agent.service"
	}
}
func updateTools() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"powershell"}
	case "darwin":
		return []string{"launchctl"}
	default:
		return []string{"systemctl", "systemd-run"}
	}
}
func serviceCommand(ctx context.Context, program string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", program, err, strings.TrimSpace(string(out)))
	}
	return nil
}
func psQuote(s string) string      { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func xmlText(s string) string      { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func updaterName(id string) string { return "homefleet-updater-" + id }
func updaterPlist(c Config, id string) string {
	return filepath.Join(c.DataDir, "updates", id, "updater.plist")
}
func dispatchUpdater(c Config, u updateState) error {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	args := []string{"apply-update", "--config", c.ConfigPath, "--update-id", u.TargetID}
	switch runtime.GOOS {
	case "windows":
		// A SYSTEM scheduled task survives service shutdown and user logoff.
		argument := "apply-update --config \"" + c.ConfigPath + "\" --update-id " + u.TargetID
		script := "$ErrorActionPreference='Stop'; $a=New-ScheduledTaskAction -Execute " + psQuote(u.Helper) + " -Argument " + psQuote(argument) + "; $p=New-ScheduledTaskPrincipal -UserId SYSTEM -LogonType ServiceAccount -RunLevel Highest; $s=New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Minutes 10); Register-ScheduledTask -TaskName " + psQuote(updaterName(u.TargetID)) + " -Action $a -Principal $p -Settings $s -Force | Out-Null; Start-ScheduledTask -TaskName " + psQuote(updaterName(u.TargetID))
		return serviceCommand(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	case "darwin":
		plist := "<?xml version=\"1.0\"?><!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\"><plist version=\"1.0\"><dict><key>Label</key><string>com.homefleet.updater." + u.TargetID + "</string><key>ProgramArguments</key><array>"
		for _, arg := range append([]string{u.Helper}, args...) {
			plist += "<string>" + xmlText(arg) + "</string>"
		}
		plist += "</array><key>RunAtLoad</key><true/><key>StandardOutPath</key><string>" + xmlText(filepath.Join(c.DataDir, "updates", u.TargetID, "helper.log")) + "</string><key>StandardErrorPath</key><string>" + xmlText(filepath.Join(c.DataDir, "updates", u.TargetID, "helper.log")) + "</string></dict></plist>"
		path := updaterPlist(c, u.TargetID)
		if err := os.WriteFile(path, []byte(plist), 0600); err != nil {
			return err
		}
		return serviceCommand(ctx, "launchctl", "bootstrap", "system", path)
	default:
		return serviceCommand(ctx, "systemd-run", append([]string{"--unit", updaterName(u.TargetID), "--collect", "--no-block", "--property=Type=exec", "--property=RuntimeMaxSec=10min", u.Helper}, args...)...)
	}
}
func cleanupUpdater(c Config, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "windows":
		serviceCommand(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", "Unregister-ScheduledTask -TaskName "+psQuote(updaterName(id))+" -Confirm:$false -ErrorAction SilentlyContinue")
	case "darwin":
		os.Remove(updaterPlist(c, id))
		serviceCommand(ctx, "launchctl", "bootout", "system/com.homefleet.updater."+id)
	}
}

type managedService struct{}

func (managedService) Stop(ctx context.Context) error {
	switch runtime.GOOS {
	case "windows":
		return serviceCommand(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; $s=Get-Service HomeFleetAgent; if($s.Status -ne 'Stopped'){Stop-Service HomeFleetAgent; $s.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))}; $t=Get-ScheduledTask HomeFleetUserWorker -ErrorAction SilentlyContinue; if($t){Stop-ScheduledTask HomeFleetUserWorker; $until=(Get-Date).AddSeconds(20); while((Get-ScheduledTask HomeFleetUserWorker).State -eq 'Running'){if((Get-Date) -gt $until){throw 'User worker did not stop'}; Start-Sleep -Milliseconds 200}}`)
	case "darwin":
		// bootout may return before the old job has completely left the domain.
		absent, err := launchdAgentAbsent(ctx)
		if err != nil {
			return err
		}
		if absent {
			return nil
		}
		if err := serviceCommand(ctx, "launchctl", "bootout", "system/com.homefleet.agent"); err != nil {
			return err
		}
		until := time.Now().Add(30 * time.Second)
		for time.Now().Before(until) {
			absent, err := launchdAgentAbsent(ctx)
			if err != nil {
				return err
			}
			if absent {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		return errors.New("launchd Agent 停止超时")
	default:
		return serviceCommand(ctx, "systemctl", "stop", "homefleet-agent.service")
	}
}

func launchdAgentAbsent(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "launchctl", "print", "system/com.homefleet.agent").CombinedOutput()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err == nil {
		return false, nil
	}
	if strings.Contains(string(out), "Could not find service") {
		return true, nil
	}
	return false, fmt.Errorf("无法确认 launchd Agent 状态: %w: %s", err, strings.TrimSpace(string(out)))
}
func (managedService) Start(ctx context.Context) error {
	switch runtime.GOOS {
	case "windows":
		return serviceCommand(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; Start-Service HomeFleetAgent; (Get-Service HomeFleetAgent).WaitForStatus('Running',[TimeSpan]::FromSeconds(30)); try { Start-ScheduledTask HomeFleetUserWorker -ErrorAction Stop } catch { }; exit 0`)
	case "darwin":
		return serviceCommand(ctx, "launchctl", "bootstrap", "system", "/Library/LaunchDaemons/com.homefleet.agent.plist")
	default:
		return serviceCommand(ctx, "systemctl", "start", "homefleet-agent.service")
	}
}
