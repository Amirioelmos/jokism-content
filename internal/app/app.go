package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)
type Job struct{ Video Video }
type Stats struct{ Queued atomic.Int64; Sent atomic.Int64; Failed atomic.Int64; LastError atomic.Value }
type App struct{ store *Store; aparat *Aparat; bale *Bale; jobs chan Job; stats Stats; server *http.Server; root context.Context; cancel context.CancelFunc; workersMu sync.Mutex; workerCancel context.CancelFunc }
func New()(*App,error){ token:=strings.TrimSpace(os.Getenv("BALE_BOT_TOKEN"));if token==""{return nil,errors.New("متغیر BALE_BOT_TOKEN تنظیم نشده است")};path:=os.Getenv("DATA_FILE");if path==""{path="data.json"};s,e:=loadStore(path);if e!=nil{return nil,e};ctx,cancel:=context.WithCancel(context.Background());if dsn:=strings.TrimSpace(os.Getenv("DATABASE_URL"));dsn!=""{db,e:=openDB(ctx,dsn);if e!=nil{cancel();return nil,e};s.attachDB(db)};client:=&http.Client{Timeout:10*time.Minute};a:=&App{store:s,jobs:make(chan Job,100),root:ctx,cancel:cancel};a.aparat=&Aparat{client:client,base:env("APARAT_BASE_URL","https://www.aparat.com")};a.bale=&Bale{client:client,base:env("BALE_API_BASE","https://tapi.bale.ai"),token:token};a.stats.LastError.Store("");a.server=&http.Server{Addr:env("LISTEN_ADDR",":8080"),Handler:a.routes(),ReadHeaderTimeout:10*time.Second};return a,nil }
func env(k,d string)string{if v:=os.Getenv(k);v!=""{return strings.TrimRight(v,"/")};return d}
func (a *App) Context()context.Context{return a.root}
func (a *App) Run(ctx context.Context)error{ defer a.store.close();a.resizeWorkers();go a.scheduler(ctx);go func(){<-ctx.Done();a.cancel();c,t:=context.WithTimeout(context.Background(),10*time.Second);defer t();_ = a.server.Shutdown(c)}();log.Printf("پنل روی %s آماده است",a.server.Addr);e:=a.server.ListenAndServe();if errors.Is(e,http.ErrServerClosed){return nil};return e }
func (a *App) resizeWorkers(){a.workersMu.Lock();defer a.workersMu.Unlock();if a.workerCancel!=nil{a.workerCancel()};ctx,cancel:=context.WithCancel(a.root);a.workerCancel=cancel;for i:=0;i<a.store.get().WorkerCount;i++{go a.worker(ctx,i+1)} }
func (a *App) scheduler(ctx context.Context){timer:=time.NewTimer(time.Second);defer timer.Stop();for{select{case<-ctx.Done():return;case<-timer.C:a.enqueue(ctx);timer.Reset(time.Duration(a.store.get().IntervalMinutes)*time.Minute)}}}
func (a *App) enqueue(ctx context.Context){s:=a.store.get();if !s.Enabled{return};vs,e:=a.aparat.Shorts(ctx,s.BatchCount);if e!=nil{a.fail(e);return};queued:=0;for _,v:=range vs{if !a.store.wasSent(v.ID){select{case a.jobs<-Job{v}:a.stats.Queued.Add(1);queued++;if queued>=s.BatchCount{return};default:a.fail(errors.New("صف پر است"));return}}}}
func (a *App) worker(ctx context.Context,id int){for{select{case<-ctx.Done():return;case j:=<-a.jobs:a.process(ctx,j)}}}
func (a *App) process(ctx context.Context,j Job){if a.store.wasSent(j.Video.ID){return};v,e:=a.aparat.Resolve(ctx,j.Video);if e!=nil{a.fail(e);return};s:=a.store.get();if e=a.bale.SendVideoURL(ctx,s.ChannelID,v.DownloadURL,captionFor(v,s));e!=nil{a.fail(e);return};if e=a.store.markSent(v.ID);e!=nil{a.fail(e);return};a.stats.Sent.Add(1)}
func (a *App) fail(e error){log.Printf("خطا: %v",e);a.stats.Failed.Add(1);a.stats.LastError.Store(e.Error())}

func captionFor(v Video, s Settings) string {
	parts:=[]string{}
	if strings.TrimSpace(v.Title)!=""{parts=append(parts,strings.TrimSpace(v.Title))}
	if len(v.Hashtags)>0{parts=append(parts,strings.Join(v.Hashtags," "))}
	if strings.TrimSpace(s.ChannelLink)!=""{parts=append(parts,strings.TrimSpace(s.ChannelLink))}
	return strings.Join(parts,"\n\n")
}
