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
type MusicJob struct{ Track Track }
type Stats struct {
	Queued    atomic.Int64
	Sent      atomic.Int64
	Failed    atomic.Int64
	LastError atomic.Value
}
type App struct {
	store        *Store
	aparat       *Aparat
	music        MusicSource
	client       *http.Client
	bale         *Bale
	jobs         chan Job
	musicJobs    chan MusicJob
	stats        Stats
	server       *http.Server
	root         context.Context
	cancel       context.CancelFunc
	workersMu    sync.Mutex
	workerCancel context.CancelFunc
	musicRetryMu sync.Mutex
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
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = time.Duration(envInt("HTTP_TLS_HANDSHAKE_SECONDS", 60)) * time.Second
	client := &http.Client{Transport: transport, Timeout: 10 * time.Minute}
	a := &App{store: s, jobs: make(chan Job, 100), musicJobs: make(chan MusicJob, 100), root: ctx, cancel: cancel, captions: captions, client: client}
	a.aparat = &Aparat{client: client, base: env("APARAT_BASE_URL", "https://www.aparat.com")}
	a.music = NewMusicSource(client)
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
	go a.musicScheduler(ctx)
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
		go a.musicWorker(ctx, i+1)
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

func (a *App) musicScheduler(ctx context.Context) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.enqueueMusicUntilQueued(ctx)
			timer.Reset(time.Duration(a.store.get().MusicInterval) * time.Minute)
		}
	}
}

func (a *App) enqueue(ctx context.Context) {
	s := a.store.get()
	if !s.Enabled {
		return
	}
	fetchLimit := s.BatchCount * 4
	if fetchLimit < 20 {
		fetchLimit = 20
	}
	vs, e := a.aparat.Shorts(ctx, fetchLimit)
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

func (a *App) enqueueMusic(ctx context.Context) bool {
	s := a.store.get()
	if !s.Enabled || strings.TrimSpace(s.MusicChannelID) == "" {
		return false
	}
	// Always inspect enough source items to move past tracks that were sent in
	// earlier runs. With a batch size of one, limiting the source to four made
	// the scheduler permanently stall after those first four tracks.
	fetchLimit := s.MusicBatchCount * 4
	if fetchLimit < 20 {
		fetchLimit = 20
	}
	tracks, e := a.music.Latest(ctx, fetchLimit)
	if e != nil {
		a.fail(e)
		return false
	}
	queued := 0
	for _, track := range tracks {
		if !a.store.wasSent("music:" + track.ID) {
			select {
			case a.musicJobs <- MusicJob{track}:
				a.stats.Queued.Add(1)
				queued++
				if queued >= s.MusicBatchCount {
					return true
				}
			default:
				a.fail(errors.New("صف موزیک پر است"))
				return false
			}
		}
	}
	return queued > 0
}

func (a *App) enqueueMusicUntilQueued(ctx context.Context) {
	a.musicRetryMu.Lock()
	defer a.musicRetryMu.Unlock()

	retryDelay := time.Duration(envInt("MUSIC_RETRY_SECONDS", 10)) * time.Second
	for {
		s := a.store.get()
		if !s.Enabled || strings.TrimSpace(s.MusicChannelID) == "" {
			return
		}
		if a.enqueueMusic(ctx) {
			return
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
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

func (a *App) musicWorker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-a.musicJobs:
			a.processMusic(ctx, j)
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
			// A 413 is permanent for this video. Remember it and immediately
			// refill a single-item batch so the worker keeps trying until one
			// candidate is accepted by Bale.
			if a.skip(v.ID, e) && s.BatchCount == 1 {
				a.enqueue(ctx)
			}
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

func (a *App) processMusic(ctx context.Context, j MusicJob) {
	id := "music:" + j.Track.ID
	if a.store.wasSent(id) {
		return
	}
	s := a.store.get()
	retry := func() {
		if s.MusicBatchCount == 1 {
			a.enqueueMusicUntilQueued(ctx)
		}
	}
	maxBytes := s.MaxAudioMB << 20
	if maxBytes > 0 {
		size, err := contentLength(ctx, a.client, j.Track.AudioURL)
		if err != nil {
			// Some iTunes CDN hosts intermittently fail or reject HEAD while the
			// actual audio URL remains downloadable. Let Bale try the URL; a real
			// size violation is still handled by its 413 response below.
			log.Printf("هشدار: حجم موزیک قابل بررسی نبود: %v", err)
		}
		if err == nil && size > maxBytes {
			a.skip(id, errors.New("حجم موزیک بیشتر از سقف تنظیم‌شده است"))
			retry()
			return
		}
	}
	if cover := strings.TrimSpace(j.Track.CoverURL); cover != "" {
		if e := a.bale.SendPhotoURL(ctx, s.MusicChannelID, cover, musicCaption(j.Track, s)); e != nil {
			var be *BaleError
			if errors.As(e, &be) && be.StatusCode == http.StatusRequestEntityTooLarge {
				a.skip(id, e)
				retry()
				return
			}
			a.fail(e)
			retry()
			return
		}
	}
	if e := a.bale.SendAudioURL(ctx, s.MusicChannelID, j.Track.AudioURL, musicAudioCaption(s), musicTitle(j.Track), "Jokism Music", j.Track.CoverURL); e != nil {
		var be *BaleError
		if errors.As(e, &be) && be.StatusCode == http.StatusRequestEntityTooLarge {
			a.skip(id, e)
			retry()
			return
		}
		a.fail(e)
		retry()
		return
	}
	if e := a.store.markSent(id); e != nil {
		a.fail(e)
		return
	}
	a.stats.Sent.Add(1)
}

func (a *App) skip(id string, reason error) bool {
	log.Printf("رد شد: %s: %v", id, reason)
	if e := a.store.markSent(id); e != nil {
		a.fail(e)
		return false
	}
	return true
}

func contentLength(ctx context.Context, client *http.Client, link string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, link, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("User-Agent", "JokismBot/1.0")
	res, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusMethodNotAllowed || res.StatusCode == http.StatusNotImplemented {
		return -1, nil
	}
	if res.StatusCode/100 != 2 {
		return -1, errors.New("بررسی حجم فایل ناموفق بود")
	}
	return res.ContentLength, nil
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

func musicTitle(t Track) string {
	artist := strings.TrimSpace(t.Artist)
	if artist == "" {
		artist = "Jokism"
	}
	return artist + " - Jokism Music"
}

func musicCaption(t Track, s Settings) string {
	artist := valueOrDash(t.Artist)
	song := valueOrDash(t.Song)
	parts := []string{
		"اسم خواننده: " + artist,
		"اسم آهنگ: " + song,
	}
	if tag := strings.TrimSpace(s.MusicChannelTag); tag != "" {
		parts = append(parts, tag)
	}
	return strings.Join(parts, "\n")
}

func musicAudioCaption(s Settings) string {
	return strings.TrimSpace(s.MusicChannelTag)
}

func valueOrDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}
