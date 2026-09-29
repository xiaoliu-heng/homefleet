package server

import (
	"bytes"
	"encoding/json"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"github.com/xiaoliu-heng/homefleet/internal/store"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "fleet.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	hash, _ := bcrypt.GenerateFromPassword([]byte("test-password-12345"), bcrypt.MinCost)
	s.SetMeta("password", string(hash))
	return New(s, "http://localhost", "../../web/dist", true), s
}
func request(a *Server, method, path string, body any, cookie *http.Cookie, bearer string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-HomeFleet-Request", "1")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func login(t *testing.T, a *Server) *http.Cookie {
	t.Helper()
	r := request(a, "POST", "/api/v1/login", map[string]string{"password": "test-password-12345"}, nil, "")
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	return r.Result().Cookies()[0]
}
func TestLoginAndCSRF(t *testing.T) {
	a, _ := fixture(t)
	if r := request(a, "GET", "/api/v1/devices", nil, nil, ""); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := request(a, "POST", "/api/v1/login", map[string]string{"password": "wrong"}, nil, ""); r.Code != 401 {
		t.Fatal(r.Code)
	}
	cookie := login(t, a)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("weak session cookie")
	}
	if r := request(a, "GET", "/api/v1/devices", nil, cookie, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r := httptest.NewRequest("POST", "/api/v1/enrollment", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-HomeFleet-Request", "1")
	r.Header.Set("Origin", "https://malicious.example")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	request(a, "POST", "/api/v1/logout", map[string]bool{}, cookie, "")
	if r := request(a, "GET", "/api/v1/devices", nil, cookie, ""); r.Code != 401 {
		t.Fatal("logout failed")
	}
}
func TestAgentRegistrationCannotEscalateToAdmin(t *testing.T) {
	a, s := fixture(t)
	cookie := login(t, a)
	res := request(a, "POST", "/api/v1/enrollment", map[string]string{}, cookie, "")
	var enrollment map[string]any
	json.Unmarshal(res.Body.Bytes(), &enrollment)
	res = request(a, "POST", "/agent/v1/register", map[string]any{"token": enrollment["token"], "device": model.Device{Name: "test-agent", OS: "linux"}}, nil, "")
	if res.Code != 201 {
		t.Fatal(res.Body.String())
	}
	var credentials map[string]string
	json.Unmarshal(res.Body.Bytes(), &credentials)
	if r := request(a, "GET", "/api/v1/devices", nil, nil, credentials["token"]); r.Code != 401 {
		t.Fatal("agent credential granted admin")
	}
	if r := request(a, "POST", "/agent/v1/heartbeat", map[string]any{"device": model.Device{Name: "changed"}, "sample": model.Sample{}}, nil, credentials["token"]); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	d, _ := s.Device(credentials["id"])
	if d.Name != "test-agent" || !d.Online {
		t.Fatal("heartbeat failed")
	}
	request(a, "POST", "/api/v1/devices/"+d.ID+"/revoke", map[string]bool{}, cookie, "")
	if r := request(a, "POST", "/agent/v1/heartbeat", map[string]any{"device": model.Device{}, "sample": model.Sample{}}, nil, credentials["token"]); r.Code != 401 {
		t.Fatal("revoked agent accepted")
	}
}
func TestAdditionalOriginIsExplicitAndEnrollmentUsesIt(t *testing.T) {
	t.Setenv("HOMEFLEET_ALLOWED_ORIGINS", "https://192.0.2.10:8443, https://bad.example/path, *")
	a, s := fixture(t)
	cookie := login(t, a)
	if res := request(a, "GET", "/api/v1/enrollment-addresses", nil, nil, ""); res.Code != 401 {
		t.Fatal("private enrollment addresses exposed without login")
	}
	token, _ := s.NewEnrollment()
	if _, _, err := s.Enroll(token, model.Device{Name: "hub host", Addresses: []model.Address{{Interface: "eno1", Address: "192.0.2.10/24"}, {Interface: "docker0", Address: "172.17.0.1/16"}}}); err != nil {
		t.Fatal(err)
	}
	res := request(a, "GET", "/api/v1/enrollment-addresses", nil, cookie, "")
	var options struct {
		Addresses []enrollmentAddress `json:"addresses"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &options); err != nil || res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if len(options.Addresses) != 1 || options.Addresses[0].URL != "https://192.0.2.10:8443" || options.Addresses[0].Interface != "eno1" || !options.Addresses[0].RequiresCA {
		t.Fatalf("incorrect or unconfigured entrypoint: %+v", options)
	}
	for _, origin := range []string{"https://192.0.2.10:8443", "https://192.0.2.10", "https://192.0.2.11:8443", "https://bad.example", "null"} {
		r := httptest.NewRequest("POST", "/api/v1/enrollment", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-HomeFleet-Request", "1")
		r.Header.Set("Origin", origin)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if origin != "https://192.0.2.10:8443" {
			if w.Code != 403 {
				t.Fatalf("unexpected origin accepted: %s (%d)", origin, w.Code)
			}
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 201 || v["hub_url"] != origin {
			t.Fatalf("enrollment must use reachable allowed origin: %s", w.Body.String())
		}
	}
}
func TestProjectSecretsNeverReturnedAndSnapshotsPreserved(t *testing.T) {
	a, s := fixture(t)
	cookie := login(t, a)
	p := model.Project{Name: "test", Directory: "/srv/app", Repository: "https://example.com/app.git", Ref: "main", SecretEnv: map[string]string{"SECRET": "never-in-public-response"}}
	res := request(a, "POST", "/api/v1/projects", p, cookie, "")
	if res.Code != 201 {
		t.Fatal(res.Body.String())
	}
	if strings.Contains(res.Body.String(), "never-in-public-response") {
		t.Fatal("secret leaked")
	}
	var saved model.Project
	json.Unmarshal(res.Body.Bytes(), &saved)
	token, _ := s.NewEnrollment()
	d, _, _ := s.Enroll(token, model.Device{Name: "target"})
	action := model.Action{Kind: "project", Operation: "deploy", ProjectID: saved.ID, Secrets: map[string]string{"EVIL": "do-not-trust"}, Project: &model.Project{Name: "forged"}}
	res = request(a, "POST", "/api/v1/jobs/preview", map[string]any{"device_ids": []string{d.ID}, "action": action}, cookie, "")
	if res.Code != 201 {
		t.Fatal(res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret_envelope") || strings.Contains(res.Body.String(), "do-not-trust") || strings.Contains(res.Body.String(), "forged") {
		t.Fatal("untrusted action or encrypted secret leaked")
	}
	job, _ := s.Claim(d.ID)
	if job.Action.Project.Name != "test" || job.Action.Secrets["SECRET"] != "never-in-public-response" {
		t.Fatal("agent did not receive stored project snapshot")
	}
}

func TestPublicCertificateEnrollmentUsesCanonicalDomainWithoutCustomCA(t *testing.T) {
	t.Setenv("HOMEFLEET_CUSTOM_CA", "false")
	t.Setenv("HOMEFLEET_ALLOWED_ORIGINS", "https://legacy.example:8443")
	_, s := fixture(t)
	a := New(s, "https://homefleet.example.test", "../../web/dist", false)
	cookie := login(t, a)
	r := httptest.NewRequest("POST", "/api/v1/enrollment", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-HomeFleet-Request", "1")
	r.Header.Set("Origin", "https://legacy.example:8443")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 201 || v["requires_ca"] != false || v["hub_url"] != "https://homefleet.example.test" {
		t.Fatal("Public certificate enrollment must use its canonical domain and system trust")
	}
	t.Setenv("HOMEFLEET_CUSTOM_CA", "")
	a = New(s, "https://homefleet.example.test", "../../web/dist", false)
	w = request(a, "POST", "/api/v1/enrollment", map[string]any{}, cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v["requires_ca"] != true {
		t.Fatal("Standalone deployment must retain explicit local CA instructions")
	}
}

func TestPublicInstallerDownloadsExposeOnlyReleaseFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMEFLEET_RELEASES", dir)
	for _, name := range []string{"bootstrap-unix.sh", "bootstrap-windows.ps1", "install-unix.sh", "homefleet-agent-linux-amd64", "SHA256SUMS", "LICENSE", "THIRD_PARTY_NOTICES.md", "master.key"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture:"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := fixture(t)
	for path, name := range map[string]string{
		"/install.sh": "bootstrap-unix.sh", "/install.ps1": "bootstrap-windows.ps1",
		"/downloads/install-unix.sh": "install-unix.sh", "/downloads/SHA256SUMS": "SHA256SUMS",
		"/downloads/homefleet-agent-linux-amd64": "homefleet-agent-linux-amd64",
		"/downloads/LICENSE":                     "LICENSE", "/downloads/THIRD_PARTY_NOTICES.md": "THIRD_PARTY_NOTICES.md",
	} {
		r := request(a, "GET", path, nil, nil, "")
		if r.Code != 200 || r.Body.String() != "fixture:"+name {
			t.Fatalf("public release %s: %d %s", path, r.Code, r.Body.String())
		}
	}
	for _, path := range []string{"/downloads/master.key", "/downloads/.env", "/downloads/homefleet.db", "/downloads/agent.json", "/downloads/homefleet-agent-missing"} {
		if r := request(a, "GET", path, nil, nil, ""); r.Code != 404 {
			t.Fatalf("non-release path exposed: %s (%d)", path, r.Code)
		}
	}
	for _, path := range []string{"/api/v1/devices", "/api/v1/downloads/SHA256SUMS"} {
		if r := request(a, "GET", path, nil, nil, ""); r.Code != 401 {
			t.Fatalf("admin route lost authentication: %s", path)
		}
	}
	if r := request(a, "POST", "/api/v1/enrollment", map[string]any{}, nil, ""); r.Code != 401 {
		t.Fatal("public downloads must not allow unauthenticated enrollment token creation")
	}
}
func TestProbeValidationAndManagementLink(t *testing.T) {
	for _, p := range []model.Probe{{Type: "icmp", Target: "; rm -rf /"}, {Type: "http", Target: "file:///etc/passwd"}, {Type: "tcp", Target: "missing-port"}} {
		if ValidateProbe(p) == nil {
			t.Fatalf("bad probe accepted %+v", p)
		}
	}
	if safeURL("javascript:alert(1)") {
		t.Fatal("script link accepted")
	}
	a, _ := fixture(t)
	cookie := login(t, a)
	res := request(a, "POST", "/api/v1/appliances", map[string]any{"name": "AP", "group": "home", "management_url": "http://192.168.1.1", "probe": model.Probe{Type: "tcp", Target: "192.168.1.2:80"}}, cookie, "")
	if res.Code != 201 {
		t.Fatal(res.Body.String())
	}
	var d model.Device
	json.Unmarshal(res.Body.Bytes(), &d)
	if d.Online {
		t.Fatal("new appliance falsely shown online")
	}
}
