package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/xiaoliu-heng/homefleet/internal/model"
)

func (s *Store) NewJob(mode string, a model.Action, ids []string, source string, plans map[string]*model.Plan) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newJob(mode, a, ids, source, plans)
}
func (s *Store) newJob(mode string, a model.Action, ids []string, source string, plans map[string]*model.Plan) (model.Job, error) {
	j := model.Job{ID: ID(), Mode: mode, Action: a, CreatedAt: time.Now().UTC(), SourceID: source, Targets: []model.Target{}}
	if len(ids) == 0 || len(ids) > 100 {
		return j, errors.New("请选择 1 至 100 台设备")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		d, e := s.Device(id)
		if e != nil {
			return j, e
		}
		t := model.Target{ID: ID(), JobID: j.ID, DeviceID: id, DeviceName: d.Name, State: "queued", Plan: plans[id]}
		if d.Kind != "agent" {
			t.State = "blocked"
			t.Reason = "该设备仅支持外部状态监测"
		} else if d.Revoked || !d.Online {
			t.State = "blocked"
			t.Reason = "设备离线或凭据已撤销"
		}
		if t.State == "queued" && a.Kind == "agent" && !d.Capabilities["agent_update"].Available {
			t.State = "blocked"
			t.Reason = d.Capabilities["agent_update"].Reason
			if t.Reason == "" {
				t.Reason = "此 Agent 尚不支持后台更新，请先用新版安装命令升级一次"
			}
		}
		if t.State == "queued" && a.Kind == "package" && a.Operation == "install" && d.Capabilities["packages"].Reason == "pacman" && !d.Capabilities["pacman_cached_install"].Available {
			t.State = "blocked"
			t.Reason = "Arch 默认安装方式需要 Agent 0.2.2 或更新版本；请先在 Agent 更新中升级该设备，再重新预览"
		}
		if mode == "execute" && t.State == "blocked" {
			return j, fmt.Errorf("%s: %s，请重新预览", d.Name, t.Reason)
		}
		j.Targets = append(j.Targets, t)
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return j, e
	}
	defer tx.Rollback()
	var src any
	if source != "" {
		src = source
	}
	if _, e = tx.Exec("INSERT INTO jobs VALUES(?,?,?,?)", j.ID, encode(j), stamp(j.CreatedAt), src); e != nil {
		return j, e
	}
	for _, t := range j.Targets {
		if _, e = tx.Exec("INSERT INTO targets VALUES(?,?,?,?,?)", t.ID, j.ID, t.DeviceID, t.State, encode(t)); e != nil {
			return j, e
		}
	}
	return j, tx.Commit()
}
func (s *Store) Job(id string) (model.Job, error) {
	var j model.Job
	var raw string
	e := s.DB.QueryRow("SELECT body FROM jobs WHERE id=?", id).Scan(&raw)
	if e != nil {
		return j, e
	}
	if e = json.Unmarshal([]byte(raw), &j); e != nil {
		return j, e
	}
	rows, e := s.DB.Query("SELECT body,state FROM targets WHERE job_id=? ORDER BY rowid", id)
	if e != nil {
		return j, e
	}
	defer rows.Close()
	j.Targets = []model.Target{}
	counts := map[string]int{}
	for rows.Next() {
		var b, st string
		rows.Scan(&b, &st)
		var t model.Target
		json.Unmarshal([]byte(b), &t)
		t.State = st
		j.Targets = append(j.Targets, t)
		counts[st]++
	}
	j.Status = "succeeded"
	switch {
	case counts["running"] > 0:
		j.Status = "running"
	case counts["queued"] > 0:
		j.Status = "queued"
	case counts["unknown"] > 0:
		j.Status = "unknown"
	case counts["succeeded"] == len(j.Targets):
	case counts["succeeded"] > 0:
		j.Status = "partial"
	case counts["failed"] > 0:
		j.Status = "failed"
	case counts["blocked"] > 0:
		j.Status = "blocked"
	case counts["cancelled"] > 0:
		j.Status = "cancelled"
	}
	return j, rows.Err()
}
func (s *Store) Jobs() ([]model.Job, error) {
	rows, e := s.DB.Query("SELECT id FROM jobs ORDER BY created DESC LIMIT 100")
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []model.Job{}
	for _, id := range ids {
		j, e := s.Job(id)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, nil
}
func (s *Store) Execute(id string, selected []string) (model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing string
	if s.DB.QueryRow("SELECT id FROM jobs WHERE source_id=?", id).Scan(&existing) == nil {
		return s.Job(existing)
	}
	j, e := s.Job(id)
	if e != nil {
		return j, e
	}
	if j.Mode != "preview" {
		return j, errors.New("仅预检查任务可以提交执行")
	}
	if len(selected) == 0 {
		return j, errors.New("请确认要执行的设备")
	}
	plans := map[string]*model.Plan{}
	allowed := map[string]bool{}
	for _, t := range j.Targets {
		if t.State == "succeeded" && t.Plan != nil && time.Now().Before(t.Plan.ExpiresAt) {
			plans[t.DeviceID] = t.Plan
			allowed[t.DeviceID] = true
		}
	}
	for _, device := range selected {
		if !allowed[device] {
			return j, errors.New("目标未通过预检查或预览已过期，请重新预览")
		}
	}
	return s.newJob("execute", j.Action, selected, id, plans)
}
func (s *Store) Target(id string) (model.Target, error) {
	var t model.Target
	var raw, st string
	e := s.DB.QueryRow("SELECT body,state FROM targets WHERE id=?", id).Scan(&raw, &st)
	if e != nil {
		return t, e
	}
	e = json.Unmarshal([]byte(raw), &t)
	t.State = st
	return t, e
}
func saveTarget(tx *sql.Tx, t model.Target) error {
	_, e := tx.Exec("UPDATE targets SET state=?,body=? WHERE id=?", t.State, encode(t), t.ID)
	return e
}
func (s *Store) Claim(deviceID string) (*model.Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, e := s.Device(deviceID)
	if e != nil || d.Revoked {
		return nil, errors.New("设备不可用")
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var running int
	tx.QueryRow("SELECT count(*) FROM targets WHERE state='running'").Scan(&running)
	if running >= 3 {
		return nil, nil
	}
	var local int
	tx.QueryRow("SELECT count(*) FROM targets WHERE device_id=? AND state='running'", deviceID).Scan(&local)
	if local > 0 {
		return nil, nil
	}
	var raw, jraw string
	e = tx.QueryRow(`SELECT t.body,j.body FROM targets t JOIN jobs j ON j.id=t.job_id
 WHERE t.device_id=? AND t.state='queued'
 AND (json_extract(j.body,'$.mode')!='execute' OR NOT EXISTS (SELECT 1 FROM targets u WHERE u.device_id=t.device_id AND u.state='unknown'))
 ORDER BY j.created,t.rowid LIMIT 1`, deviceID).Scan(&raw, &jraw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var t model.Target
	var j model.Job
	json.Unmarshal([]byte(raw), &t)
	json.Unmarshal([]byte(jraw), &j)
	now := time.Now().UTC()

	t.State = "running"
	t.ClaimedAt = &now
	if e = saveTarget(tx, t); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	a := j.Action
	if a.SecretEnvelope != "" {
		a.Secrets, e = s.Decrypt(a.SecretEnvelope)
		if e != nil {
			return nil, e
		}
		a.SecretEnvelope = ""
	}
	return &model.Assignment{JobID: j.ID, TargetID: t.ID, Mode: j.Mode, Action: a, Plan: t.Plan}, nil
}
func (s *Store) AddLogs(device, target string, logs []model.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.Target(target)
	if e != nil {
		return e
	}
	if t.DeviceID != device {
		return errors.New("设备不能写入其他设备的日志")
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, v := range logs {
		if v.Seq <= 0 || len(v.Text) > 65536 {
			return errors.New("invalid log")
		}
		if _, e = tx.Exec("INSERT OR IGNORE INTO logs VALUES(?,?,?)", target, v.Seq, encode(v)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Logs(target string, after int) ([]model.Log, error) {
	rows, e := s.DB.Query("SELECT body FROM logs WHERE target_id=? AND seq>? ORDER BY seq LIMIT 2000", target, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Log{}
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		var v model.Log
		json.Unmarshal([]byte(raw), &v)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Finish(device, target string, r model.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.Target(target)
	if e != nil {
		return e
	}
	if t.DeviceID != device {
		return errors.New("设备不匹配")
	}
	if model.Terminal(t.State) {
		return nil
	}
	if t.State != "running" && t.State != "unknown" {
		return errors.New("任务尚未领取")
	}
	if !model.Terminal(r.State) && r.State != "unknown" {
		return errors.New("invalid result state")
	}
	j, e := s.Job(t.JobID)
	if e != nil {
		return e
	}
	if j.Mode == "preview" && r.State == "succeeded" {
		if r.Plan == nil {
			return errors.New("缺少预检查计划")
		}
		r.Plan.ExpiresAt = time.Now().UTC().Add(5 * time.Minute)
	}
	if j.Mode != "preview" {
		r.Plan = nil
	}
	r.FinishedAt = time.Now().UTC()
	t.State = r.State
	t.Reason = r.Reason
	t.Result = &r
	if r.Plan != nil {
		t.Plan = r.Plan
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = saveTarget(tx, t); e != nil {
		return e
	}
	if r.Inventory != nil {
		r.Inventory.At = time.Now().UTC()
		if _, e = tx.Exec("INSERT INTO inventories VALUES(?,?) ON CONFLICT(device_id) DO UPDATE SET body=excluded.body", device, encode(r.Inventory)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.Job(id)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, t := range j.Targets {
		t.CancelRequested = true
		if t.State == "queued" {
			t.State = "cancelled"
			t.Reason = "执行前取消"
		}
		if e = saveTarget(tx, t); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Reconcile(device string, known []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	exists := map[string]bool{}
	for _, id := range known {
		exists[id] = true
	}
	rows, e := s.DB.Query("SELECT body FROM targets WHERE device_id=? AND state='running'", device)
	if e != nil {
		return e
	}
	list := []model.Target{}
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		var t model.Target
		json.Unmarshal([]byte(raw), &t)
		if !exists[t.ID] {
			list = append(list, t)
		}
	}
	rows.Close()
	for _, t := range list {
		t.State = "unknown"
		t.Reason = "Agent 重连后无本地执行凭证，需要核实，未自动重跑"
		_, e = s.DB.Exec("UPDATE targets SET state=?,body=? WHERE id=?", t.State, encode(t), t.ID)
		if e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) MarkDisconnected() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.DB.Query("SELECT t.body FROM targets t JOIN devices d ON d.id=t.device_id WHERE t.state='running' AND (d.last_seen<? OR d.revoked=1)", stamp(time.Now().Add(-45*time.Second)))
	if e != nil {
		return e
	}
	list := []model.Target{}
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		var t model.Target
		json.Unmarshal([]byte(raw), &t)
		list = append(list, t)
	}
	rows.Close()
	for _, t := range list {
		// An Agent replacement deliberately restarts its connection. Keep its
		// concurrency slot until the bounded upgrade/rollback window has elapsed.
		if t.Plan != nil && t.Plan.AgentUpdate != nil && t.ClaimedAt != nil && time.Since(*t.ClaimedAt) < 15*time.Minute {
			continue
		}
		t.State = "unknown"
		t.Reason = "设备连接中断，等待本地执行结果；不会自动重跑"
		_, e = s.DB.Exec("UPDATE targets SET state=?,body=? WHERE id=?", t.State, encode(t), t.ID)
		if e != nil {
			return e
		}
	}
	return nil
}

// Resolve is deliberately separate from retry: an operator must first verify the real outcome.
func (s *Store) Resolve(target, state, note string) error {
	if (state != "failed" && state != "succeeded") || len(note) < 4 {
		return errors.New("请填写实机核实说明并选择成功或失败")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.Target(target)
	if e != nil {
		return e
	}
	if t.State != "unknown" {
		return errors.New("只允许核实状态未知的任务")
	}
	t.State = state
	t.Reason = "人工核实：" + note
	_, e = s.DB.Exec("UPDATE targets SET state=?,body=? WHERE id=?", state, encode(t), t.ID)
	return e
}
