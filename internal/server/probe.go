package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func ValidateProbe(p model.Probe) error {
	if p.Target == "" || strings.ContainsAny(p.Target, "\r\n\x00") {
		return errors.New("请填写有效的探测目标")
	}
	switch p.Type {
	case "http":
		if !safeURL(p.Target) {
			return errors.New("HTTP 探测需要完整 http(s) 地址")
		}
	case "tcp":
		h, port, e := net.SplitHostPort(p.Target)
		if e != nil || h == "" || port == "" {
			return errors.New("TCP 目标格式为 主机:端口")
		}
	case "icmp":
		if net.ParseIP(p.Target) == nil {
			return errors.New("ICMP 首版需要明确的 IPv4 地址")
		}
		if net.ParseIP(p.Target).To4() == nil {
			return errors.New("ICMP 首版仅支持 IPv4，可用 TCP/HTTP 探测 IPv6")
		}
	default:
		return errors.New("探测类型须为 icmp、tcp 或 http")
	}
	return nil
}
func Probe(ctx context.Context, p model.Probe) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch p.Type {
	case "http":
		client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(r *http.Request, v []*http.Request) error { return http.ErrUseLastResponse }}
		req, e := http.NewRequestWithContext(ctx, "HEAD", p.Target, nil)
		if e != nil {
			return e
		}
		r, e := client.Do(req)
		if e != nil {
			return e
		}
		r.Body.Close()
		return nil
	case "tcp":
		c, e := (&net.Dialer{}).DialContext(ctx, "tcp", p.Target)
		if e == nil {
			c.Close()
		}
		return e
	case "icmp":
		c, e := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if e != nil {
			return fmt.Errorf("ICMP 需要 NET_RAW 权限: %w", e)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: 17761, Seq: 1, Data: []byte("homefleet")}}
		raw, e := msg.Marshal(nil)
		if e != nil {
			return e
		}
		if _, e = c.WriteTo(raw, &net.IPAddr{IP: net.ParseIP(p.Target)}); e != nil {
			return e
		}
		buf := make([]byte, 1500)
		for {
			n, peer, e := c.ReadFrom(buf)
			if e != nil {
				return e
			}
			if strings.Split(peer.String(), "%")[0] != p.Target {
				continue
			}
			m, e := icmp.ParseMessage(1, buf[:n])
			if e == nil && m.Type == ipv4.ICMPTypeEchoReply {
				if echo, ok := m.Body.(*icmp.Echo); ok && echo.ID == 17761 && string(echo.Data) == "homefleet" {
					return nil
				}
			}
		}
	}
	return errors.New("unsupported probe")
}
func (a *Server) probeAll(ctx context.Context) {
	devices, e := a.Store.Devices()
	if e != nil {
		return
	}
	for _, d := range devices {
		if d.Kind != "appliance" || d.Probe == nil {
			continue
		}
		start := time.Now()
		err := Probe(ctx, *d.Probe)
		d.Probe.CheckedAt = time.Now().UTC()
		d.Probe.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
		if err == nil {
			d.LastSeen = d.Probe.CheckedAt
			d.Probe.LastError = ""
		} else {
			d.Probe.LastError = err.Error()
		}
		target := d.Probe.Target
		if d.Probe.Type == "http" {
			u, _ := url.Parse(target)
			target = u.Hostname()
		} else if d.Probe.Type == "tcp" {
			target, _, _ = net.SplitHostPort(target)
		}
		d.Addresses = []model.Address{{Interface: "管理地址", Address: target}}
		a.Store.UpdateDevice(d.ID, func(current *model.Device) {
			current.Probe = d.Probe
			current.Addresses = d.Addresses
			current.LastSeen = d.LastSeen
		})
	}
}
