//go:build !windows

package main

import "github.com/xiaoliu-heng/homefleet/internal/agent"

func runService(e *agent.Engine) (bool, error) { return false, nil }
