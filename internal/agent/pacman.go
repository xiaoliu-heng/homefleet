package agent

import (
	"context"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"strings"
	"time"
)

// Query only the existing sync database. Never refresh the live package index
// while preparing a standalone install: a failed -Syu can leave newer indexes
// behind even though no package transaction was committed.
func (e *Engine) checkArchInstallState(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var stdout, stderr strings.Builder
	code, err := e.command(ctx, model.Step{Program: "pacman", Args: []string{"-Qu"}, Identity: "system", Env: map[string]string{"LC_ALL": "C"}}, nil, func(stream, line string) {
		if stream == "stdout" {
			stdout.WriteString(line + "\n")
		} else {
			stderr.WriteString(line + "\n")
		}
	})
	out, detail := strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String())
	// pacman -Qu exits 1 when its filter finds no outdated packages. An error
	// message, timeout, or nonempty output must not be mistaken for that case.
	if code == 1 && out == "" && detail == "" && ctx.Err() == nil {
		return nil
	}
	if err != nil || code != 0 || detail != "" {
		if detail == "" {
			if err != nil {
				detail = err.Error()
			} else {
				detail = fmt.Sprintf("退出码 %d", code)
			}
		}
		return fmt.Errorf("无法核对 Arch 软件源与已安装版本，未安排安装: %s", detail)
	}
	if out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	count := len(lines)
	if len(lines) > 3 {
		lines = lines[:3]
	}
	return fmt.Errorf("Arch 本机软件源索引与已安装版本不同步（%d 项待更新，例如 %s）。可能刚刷新软件源但升级未完成；请先处理升级冲突并完成完整系统升级，再重新预览安装。本次未刷新软件源，也未安装或升级软件", count, strings.Join(lines, "；"))
}
