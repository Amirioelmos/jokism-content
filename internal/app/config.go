package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Settings struct {
	Topic           string `json:"topic"`
	IntervalMinutes int    `json:"interval_minutes"`
	WorkerCount     int    `json:"worker_count"`
	ChannelID       string `json:"channel_id"`
	ChannelLink     string `json:"channel_link"`
	Enabled         bool   `json:"enabled"`
	MaxVideoMB      int64  `json:"max_video_mb"`
	BatchCount      int    `json:"batch_count"`
}

func defaults() Settings {
	return Settings{
		Topic:           "",
		IntervalMinutes: envInt("INTERVAL_MINUTES", 15),
		WorkerCount:     envInt("WORKER_COUNT", 2),
		ChannelID:       strings.TrimSpace(os.Getenv("BALE_CHANNEL_ID")),
		ChannelLink:     strings.TrimSpace(os.Getenv("BALE_CHANNEL_LINK")),
		Enabled:         true,
		MaxVideoMB:      int64(envInt("MAX_VIDEO_MB", 45)),
		BatchCount:      envInt("BATCH_COUNT", 6),
	}
}

func (s Settings) validate() error {
	if s.IntervalMinutes < 1 || s.IntervalMinutes > 1440 { return errors.New("فاصله ارسال باید بین ۱ تا ۱۴۴۰ دقیقه باشد") }
	if s.WorkerCount < 1 || s.WorkerCount > 20 { return errors.New("تعداد ورکر باید بین ۱ تا ۲۰ باشد") }
	if strings.TrimSpace(s.ChannelID) == "" { return errors.New("شناسه کانال الزامی است") }
	if s.MaxVideoMB < 1 || s.MaxVideoMB > 2000 { return errors.New("حداکثر حجم نامعتبر است") }
	if s.BatchCount < 1 || s.BatchCount > 20 { return errors.New("تعداد ویدیو در هر نوبت باید بین ۱ تا ۲۰ باشد") }
	return nil
}

type Store struct { mu sync.RWMutex; path string; db *sql.DB; Settings Settings `json:"settings"`; Sent map[string]time.Time `json:"sent"` }

func loadStore(path string) (*Store, error) {
	s := &Store{path: path, Settings: defaults(), Sent: map[string]time.Time{}}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) { return nil, err }
	if len(b) > 0 && json.Unmarshal(b, s) != nil { return nil, errors.New("فایل داده خراب است") }
	if s.Sent == nil { s.Sent = map[string]time.Time{} }
	return s, nil
}
func openDB(ctx context.Context, dsn string) (*sql.DB,error) {
	db,e:=sql.Open("pgx",dsn);if e!=nil{return nil,e}
	db.SetMaxOpenConns(envInt("DB_MAX_OPEN_CONNS",10))
	db.SetMaxIdleConns(envInt("DB_MAX_IDLE_CONNS",5))
	db.SetConnMaxLifetime(time.Hour)
	if e=db.PingContext(ctx);e!=nil{_ = db.Close();return nil,e}
	_,e=db.ExecContext(ctx,`CREATE TABLE IF NOT EXISTS sent_videos (
		id text PRIMARY KEY,
		sent_at timestamptz NOT NULL DEFAULT now()
	)`)
	if e!=nil{_ = db.Close();return nil,e}
	return db,nil
}
func (s *Store) attachDB(db *sql.DB){s.mu.Lock();defer s.mu.Unlock();s.db=db}
func (s *Store) close() error{s.mu.RLock();db:=s.db;s.mu.RUnlock();if db!=nil{return db.Close()};return nil}
func (s *Store) get() Settings { s.mu.RLock(); defer s.mu.RUnlock(); return s.Settings }
func (s *Store) update(v Settings) error { if err:=v.validate(); err!=nil{return err}; s.mu.Lock(); defer s.mu.Unlock(); s.Settings=v; return s.saveLocked() }
func (s *Store) wasSent(id string) bool { s.mu.RLock();db:=s.db;if db==nil{_,ok:=s.Sent[id];s.mu.RUnlock();return ok};s.mu.RUnlock();var exists bool;_ = db.QueryRowContext(context.Background(),"SELECT EXISTS (SELECT 1 FROM sent_videos WHERE id=$1)",id).Scan(&exists);return exists }
func (s *Store) markSent(id string) error { s.mu.RLock();db:=s.db;s.mu.RUnlock();if db!=nil{_,e:=db.ExecContext(context.Background(),"INSERT INTO sent_videos (id) VALUES ($1) ON CONFLICT (id) DO NOTHING",id);return e};s.mu.Lock();defer s.mu.Unlock();s.Sent[id]=time.Now();return s.saveLocked() }
func (s *Store) saveLocked() error { b,e:=json.MarshalIndent(s,"","  "); if e!=nil{return e}; tmp:=s.path+".tmp"; if e=os.WriteFile(tmp,b,0600);e!=nil{return e}; return os.Rename(tmp,s.path) }
func envInt(name string, fallback int) int { if n,e:=strconv.Atoi(os.Getenv(name));e==nil{return n};return fallback }
