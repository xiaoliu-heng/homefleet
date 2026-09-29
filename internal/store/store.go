package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xiaoliu-heng/homefleet/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB  *sql.DB
	mu  sync.Mutex
	Key []byte
}

func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Open(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY,v TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(hash TEXT PRIMARY KEY,expires TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS enrollment(hash TEXT PRIMARY KEY,expires TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY,body TEXT NOT NULL,token_hash TEXT UNIQUE,revoked INTEGER NOT NULL DEFAULT 0,last_seen TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS metrics(device_id TEXT NOT NULL,at TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(device_id,at));
 CREATE INDEX IF NOT EXISTS metrics_at ON metrics(at);
 CREATE TABLE IF NOT EXISTS inventories(device_id TEXT PRIMARY KEY,body TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,body TEXT NOT NULL,secrets TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,body TEXT NOT NULL,created TEXT NOT NULL,source_id TEXT UNIQUE);
 CREATE TABLE IF NOT EXISTS targets(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs(id),device_id TEXT NOT NULL REFERENCES devices(id),state TEXT NOT NULL,body TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS targets_claim ON targets(device_id,state);
 CREATE TABLE IF NOT EXISTS logs(target_id TEXT NOT NULL,seq INTEGER NOT NULL,body TEXT NOT NULL,PRIMARY KEY(target_id,seq));
 PRAGMA user_version=1;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		db.Close()
		return nil, e
	}
	key := os.Getenv("HOMEFLEET_MASTER_KEY")
	if key != "" {
		s.Key, e = base64.StdEncoding.DecodeString(key)
		if e != nil || len(s.Key) != 32 {
			return nil, errors.New("HOMEFLEET_MASTER_KEY must be 32 base64-encoded bytes")
		}
	} else {
		kp := filepath.Join(filepath.Dir(path), "master.key")
		s.Key, e = os.ReadFile(kp)
		if os.IsNotExist(e) {
			s.Key = make([]byte, 32)
			_, e = rand.Read(s.Key)
			if e == nil {
				e = os.WriteFile(kp, s.Key, 0600)
			}
		}
		if e != nil {
			return nil, e
		}
		if len(s.Key) != 32 {
			return nil, errors.New("invalid master.key")
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func encode(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func (s *Store) SetMeta(k, v string) error {
	_, e := s.DB.Exec("INSERT INTO meta VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", k, v)
	return e
}
func (s *Store) Meta(k string) string {
	var v string
	s.DB.QueryRow("SELECT v FROM meta WHERE k=?", k).Scan(&v)
	return v
}
func (s *Store) Encrypt(values map[string]string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	block, e := aes.NewCipher(s.Key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(g.Seal(n, n, []byte(encode(values)), nil)), nil
}
func (s *Store) Decrypt(v string) (map[string]string, error) {
	out := map[string]string{}
	if v == "" {
		return out, nil
	}
	b, e := base64.StdEncoding.DecodeString(v)
	if e != nil {
		return nil, e
	}
	c, e := aes.NewCipher(s.Key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(c)
	if e != nil {
		return nil, e
	}
	if len(b) < g.NonceSize() {
		return nil, errors.New("invalid secret")
	}
	raw, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	if e != nil {
		return nil, e
	}
	e = json.Unmarshal(raw, &out)
	return out, e
}
func (s *Store) NewEnrollment() (string, error) {
	token := ID() + ID()
	_, e := s.DB.Exec("INSERT INTO enrollment VALUES(?,?)", Hash(token), stamp(time.Now().Add(15*time.Minute)))
	return token, e
}
func (s *Store) Enroll(token string, d model.Device) (model.Device, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.DB.Begin()
	if e != nil {
		return d, "", e
	}
	defer tx.Rollback()
	var expires string
	if e = tx.QueryRow("SELECT expires FROM enrollment WHERE hash=?", Hash(token)).Scan(&expires); e != nil {
		return d, "", errors.New("注册令牌无效或已使用")
	}
	t, _ := time.Parse(time.RFC3339Nano, expires)
	if time.Now().After(t) {
		return d, "", errors.New("注册令牌已过期")
	}
	d.ID = ID()
	d.Kind = "agent"
	d.CreatedAt = time.Now().UTC()
	d.LastSeen = d.CreatedAt
	d.Revoked = false
	d.Latest = nil
	d.Probe = nil
	credential := ID() + ID()
	if _, e = tx.Exec("INSERT INTO devices VALUES(?,?,?,0,?)", d.ID, encode(d), Hash(credential), stamp(d.LastSeen)); e != nil {
		return d, "", e
	}
	if _, e = tx.Exec("DELETE FROM enrollment WHERE hash=?", Hash(token)); e != nil {
		return d, "", e
	}
	return d, credential, tx.Commit()
}
func (s *Store) AgentID(token string) (string, error) {
	var id string
	e := s.DB.QueryRow("SELECT id FROM devices WHERE token_hash=? AND revoked=0", Hash(token)).Scan(&id)
	return id, e
}
func (s *Store) Device(id string) (model.Device, error) {
	var d model.Device
	var raw, seen string
	var rev bool
	e := s.DB.QueryRow("SELECT body,last_seen,revoked FROM devices WHERE id=?", id).Scan(&raw, &seen, &rev)
	if e != nil {
		return d, e
	}
	if e = json.Unmarshal([]byte(raw), &d); e != nil {
		return d, e
	}
	d.LastSeen, _ = time.Parse(time.RFC3339Nano, seen)
	d.Revoked = rev
	d.Online = !rev && time.Since(d.LastSeen) < 45*time.Second
	var m string
	if s.DB.QueryRow("SELECT body FROM metrics WHERE device_id=? ORDER BY at DESC LIMIT 1", id).Scan(&m) == nil {
		json.Unmarshal([]byte(m), &d.Latest)
	}
	return d, nil
}
func (s *Store) Devices() ([]model.Device, error) {
	// Heartbeats change last_seen constantly; keep every device list in enrollment order.
	rows, e := s.DB.Query("SELECT id FROM devices ORDER BY json_extract(body, '$.created_at') ASC, id ASC")
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
	out := []model.Device{}
	for _, id := range ids {
		d, e := s.Device(id)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (s *Store) SaveDevice(d model.Device) error {
	d.Latest = nil
	_, e := s.DB.Exec("INSERT INTO devices(id,body,last_seen) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,last_seen=excluded.last_seen", d.ID, encode(d), stamp(d.LastSeen))
	return e
}
func (s *Store) UpdateDevice(id string, update func(*model.Device)) (model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.Device(id)
	if err != nil {
		return d, err
	}
	update(&d)
	return d, s.SaveDevice(d)
}
func (s *Store) Heartbeat(id string, received model.Device, sample model.Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, e := s.Device(id)
	if e != nil {
		return e
	}
	received.ID = id
	received.Name = old.Name
	received.Group = old.Group
	received.Kind = "agent"
	received.CreatedAt = old.CreatedAt
	received.ManagementURL = old.ManagementURL
	received.Revoked = old.Revoked
	received.LastSeen = time.Now().UTC()
	received.Latest = nil
	if received.Addresses == nil {
		received.Addresses = []model.Address{}
	}
	if received.Capabilities == nil {
		received.Capabilities = map[string]model.Capability{}
	}
	if sample.GPUs == nil {
		sample.GPUs = []model.GPU{}
	}
	if sample.Disks == nil {
		sample.Disks = []model.Disk{}
	}
	if sample.Errors == nil {
		sample.Errors = map[string]string{}
	}
	// The hub clock governs freshness; agent timestamps are never trusted for liveness.
	sample.At = received.LastSeen
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("UPDATE devices SET body=?,last_seen=? WHERE id=? AND revoked=0", encode(received), stamp(received.LastSeen), id); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT OR REPLACE INTO metrics VALUES(?,?,?)", id, stamp(sample.At), encode(sample)); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.DB.Exec("UPDATE devices SET revoked=1 WHERE id=?", id)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("UPDATE targets SET state='cancelled',body=json_set(body,'$.state','cancelled','$.reason','设备凭据已撤销') WHERE device_id=? AND state='queued'", id)
	return e
}
func (s *Store) Metrics(id string) ([]model.Sample, error) {
	// Downsample timestamps in SQLite before loading JSON, keeping a week of large
	// GPU/disk samples from being decoded on every dashboard refresh.
	rows, e := s.DB.Query(`WITH ordered AS (
 SELECT at,row_number() OVER (ORDER BY at) AS n,count(*) OVER () AS total
 FROM metrics WHERE device_id=? AND at>=?
 ), selected AS (SELECT at FROM ordered WHERE (n-1)%MAX(1,(total+719)/720)=0 OR n=total)
 SELECT m.body FROM metrics m JOIN selected s ON m.at=s.at WHERE m.device_id=? ORDER BY m.at`, id, stamp(time.Now().Add(-7*24*time.Hour)), id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Sample{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var m model.Sample
		if e = json.Unmarshal([]byte(raw), &m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SaveInventory(id string, v model.Inventory) error {
	_, e := s.DB.Exec("INSERT INTO inventories VALUES(?,?) ON CONFLICT(device_id) DO UPDATE SET body=excluded.body", id, encode(v))
	return e
}
func (s *Store) Inventory(id string) model.Inventory {
	var raw string
	out := model.Inventory{Packages: []model.Package{}}
	if s.DB.QueryRow("SELECT body FROM inventories WHERE device_id=?", id).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &out)
	}
	return out
}
func (s *Store) SaveProject(p model.Project) (model.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := map[string]string{}
	if p.ID != "" {
		old, e := s.Project(p.ID, true)
		if e != nil {
			return p, e
		}
		p.Version = old.Version + 1
		values = old.SecretEnv
	} else {
		p.ID = ID()
		p.Version = 1
	}
	for k, v := range p.SecretEnv {
		if v == "" {
			delete(values, k)
		} else {
			values[k] = v
		}
	}
	encrypted, e := s.Encrypt(values)
	if e != nil {
		return p, e
	}
	p.SecretEnv = nil
	p.SecretKeys = []string{}
	for k := range values {
		p.SecretKeys = append(p.SecretKeys, k)
	}
	p.UpdatedAt = time.Now().UTC()
	_, e = s.DB.Exec("INSERT INTO projects VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,secrets=excluded.secrets", p.ID, encode(p), encrypted)
	return p, e
}
func (s *Store) Project(id string, secrets bool) (model.Project, error) {
	var p model.Project
	var raw, enc string
	e := s.DB.QueryRow("SELECT body,secrets FROM projects WHERE id=?", id).Scan(&raw, &enc)
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal([]byte(raw), &p); e != nil {
		return p, e
	}
	p.EncryptedSecrets = enc
	if secrets {
		p.SecretEnv, e = s.Decrypt(enc)
	}
	return p, e
}
func (s *Store) Projects() ([]model.Project, error) {
	rows, e := s.DB.Query("SELECT body FROM projects ORDER BY json_extract(body,'$.name')")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Project{}
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		var p model.Project
		json.Unmarshal([]byte(raw), &p)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Cleanup() error {
	_, e := s.DB.Exec("DELETE FROM metrics WHERE at<?", stamp(time.Now().Add(-7*24*time.Hour)))
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("DELETE FROM sessions WHERE expires<?; DELETE FROM enrollment WHERE expires<?", stamp(time.Now()), stamp(time.Now()))
	return e
}
func (s *Store) Backup(path string) error {
	if strings.Contains(path, "'") {
		return errors.New("invalid backup path")
	}
	_, e := s.DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", path))
	return e
}
