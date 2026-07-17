package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Job struct{ Video Video }
type Stats struct {
	Queued    atomic.Int64
	Sent      atomic.Int64
	Failed    atomic.Int64
	LastError atomic.Value
}
type App struct {
	store        *Store
	aparat       *Aparat
	bale         *Bale
	jobs         chan Job
	stats        Stats
	server       *http.Server
	root         context.Context
	cancel       context.CancelFunc
	workersMu    sync.Mutex
	workerCancel context.CancelFunc
	captions     []string
}

func New() (*App, error) {
	token := strings.TrimSpace(os.Getenv("BALE_BOT_TOKEN"))
	if token == "" {
		return nil, errors.New("متغیر BALE_BOT_TOKEN تنظیم نشده است")
	}
	path := os.Getenv("DATA_FILE")
	if path == "" {
		path = "data.json"
	}
	s, e := loadStore(path)
	if e != nil {
		return nil, e
	}
	captions, e := loadCaptions(env("CAPTIONS_FILE", "captions.txt"))
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_URL")); dsn != "" {
		db, e := openDB(ctx, dsn)
		if e != nil {
			cancel()
			return nil, e
		}
		s.attachDB(db)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	a := &App{store: s, jobs: make(chan Job, 100), root: ctx, cancel: cancel, captions: captions}
	a.aparat = &Aparat{client: client, base: env("APARAT_BASE_URL", "https://www.aparat.com")}
	a.bale = &Bale{client: client, base: env("BALE_API_BASE", "https://tapi.bale.ai"), token: token}
	a.stats.LastError.Store("")
	a.server = &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	return a, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return strings.TrimRight(v, "/")
	}
	return d
}
func (a *App) Context() context.Context { return a.root }
func (a *App) Run(ctx context.Context) error {
	defer a.store.close()
	a.resizeWorkers()
	go a.scheduler(ctx)
	go func() {
		<-ctx.Done()
		a.cancel()
		c, t := context.WithTimeout(context.Background(), 10*time.Second)
		defer t()
		_ = a.server.Shutdown(c)
	}()
	log.Printf("پنل روی %s آماده است", a.server.Addr)
	e := a.server.ListenAndServe()
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func (a *App) resizeWorkers() {
	a.workersMu.Lock()
	defer a.workersMu.Unlock()
	if a.workerCancel != nil {
		a.workerCancel()
	}
	ctx, cancel := context.WithCancel(a.root)
	a.workerCancel = cancel
	for i := 0; i < a.store.get().WorkerCount; i++ {
		go a.worker(ctx, i+1)
	}
}
func (a *App) scheduler(ctx context.Context) {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.enqueue(ctx)
			timer.Reset(time.Duration(a.store.get().IntervalMinutes) * time.Minute)
		}
	}
}
func (a *App) enqueue(ctx context.Context) {
	s := a.store.get()
	if !s.Enabled {
		return
	}
	vs, e := a.aparat.Shorts(ctx, s.BatchCount*4)
	if e != nil {
		a.fail(e)
		return
	}
	queued := 0
	for _, v := range vs {
		if !a.store.wasSent(v.ID) {
			select {
			case a.jobs <- Job{v}:
				a.stats.Queued.Add(1)
				queued++
				if queued >= s.BatchCount {
					return
				}
			default:
				a.fail(errors.New("صف پر است"))
				return
			}
		}
	}
}
func (a *App) worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-a.jobs:
			a.process(ctx, j)
		}
	}
}
func (a *App) process(ctx context.Context, j Job) {
	if a.store.wasSent(j.Video.ID) {
		return
	}
	v, e := a.aparat.Resolve(ctx, j.Video)
	if e != nil {
		a.fail(e)
		return
	}
	s := a.store.get()
	maxBytes := s.MaxVideoMB << 20
	if maxBytes > 0 {
		size, err := a.aparat.ContentLength(ctx, v)
		if err != nil {
			a.fail(err)
			return
		}
		if size > maxBytes {
			a.skip(v.ID, errors.New("حجم ویدیو بیشتر از سقف تنظیم‌شده است"))
			return
		}
	}
	if e = a.bale.SendVideoURL(ctx, s.ChannelID, v.DownloadURL, a.captionFor(s)); e != nil {
		var be *BaleError
		if errors.As(e, &be) && be.StatusCode == http.StatusRequestEntityTooLarge {
			a.skip(v.ID, e)
			return
		}
		a.fail(e)
		return
	}
	if e = a.store.markSent(v.ID); e != nil {
		a.fail(e)
		return
	}
	a.stats.Sent.Add(1)
}
func (a *App) skip(id string, reason error) {
	log.Printf("رد شد: %s: %v", id, reason)
	if e := a.store.markSent(id); e != nil {
		a.fail(e)
	}
}
func (a *App) fail(e error) {
	log.Printf("خطا: %v", e)
	a.stats.Failed.Add(1)
	a.stats.LastError.Store(e.Error())
}

func loadCaptions(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	return out, scanner.Err()
}

func (a *App) captionFor(s Settings) string {
	channel := strings.TrimSpace(s.ChannelLink)
	if len(a.captions) == 0 {
		return channel
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(a.captions))))
	if err != nil {
		return joinCaption(a.captions[time.Now().UnixNano()%int64(len(a.captions))], channel)
	}
	return joinCaption(a.captions[n.Int64()], channel)
}

func joinCaption(caption, channel string) string {
	caption = strings.TrimSpace(caption)
	if channel == "" {
		return caption
	}
	if caption == "" {
		return channel
	}
	return caption + "\n\n" + channel
}
