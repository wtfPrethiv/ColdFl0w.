package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"coldflow/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "coldflow.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) migrate() error {
	_, err := s.DB.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON; PRAGMA cache_size=-4096;
 CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY,at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS records(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,updated TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE VIRTUAL TABLE IF NOT EXISTS search USING fts5(kind UNINDEXED,id UNINDEXED,body);
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY AUTOINCREMENT,at TEXT NOT NULL,agent TEXT NOT NULL,kind TEXT NOT NULL,text TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,kind TEXT NOT NULL,jobkey TEXT NOT NULL UNIQUE,payload TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'queued',attempts INTEGER NOT NULL DEFAULT 0,due TEXT NOT NULL,lease TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '');
 CREATE INDEX IF NOT EXISTS jobs_ready ON jobs(status,due);
 CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 INSERT OR IGNORE INTO migrations VALUES(1,datetime('now'));`)
	return err
}
func (s *Store) Put(kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,updated=excluded.updated`, kind, id, string(b), domain.Now()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM search WHERE kind=? AND id=?`, kind, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO search VALUES(?,?,?)`, kind, id, string(b)); err != nil {
		return err
	}
	return tx.Commit()
}
func Get[T any](s *Store, kind, id string) (T, error) {
	var v T
	var b string
	err := s.DB.QueryRow(`SELECT data FROM records WHERE kind=? AND id=?`, kind, id).Scan(&b)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(b), &v)
	return v, err
}
func List[T any](s *Store, kind string) ([]T, error) {
	projection := "data"
	if kind == "paper" {
		projection = "json_remove(data,'$.text')"
	}
	rows, err := s.DB.Query(`SELECT `+projection+` FROM records WHERE kind=? ORDER BY updated DESC`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b string
		var v T
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Delete(kind, id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM records WHERE kind=? AND id=?`, kind, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM search WHERE kind=? AND id=?`, kind, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Set(k, v string) error {
	_, err := s.DB.Exec(`INSERT INTO kv VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	return err
}
func (s *Store) Value(k string) string {
	var v string
	_ = s.DB.QueryRow(`SELECT value FROM kv WHERE key=?`, k).Scan(&v)
	return v
}
func (s *Store) Event(agent, kind, msg string) error {
	_, err := s.DB.Exec(`INSERT INTO events(at,agent,kind,text) VALUES(?,?,?,?)`, domain.Now(), agent, kind, msg)
	return err
}
func (s *Store) Events(limit int) ([]domain.Event, error) {
	rows, err := s.DB.Query(`SELECT id,at,agent,kind,text FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		if err = rows.Scan(&e.ID, &e.At, &e.Agent, &e.Kind, &e.Text); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Enqueue(kind, key string, payload any, due time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT OR IGNORE INTO jobs(id,kind,jobkey,payload,due) VALUES(?,?,?,?,?)`, domain.ID(), kind, key, string(b), due.UTC().Format(time.RFC3339))
	return err
}
func (s *Store) Claim() (domain.Job, error) {
	var j domain.Job
	// Queue timestamps use a fixed precision so lexical ordering is chronological.
	now := time.Now().UTC().Format(time.RFC3339)
	err := s.DB.QueryRow(`UPDATE jobs SET status='running',attempts=attempts+1,lease=? WHERE id=(SELECT id FROM jobs WHERE (status='queued' AND due<=?) OR (status='running' AND lease<?) ORDER BY CASE WHEN kind IN ('chat','incoming','inbox','notify') THEN 0 ELSE 1 END,due LIMIT 1) RETURNING id,kind,jobkey,payload,attempts,due`, now, now, time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339)).Scan(&j.ID, &j.Kind, &j.Key, &j.Payload, &j.Attempts, &j.Due)
	return j, err
}
func (s *Store) Finish(j domain.Job, workErr error) error {
	if workErr == nil {
		_, err := s.DB.Exec(`UPDATE jobs SET status='done',error='' WHERE id=?`, j.ID)
		return err
	}
	status := "queued"
	if j.Attempts >= 5 {
		status = "failed"
	}
	delay := time.Duration(1<<min(j.Attempts, 8)) * time.Minute
	_, err := s.DB.Exec(`UPDATE jobs SET status=?,error=?,due=? WHERE id=?`, status, workErr.Error(), time.Now().Add(delay).UTC().Format(time.RFC3339), j.ID)
	return err
}
func (s *Store) Jobs() ([]map[string]any, error) {
	rows, err := s.DB.Query(`SELECT id,kind,status,attempts,error FROM jobs WHERE status!='done' ORDER BY due LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, k, st, e string
		var a int
		if err = rows.Scan(&id, &k, &st, &a, &e); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "kind": k, "status": st, "attempts": a, "error": e})
	}
	return out, rows.Err()
}
func (s *Store) Retry(id string) error {
	_, err := s.DB.Exec(`UPDATE jobs SET status='queued',attempts=0,due=? WHERE id=? AND status='failed'`, domain.Now(), id)
	return err
}

func (s *Store) Defer(j domain.Job, at time.Time) error {
	_, err := s.DB.Exec(`UPDATE jobs SET status='queued',attempts=MAX(attempts-1,0),due=? WHERE id=?`, at.UTC().Format(time.RFC3339), j.ID)
	return err
}
func (s *Store) Search(q string) ([]string, error) {
	rows, err := s.DB.Query(`SELECT body FROM search WHERE search MATCH ? LIMIT 20`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Backup(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup already exists: %s", path)
	}
	_, err := s.DB.Exec(`VACUUM INTO ?`, path)
	return err
}
func (s *Store) Prune() error {
	_, err := s.DB.Exec(`DELETE FROM events WHERE id < (SELECT COALESCE(MAX(id),0)-5000 FROM events);
 DELETE FROM jobs WHERE status='done' AND julianday(due) < julianday('now','-14 days');
 DELETE FROM search WHERE kind='chat' AND id IN (SELECT id FROM records WHERE kind='chat' ORDER BY updated DESC LIMIT -1 OFFSET 1000);
 DELETE FROM records WHERE kind='chat' AND id IN (SELECT id FROM records WHERE kind='chat' ORDER BY updated DESC LIMIT -1 OFFSET 1000);`)
	return err
}
