package scripts

import (
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise real curl, TLS and checksums; fixture executables must never run.
func TestBootstrapDownloads(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH != "arm64" {
		t.Skip("Only Apple Silicon macOS is a supported Agent target")
	}
	for _, scenario := range []string{"valid", "tampered-agent", "tampered-installer", "missing-hash", "untrusted-ca", "insecure-url", "separate-download-address"} {
		t.Run(scenario, func(t *testing.T) {
			files := map[string]string{
				"install-unix.sh": "#!/bin/sh\nexit 93\n", "install-windows.ps1": "throw 'must not execute'\n",
				"homefleet-agent-linux-amd64": "fixture-amd64", "homefleet-agent-linux-arm64": "fixture-arm64",
				"homefleet-agent-darwin-arm64": "fixture-mac", "homefleet-agent-windows-amd64.exe": "fixture-windows",
			}
			var manifest strings.Builder
			for name, contents := range files {
				if scenario != "missing-hash" || !strings.HasPrefix(name, "install-") {
					fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256([]byte(contents)), name)
				}
				if scenario == "tampered-agent" && strings.HasPrefix(name, "homefleet-agent-") || scenario == "tampered-installer" && strings.HasPrefix(name, "install-") {
					files[name] = contents + "corrupted"
				}
			}
			files["SHA256SUMS"] = manifest.String()
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if content, ok := files[strings.TrimPrefix(r.URL.Path, "/downloads/")]; ok && strings.HasPrefix(r.URL.Path, "/downloads/") {
					fmt.Fprint(w, content)
				} else {
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			ca := filepath.Join(t.TempDir(), "test-ca.crt")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			hub := srv.URL
			if scenario == "insecure-url" {
				hub = strings.Replace(hub, "https://", "http://", 1)
			}
			if scenario == "separate-download-address" {
				hub = "https://controller.example.test:8443"
			}
			program := "bash"
			args := []string{"bootstrap-unix.sh", "--hub", hub, "--check-downloads"}
			if scenario != "untrusted-ca" {
				args = append(args, "--ca", ca)
			}
			if scenario == "separate-download-address" {
				args = append(args, "--download-url", srv.URL)
			}
			if runtime.GOOS == "windows" {
				program = "powershell.exe"
				args = []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "bootstrap-windows.ps1", "-Hub", hub, "-CheckDownloads"}
				if scenario != "untrusted-ca" {
					args = append(args, "-CA", ca)
				}
				if scenario == "separate-download-address" {
					args = append(args, "-DownloadUrl", srv.URL)
				}
			}
			cmd := exec.Command(program, args...)
			cmd.Env = append(os.Environ(), "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
			out, err := cmd.CombinedOutput()
			wantSuccess := scenario == "valid" || scenario == "separate-download-address"
			if (err == nil) != wantSuccess || wantSuccess && !strings.Contains(string(out), "no installation or enrollment") {
				t.Fatalf("download check: %v\n%s", err, out)
			}
			if strings.Contains(string(out), "must not execute") {
				t.Fatal("check-only mode executed an installer")
			}
		})
	}
}
