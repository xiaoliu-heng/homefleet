package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiaoliu-heng/homefleet/internal/model"
	"github.com/xiaoliu-heng/homefleet/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	Store       *store.Store
	PublicURL   string
	WebDir      string
	Dev         bool
	mux         *http.ServeMux
	mu          sync.Mutex
	subscribers map[chan struct{}]bool
	attempts    map[string][]time.Time
	origins     map[string]bool
	requiresCA  bool
}

func New(s *store.Store, publicURL, web string, dev bool) *Server {
	a := &Server{Store: s, PublicURL: strings.TrimRight(publicURL, "/"), WebDir: web, Dev: dev, mux: http.NewServeMux(), subscribers: map[chan struct{}]bool{}, attempts: map[string][]time.Time{}}
	a.origins = map[string]bool{a.PublicURL: true}
	a.requiresCA = os.Getenv("HOMEFLEET_CUSTOM_CA") != "false"
	for _, entry := range strings.Split(os.Getenv("HOMEFLEET_ALLOWED_ORIGINS"), ",") {
		origin := strings.TrimRight(strings.TrimSpace(entry), "/")
		u, err := url.Parse(origin)
		if err == nil && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || (dev && u.Scheme == "http")) {
			a.origins[origin] = true
		}
	}
	a.routes()
	return a
}
func (a *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "HEAD" {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			fail(w, 415, "请求必须使用 JSON")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-HomeFleet-Request")), []byte("1")) != 1 {
				fail(w, 403, "缺少请求校验标记")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && !a.origins[origin] {
				fail(w, 403, "请求来源不匹配，请检查 HOMEFLEET_PUBLIC_URL")
				return
			}
		}
	}
	a.mux.ServeHTTP(w, r)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		fail(w, 400, "无效的 JSON: "+e.Error())
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}
func (a *Server) changed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for ch := range a.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (a *Server) validSession(r *http.Request) bool {
	c, e := r.Cookie("homefleet_session")
	if e != nil {
		return false
	}
	var expiry string
	if a.Store.DB.QueryRow("SELECT expires FROM sessions WHERE hash=?", store.Hash(c.Value)).Scan(&expiry) != nil {
		return false
	}
	at, _ := time.Parse(time.RFC3339Nano, expiry)
	return time.Now().Before(at)
}
func (a *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.validSession(r) {
			fail(w, 401, "请先登录")
			return
		}
		h(w, r)
	}
}
func (a *Server) agent(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get("Authorization")
		if !strings.HasPrefix(v, "Bearer ") {
			fail(w, 401, "缺少 Agent 凭据")
			return
		}
		id, e := a.Store.AgentID(strings.TrimPrefix(v, "Bearer "))
		if e != nil {
			fail(w, 401, "Agent 凭据无效或已撤销")
			return
		}
		h(w, r, id)
	}
}
func publicJob(j model.Job) model.Job {
	j.Action.Secrets = nil
	j.Action.SecretEnvelope = ""
	if j.Action.Project != nil {
		p := *j.Action.Project
		p.SecretEnv = nil
		j.Action.Project = &p
	}
	return j
}
func (a *Server) routes() {
	m := a.mux
	m.HandleFunc("GET /api/v1/agent-release", a.admin(a.agentRelease))
	m.HandleFunc("GET /downloads/agents/{version}/{name}", a.versionedDownload)
	m.HandleFunc("GET /api/v1/downloads/{name}", a.admin(a.download))
	// Release files contain no credentials. Enrollment still requires a one-time token.
	m.HandleFunc("GET /downloads/{name}", a.download)
	m.HandleFunc("GET /install.sh", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("name", "bootstrap-unix.sh")
		a.download(w, r)
	})
	m.HandleFunc("GET /install.ps1", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("name", "bootstrap-windows.ps1")
		a.download(w, r)
	})
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if e := a.Store.DB.Ping(); e != nil {
			fail(w, 503, "database unavailable")
			return
		}
		write(w, 200, map[string]string{"status": "ok", "version": model.Version})
	})
	m.HandleFunc("POST /api/v1/login", a.login)
	m.HandleFunc("GET /api/v1/me", a.admin(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"name": "管理员", "version": model.Version, "public_url": a.PublicURL})
	}))
	m.HandleFunc("POST /api/v1/logout", a.admin(func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie("homefleet_session")
		a.Store.DB.Exec("DELETE FROM sessions WHERE hash=?", store.Hash(c.Value))
		http.SetCookie(w, &http.Cookie{Name: "homefleet_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !a.Dev, SameSite: http.SameSiteStrictMode})
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/v1/devices", a.admin(func(w http.ResponseWriter, r *http.Request) {
		d, e := a.Store.Devices()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		write(w, 200, d)
	}))
	m.HandleFunc("GET /api/v1/devices/{id}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		d, e := a.Store.Device(r.PathValue("id"))
		if e != nil {
			fail(w, 404, "设备不存在")
			return
		}
		write(w, 200, d)
	}))
	m.HandleFunc("PATCH /api/v1/devices/{id}", a.admin(a.updateDevice))
	m.HandleFunc("POST /api/v1/devices/{id}/revoke", a.admin(func(w http.ResponseWriter, r *http.Request) {
		if e := a.Store.Revoke(r.PathValue("id")); e != nil {
			fail(w, 500, e.Error())
			return
		}
		a.changed()
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/v1/devices/{id}/metrics", a.admin(func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Metrics(r.PathValue("id"))
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		write(w, 200, v)
	}))
	m.HandleFunc("GET /api/v1/devices/{id}/inventory", a.admin(func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Store.Inventory(r.PathValue("id"))) }))
	m.HandleFunc("POST /api/v1/appliances", a.admin(a.addAppliance))
	m.HandleFunc("GET /api/v1/enrollment-addresses", a.admin(a.enrollmentAddresses))
	m.HandleFunc("POST /api/v1/enrollment", a.admin(func(w http.ResponseWriter, r *http.Request) {
		token, e := a.Store.NewEnrollment()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		hubURL := a.PublicURL
		if origin := r.Header.Get("Origin"); a.requiresCA && a.origins[origin] {
			hubURL = origin
		}
		write(w, 201, map[string]any{"token": token, "expires_in": 900, "hub_url": hubURL, "requires_ca": a.requiresCA})
	}))
	m.HandleFunc("GET /api/v1/catalog", a.admin(func(w http.ResponseWriter, r *http.Request) { write(w, 200, model.Catalog) }))
	m.HandleFunc("GET /api/v1/projects", a.admin(func(w http.ResponseWriter, r *http.Request) {
		p, e := a.Store.Projects()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		write(w, 200, p)
	}))
	m.HandleFunc("POST /api/v1/projects", a.admin(a.saveProject))
	m.HandleFunc("GET /api/v1/jobs", a.admin(func(w http.ResponseWriter, r *http.Request) {
		js, e := a.Store.Jobs()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		for i := range js {
			js[i] = publicJob(js[i])
		}
		write(w, 200, js)
	}))
	m.HandleFunc("GET /api/v1/jobs/{id}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		j, e := a.Store.Job(r.PathValue("id"))
		if e != nil {
			fail(w, 404, "任务不存在")
			return
		}
		write(w, 200, publicJob(j))
	}))
	m.HandleFunc("POST /api/v1/jobs/preview", a.admin(a.preview))
	m.HandleFunc("POST /api/v1/jobs/{id}/execute", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			DeviceIDs []string `json:"device_ids"`
		}
		if !decode(w, r, &v) {
			return
		}
		j, e := a.Store.Execute(r.PathValue("id"), v.DeviceIDs)
		if e != nil {
			fail(w, 409, e.Error())
			return
		}
		a.changed()
		write(w, 201, publicJob(j))
	}))
	m.HandleFunc("POST /api/v1/jobs/{id}/cancel", a.admin(func(w http.ResponseWriter, r *http.Request) {
		if e := a.Store.Cancel(r.PathValue("id")); e != nil {
			fail(w, 400, e.Error())
			return
		}
		a.changed()
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /api/v1/jobs/{id}/retry", a.admin(a.retry))
	m.HandleFunc("GET /api/v1/targets/{id}/logs", a.admin(func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		v, e := a.Store.Logs(r.PathValue("id"), after)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		write(w, 200, v)
	}))
	m.HandleFunc("POST /api/v1/targets/{id}/resolve", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			State string `json:"state"`
			Note  string `json:"note"`
		}
		if !decode(w, r, &v) {
			return
		}
		if e := a.Store.Resolve(r.PathValue("id"), v.State, v.Note); e != nil {
			fail(w, 400, e.Error())
			return
		}
		a.changed()
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/v1/events", a.admin(a.events))
	m.HandleFunc("POST /agent/v1/register", a.register)
	m.HandleFunc("POST /agent/v1/heartbeat", a.agent(func(w http.ResponseWriter, r *http.Request, id string) {
		var v struct {
			Device model.Device `json:"device"`
			Sample model.Sample `json:"sample"`
		}
		if !decode(w, r, &v) {
			return
		}
		if e := a.Store.Heartbeat(id, v.Device, v.Sample); e != nil {
			fail(w, 400, e.Error())
			return
		}
		a.changed()
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /agent/v1/reconcile", a.agent(func(w http.ResponseWriter, r *http.Request, id string) {
		var v struct {
			Known []string `json:"known"`
		}
		if !decode(w, r, &v) {
			return
		}
		if e := a.Store.Reconcile(id, v.Known); e != nil {
			fail(w, 500, e.Error())
			return
		}
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /agent/v1/claim", a.agent(a.claim))
	m.HandleFunc("GET /agent/v1/targets/{id}/control", a.agent(func(w http.ResponseWriter, r *http.Request, id string) {
		t, e := a.Store.Target(r.PathValue("id"))
		if e != nil || t.DeviceID != id {
			fail(w, 404, "任务不存在")
			return
		}
		write(w, 200, map[string]any{"cancel": t.CancelRequested, "state": t.State})
	}))
	m.HandleFunc("POST /agent/v1/targets/{id}/logs", a.agent(func(w http.ResponseWriter, r *http.Request, id string) {
		var logs []model.Log
		if !decode(w, r, &logs) {
			return
		}
		if len(logs) > 500 {
			fail(w, 400, "日志批次过大")
			return
		}
		if e := a.Store.AddLogs(id, r.PathValue("id"), logs); e != nil {
			fail(w, 400, e.Error())
			return
		}
		a.changed()
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /agent/v1/targets/{id}/result", a.agent(func(w http.ResponseWriter, r *http.Request, id string) {
		var result model.Result
		if !decode(w, r, &result) {
			return
		}
		if e := a.Store.Finish(id, r.PathValue("id"), result); e != nil {
			fail(w, 400, e.Error())
			return
		}
		a.changed()
		write(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/agent/") {
			fail(w, 404, "接口不存在")
			return
		}
		path := filepath.Join(a.WebDir, filepath.Clean("/"+r.URL.Path))
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			http.ServeFile(w, r, path)
			return
		}
		http.ServeFile(w, r, filepath.Join(a.WebDir, "index.html"))
	})
}

var releaseName = regexp.MustCompile(`^(homefleet-agent-(linux-(amd64|arm64)|darwin-arm64|windows-amd64\.exe)|(bootstrap|install|uninstall)-(unix\.sh|windows\.ps1)|SHA256SUMS|agent-release\.json|LICENSE|THIRD_PARTY_NOTICES\.md)$`)

func (a *Server) download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !releaseName.MatchString(name) {
		fail(w, 404, "下载文件不存在")
		return
	}
	dir := os.Getenv("HOMEFLEET_RELEASES")
	if dir == "" {
		dir = "dist/releases"
	}
	path := filepath.Join(dir, name)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		fail(w, 404, "尚未构建 Agent 发布包，请先运行 scripts/build.sh")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, path)
}
func (a *Server) login(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &v) {
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	prev := a.attempts[ip]
	recent := []time.Time{}
	for _, t := range prev {
		if time.Since(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	limited := len(recent) >= 8
	a.attempts[ip] = append(recent, time.Now())
	a.mu.Unlock()
	if limited {
		fail(w, 429, "尝试次数过多，请一分钟后再试")
		return
	}
	if len(v.Password) > 200 || bcrypt.CompareHashAndPassword([]byte(a.Store.Meta("password")), []byte(v.Password)) != nil {
		fail(w, 401, "密码不正确")
		return
	}
	token := store.ID() + store.ID()
	_, e := a.Store.DB.Exec("INSERT INTO sessions VALUES(?,?)", store.Hash(token), time.Now().UTC().Add(12*time.Hour).Format(time.RFC3339Nano))
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "homefleet_session", Value: token, Path: "/", HttpOnly: true, Secure: !a.Dev, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	write(w, 200, map[string]bool{"ok": true})
}
func (a *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Name          string `json:"name"`
		Group         string `json:"group"`
		ManagementURL string `json:"management_url"`
	}
	if !decode(w, r, &v) {
		return
	}
	d, e := a.Store.Device(r.PathValue("id"))
	if e != nil {
		fail(w, 404, "设备不存在")
		return
	}
	if strings.TrimSpace(v.Name) == "" || !safeURL(v.ManagementURL) {
		fail(w, 400, "名称或管理地址不正确")
		return
	}
	d, e = a.Store.UpdateDevice(d.ID, func(current *model.Device) {
		current.Name = v.Name
		current.Group = v.Group
		current.ManagementURL = v.ManagementURL
	})
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	a.changed()
	write(w, 200, d)
}
func safeURL(v string) bool {
	if v == "" {
		return true
	}
	u, e := url.Parse(v)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
func (a *Server) addAppliance(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Name          string      `json:"name"`
		Group         string      `json:"group"`
		ManagementURL string      `json:"management_url"`
		Probe         model.Probe `json:"probe"`
	}
	if !decode(w, r, &v) {
		return
	}
	if strings.TrimSpace(v.Name) == "" || !safeURL(v.ManagementURL) {
		fail(w, 400, "名称或管理地址不正确")
		return
	}
	if e := ValidateProbe(v.Probe); e != nil {
		fail(w, 400, e.Error())
		return
	}
	d := model.Device{ID: store.ID(), Name: v.Name, Group: v.Group, Kind: "appliance", OS: "network", Platform: "iKuai / 网络设备", CreatedAt: time.Now().UTC(), ManagementURL: v.ManagementURL, Probe: &v.Probe, Addresses: []model.Address{{Interface: "管理地址", Address: v.Probe.Target}}, Capabilities: map[string]model.Capability{}}
	if e := a.Store.SaveDevice(d); e != nil {
		fail(w, 500, e.Error())
		return
	}
	a.changed()
	write(w, 201, d)
}

var envKey = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]*$")

func (a *Server) saveProject(w http.ResponseWriter, r *http.Request) {
	var p model.Project
	if !decode(w, r, &p) {
		return
	}
	hasDirectory := strings.TrimSpace(p.Directory) != ""
	for _, dir := range p.Directories {
		hasDirectory = hasDirectory || strings.TrimSpace(dir) != ""
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 || !hasDirectory {
		fail(w, 400, "项目名称和运行目录必填")
		return
	}
	if p.Repository != "" && !validRepository(p.Repository) {
		fail(w, 400, "仓库须为 HTTPS 或 SSH 地址；凭据请放入设备的 Git 配置")
		return
	}
	if p.Ref == "" {
		p.Ref = "HEAD"
	}
	if strings.HasPrefix(p.Ref, "-") || strings.ContainsAny(p.Ref, "\r\n\x00") {
		fail(w, 400, "无效的 Git 引用")
		return
	}
	for _, values := range []map[string]string{p.Env, p.SecretEnv} {
		for k := range values {
			if !envKey.MatchString(k) {
				fail(w, 400, "无效的环境变量名称")
				return
			}
		}
	}
	saved, e := a.Store.SaveProject(p)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	a.changed()
	write(w, 201, saved)
}
func validRepository(v string) bool {
	if strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "ssh://") {
		u, e := url.Parse(v)
		return e == nil && u.Host != "" && (u.User == nil || u.Scheme == "ssh")
	}
	return regexp.MustCompile(`^[a-zA-Z0-9._-]+@[a-zA-Z0-9.-]+:[a-zA-Z0-9_./-]+$`).MatchString(v)
}
func (a *Server) action(v model.Action) (model.Action, error) {
	out := model.Action{Kind: v.Kind, Operation: v.Operation, Package: v.Package, CatalogID: v.CatalogID, Scope: v.Scope, ProjectID: v.ProjectID}
	switch v.Kind {
	case "agent":
		if v.Operation != "self_update" {
			return out, errors.New("无效的 Agent 操作")
		}
		release, err := currentAgentRelease()
		if err != nil {
			return out, err
		}
		if v.AgentVersion != release.Version {
			return out, errors.New("发布版本已变化，请刷新后重新预览")
		}
		out = model.Action{Kind: "agent", Operation: "self_update", AgentVersion: release.Version, AgentRelease: release}
	case "inventory":
		if v.Operation != "" && v.Operation != "refresh" {
			return out, errors.New("无效的清单操作")
		}
		out.Operation = "refresh"
	case "package":
		if v.Operation == "upgrade_all" {
			out.Package = ""
			out.CatalogID = ""
		}
		if v.Operation != "install" && v.Operation != "upgrade" && v.Operation != "upgrade_all" {
			return out, errors.New("无效的软件操作")
		}
		if v.Scope != "" && v.Scope != "machine" && v.Scope != "user" {
			return out, errors.New("无效的软件安装范围")
		}
		if v.Operation != "upgrade_all" && v.Package == "" && v.CatalogID == "" {
			return out, errors.New("请选择软件")
		}
	case "project", "compose":
		p, e := a.Store.Project(v.ProjectID, false)
		if e != nil {
			return out, errors.New("项目不存在")
		}
		out.SecretEnvelope = p.EncryptedSecrets
		out.Project = &p
		if v.Kind == "project" && v.Operation != "deploy" {
			return out, errors.New("无效的部署操作")
		}
		if v.Kind == "compose" && v.Operation != "status" && v.Operation != "logs" && v.Operation != "start" && v.Operation != "stop" && v.Operation != "update" {
			return out, errors.New("无效的 Compose 操作")
		}
	default:
		return out, errors.New("无效的任务类型")
	}
	return out, nil
}
func (a *Server) preview(w http.ResponseWriter, r *http.Request) {
	var v struct {
		DeviceIDs []string     `json:"device_ids"`
		Action    model.Action `json:"action"`
	}
	if !decode(w, r, &v) {
		return
	}
	action, e := a.action(v.Action)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	mode := "preview"
	if action.Kind == "inventory" {
		mode = "inspect"
	}
	j, e := a.Store.NewJob(mode, action, v.DeviceIDs, "", nil)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	a.changed()
	write(w, 201, publicJob(j))
}
func (a *Server) retry(w http.ResponseWriter, r *http.Request) {
	j, e := a.Store.Job(r.PathValue("id"))
	if e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	ids := []string{}
	for _, t := range j.Targets {
		if t.State == "failed" || t.State == "blocked" {
			ids = append(ids, t.DeviceID)
		}
	}
	if len(ids) == 0 {
		fail(w, 400, "没有可重试的失败目标；结果未知的任务须先实机核实")
		return
	}
	mode := "preview"
	if j.Action.Kind == "inventory" {
		mode = "inspect"
	}
	n, e := a.Store.NewJob(mode, j.Action, ids, "", nil)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	a.changed()
	write(w, 201, publicJob(n))
}
func (a *Server) register(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Token  string       `json:"token"`
		Device model.Device `json:"device"`
	}
	if !decode(w, r, &v) {
		return
	}
	if len(v.Device.Name) == 0 || len(v.Device.Name) > 100 {
		fail(w, 400, "设备名称必填")
		return
	}
	d, token, e := a.Store.Enroll(v.Token, v.Device)
	if e != nil {
		fail(w, 401, e.Error())
		return
	}
	a.changed()
	write(w, 201, map[string]string{"id": d.ID, "token": token})
}
func (a *Server) claim(w http.ResponseWriter, r *http.Request, id string) {
	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		v, e := a.Store.Claim(id)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		if v != nil {
			a.changed()
			write(w, 200, v)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			w.WriteHeader(204)
			return
		case <-tick.C:
		}
	}
}
func (a *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "streaming unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := make(chan struct{}, 1)
	a.mu.Lock()
	a.subscribers[ch] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.subscribers, ch); a.mu.Unlock() }()
	timer := time.NewTicker(10 * time.Second)
	defer timer.Stop()
	fmt.Fprint(w, "data: {\"type\":\"refresh\"}\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			if !a.validSession(r) {
				return
			}
		case <-ch:
		}
		fmt.Fprint(w, "data: {\"type\":\"refresh\"}\n\n")
		flusher.Flush()
	}
}
func (a *Server) Background(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if e := a.Store.MarkDisconnected(); e != nil {
				log.Print(e)
			}
			a.probeAll(ctx)
			n++
			if n%360 == 0 {
				if e := a.Store.Cleanup(); e != nil {
					log.Print(e)
				}
			}
			a.changed()
		}
	}
}
