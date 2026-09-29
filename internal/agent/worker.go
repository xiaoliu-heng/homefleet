package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/xiaoliu-heng/homefleet/internal/model"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"
)

type workerRequest struct {
	Step    model.Step        `json:"step"`
	Secrets map[string]string `json:"secrets"`
}
type workerEvent struct {
	Stream    string `json:"stream,omitempty"`
	Text      string `json:"text,omitempty"`
	Done      bool   `json:"done"`
	Code      int    `json:"code"`
	Error     string `json:"error,omitempty"`
	Uncertain bool   `json:"uncertain,omitempty"`
}

func (e *Engine) workerToken() (string, error) {
	raw, err := os.ReadFile(e.Config.WorkerTokenFile)
	return strings.TrimSpace(string(raw)), err
}
func (e *Engine) workerReady() error {
	if e.Config.RunUser == "" || e.Config.WorkerURL == "" || e.Config.WorkerTokenFile == "" {
		return errors.New("需要配置 Windows 用户执行器")
	}
	token, err := e.workerToken()
	if err != nil {
		return err
	}
	req, err := http.NewRequest("GET", e.Config.WorkerURL+"/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	c := &http.Client{Timeout: 2 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		return errors.New("需要用户登录并启动用户执行器；锁屏可以继续运行")
	}
	defer res.Body.Close()
	var v struct {
		User string `json:"user"`
	}
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&v) != nil {
		return errors.New("用户执行器认证失败")
	}
	current := strings.ToLower(v.User)
	want := strings.ToLower(e.Config.RunUser)
	if current != want && !strings.HasSuffix(current, "\\"+want) {
		return errors.New("用户执行器运行账号不匹配")
	}
	return nil
}
func (e *Engine) workerCommand(ctx context.Context, step model.Step, secrets map[string]string, emit func(string, string)) (int, error) {
	if err := e.workerReady(); err != nil {
		return -1, err
	}
	token, err := e.workerToken()
	if err != nil {
		return -1, err
	}
	raw, _ := json.Marshal(workerRequest{Step: step, Secrets: secrets})
	req, err := http.NewRequestWithContext(ctx, "POST", e.Config.WorkerURL+"/run", bytes.NewReader(raw))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{}).Do(req)
	if err != nil {
		return -1, fmt.Errorf("%w: 用户执行器连接丢失: %v", ErrUncertain, err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return -1, fmt.Errorf("用户执行器拒绝任务: %d", response.StatusCode)
	}
	scan := bufio.NewScanner(response.Body)
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		var event workerEvent
		if err = json.Unmarshal(scan.Bytes(), &event); err != nil {
			return -1, fmt.Errorf("%w: 无效的执行器响应: %v", ErrUncertain, err)
		}
		if event.Done {
			if event.Uncertain {
				return event.Code, fmt.Errorf("%w: %s", ErrUncertain, event.Error)
			}
			if event.Error != "" {
				return event.Code, errors.New(event.Error)
			}
			return event.Code, nil
		}
		emit(event.Stream, event.Text)
	}
	return -1, ErrUncertain
}
func ServeWorker(ctx context.Context, listen, tokenFile string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host != "127.0.0.1" {
		return errors.New("用户执行器只能监听 127.0.0.1")
	}
	tokenRaw, err := os.ReadFile(tokenFile)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(tokenRaw))
	if len(token) < 32 {
		return errors.New("worker token too short")
	}
	mux := http.NewServeMux()
	var busy sync.Mutex
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return false
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, _ := url.Parse(origin)
			if u == nil || u.Host != r.Host {
				http.Error(w, "origin denied", 403)
				return false
			}
		}
		return true
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		u, err := user.Current()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"user": u.Username})
	})
	mux.HandleFunc("POST /run", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if !busy.TryLock() {
			http.Error(w, "worker busy", 409)
			return
		}
		defer busy.Unlock()
		var req workerRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		req.Step.Identity = "current"
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		f := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		f.Flush()
		runCtx := context.Background()
		cancel := func() {}
		if !req.Step.PackageTransaction {
			runCtx, cancel = context.WithTimeout(runCtx, 30*time.Minute)
		}
		defer cancel()
		code, err := localCommand(runCtx, req.Step, req.Secrets, "", func(stream, text string) {
			enc.Encode(workerEvent{Stream: stream, Text: Redact(text, req.Secrets)})
			f.Flush()
		})
		v := workerEvent{Done: true, Code: code, Uncertain: errors.Is(err, ErrUncertain)}
		if err != nil {
			v.Error = err.Error()
		}
		enc.Encode(v)
		f.Flush()
	})
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
