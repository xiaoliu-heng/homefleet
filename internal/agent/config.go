package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Service         string `json:"service,omitempty"`
	ConfigPath      string `json:"-"`
	HubURL          string `json:"hub_url"`
	DeviceID        string `json:"device_id"`
	Token           string `json:"token"`
	Name            string `json:"name"`
	RunUser         string `json:"run_user"`
	DataDir         string `json:"data_dir"`
	CACert          string `json:"ca_cert,omitempty"`
	ReadOnly        bool   `json:"read_only"`
	WorkerURL       string `json:"worker_url,omitempty"`
	WorkerTokenFile string `json:"worker_token_file,omitempty"`
}

func SaveJSON(path string, v any) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	raw, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return replaceFile(tmp, path)
}
func LoadConfig(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	c.ConfigPath, _ = filepath.Abs(path)
	return c, e
}

type Client struct {
	Config Config
	HTTP   *http.Client
}

func NewClient(c Config) (*Client, error) {
	u, e := url.Parse(c.HubURL)
	if e != nil || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid hub URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("Agent requires HTTPS; HTTP is only allowed on loopback for development")
		}
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if c.CACert != "" {
		b, e := os.ReadFile(c.CACert)
		if e != nil {
			return nil, e
		}
		if !roots.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid CA certificate")
		}
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, Proxy: http.ProxyFromEnvironment}
	return &Client{Config: c, HTTP: &http.Client{Transport: tr, Timeout: 35 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return errors.New("hub redirect refused") }}}, nil
}
func (c *Client) Request(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return 0, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.Config.HubURL+path, body)
	if e != nil {
		return 0, e
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Config.Token)
	}
	r, e := c.HTTP.Do(req)
	if e != nil {
		return 0, e
	}
	defer r.Body.Close()
	if r.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		return r.StatusCode, fmt.Errorf("hub %d: %s", r.StatusCode, raw)
	}
	if out != nil && r.StatusCode != 204 {
		e = json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(out)
	} else {
		io.Copy(io.Discard, r.Body)
	}
	return r.StatusCode, e
}
