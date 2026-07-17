package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Video struct {
	ID, Title, PageURL, DownloadURL string
	Hashtags                        []string
}
type Aparat struct {
	client *http.Client
	base   string
}
type aparatVideoRow struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	FileLink    string `json:"file_link"`
	FileLinkAll []struct {
		Profile string   `json:"profile"`
		URLs    []string `json:"urls"`
		URL     string   `json:"url"`
	} `json:"file_link_all"`
}

func (a *Aparat) Search(ctx context.Context, topic string) ([]Video, error) {
	u := a.base + "/etc/api/videoBySearch/text/" + url.PathEscape(topic) + "/perpage/20"
	var raw struct {
		VideoBySearch []struct {
			UID   string `json:"uid"`
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"videobysearch"`
	}
	// Aparat has used both spellings in versions of this public endpoint.
	var envelope map[string]json.RawMessage
	if err := a.getJSON(ctx, u, &envelope); err != nil {
		return nil, err
	}
	b := envelope["videobysearch"]
	if len(b) == 0 {
		b = envelope["videoBySearch"]
	}
	if err := json.Unmarshal(b, &raw.VideoBySearch); err != nil {
		return nil, fmt.Errorf("پاسخ جستجوی آپارات: %w", err)
	}
	out := make([]Video, 0, len(raw.VideoBySearch))
	for _, v := range raw.VideoBySearch {
		if v.UID != "" {
			out = append(out, Video{ID: v.UID, Title: v.Title, PageURL: v.URL})
		}
	}
	return out, nil
}

func (a *Aparat) Shorts(ctx context.Context, count int) ([]Video, error) {
	u := env("APARAT_SHORTS_API_BASE", "https://shorts.aparat.com/api") + "/v1/web/explore"
	var raw struct {
		Data struct {
			Posts []struct {
				Type        string          `json:"type"`
				ID          string          `json:"id"`
				Description string          `json:"description"`
				Hashtags    json.RawMessage `json:"hashtags"`
				User        struct {
					Name     string `json:"name"`
					Username string `json:"username"`
				} `json:"user"`
				Stats struct {
					Views int64 `json:"views"`
					Likes int64 `json:"likes"`
				} `json:"stats"`
				ShortVideos []struct {
					VideoLink string `json:"video_link"`
					HLSLink   string `json:"hls_link"`
				} `json:"short_videos"`
			} `json:"posts"`
		} `json:"data"`
	}
	if err := a.getJSON(ctx, u, &raw); err != nil {
		return nil, err
	}
	sort.SliceStable(raw.Data.Posts, func(i, j int) bool {
		return raw.Data.Posts[i].Stats.Views > raw.Data.Posts[j].Stats.Views
	})
	out := make([]Video, 0, count)
	for _, p := range raw.Data.Posts {
		if p.Type != "short-video" || p.ID == "" || len(p.ShortVideos) == 0 {
			continue
		}
		videoLink := p.ShortVideos[0].VideoLink
		if !strings.HasPrefix(videoLink, "http") && strings.HasPrefix(p.ShortVideos[0].HLSLink, "http") {
			videoLink, _ = a.mp4FromHLS(ctx, p.ShortVideos[0].HLSLink)
		}
		if !strings.HasPrefix(videoLink, "http") {
			continue
		}
		title := strings.TrimSpace(p.Description)
		if title == "" {
			title = "شورتز آپارات"
		}
		if p.User.Name != "" {
			title = title + "\nکانال: " + p.User.Name
		}
		page := "https://www.aparat.com/shorts/" + p.ID
		out = append(out, Video{ID: p.ID, Title: title, PageURL: page, DownloadURL: videoLink, Hashtags: parseHashtags(p.Hashtags)})
		if len(out) >= count {
			return out, nil
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("در feed شورتز آپارات ویدیوی قابل ارسال پیدا نشد")
	}
	return out, nil
}

func (a *Aparat) mp4FromHLS(ctx context.Context, hls string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hls, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "JokismBot/1.0")
	res, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("HLS HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "http") || !strings.Contains(line, ".mp4/chunk.m3u8") {
			continue
		}
		return strings.Replace(line, ".mp4/chunk.m3u8", ".mp4", 1), nil
	}
	return "", fmt.Errorf("لینک mp4 از HLS پیدا نشد")
}

func parseHashtags(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var stringsOnly []string
	if json.Unmarshal(raw, &stringsOnly) == nil {
		return cleanHashtags(stringsOnly)
	}
	var rows []struct {
		Title string `json:"title"`
		Name  string `json:"name"`
		Text  string `json:"text"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		return nil
	}
	tags := make([]string, 0, len(rows))
	for _, r := range rows {
		tag := r.Title
		if tag == "" {
			tag = r.Name
		}
		if tag == "" {
			tag = r.Text
		}
		tags = append(tags, tag)
	}
	return cleanHashtags(tags)
}

func cleanHashtags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.TrimPrefix(tag, "#"))
		if tag != "" {
			out = append(out, "#"+strings.ReplaceAll(tag, " ", "_"))
		}
	}
	return out
}

func (a *Aparat) Resolve(ctx context.Context, v Video) (Video, error) {
	if v.DownloadURL != "" {
		return v, nil
	}
	var data map[string]json.RawMessage
	if err := a.getJSON(ctx, a.base+"/etc/api/video/videohash/"+url.PathEscape(v.ID), &data); err != nil {
		return v, err
	}
	b := data["video"]
	var rows []aparatVideoRow
	if err := json.Unmarshal(b, &rows); err != nil || len(rows) == 0 {
		var row aparatVideoRow
		if err := json.Unmarshal(b, &row); err != nil {
			return v, fmt.Errorf("لینک دانلود برای %s پیدا نشد", v.ID)
		}
		rows = []aparatVideoRow{row}
	}
	if v.Title == "" {
		v.Title = rows[0].Title
	}
	if v.PageURL == "" {
		v.PageURL = rows[0].URL
	}
	for i := len(rows[0].FileLinkAll) - 1; i >= 0; i-- {
		x := rows[0].FileLinkAll[i]
		link := x.URL
		if link == "" && len(x.URLs) > 0 {
			link = x.URLs[0]
		}
		if strings.HasPrefix(link, "http") {
			v.DownloadURL = link
			break
		}
	}
	if v.DownloadURL == "" && strings.HasPrefix(rows[0].FileLink, "http") {
		v.DownloadURL = rows[0].FileLink
	}
	if v.DownloadURL == "" {
		return v, fmt.Errorf("آپارات لینک فایل معتبر برنگرداند")
	}
	return v, nil
}

func (a *Aparat) ContentLength(ctx context.Context, v Video) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, v.DownloadURL, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("User-Agent", "JokismBot/1.0")
	res, err := a.client.Do(req)
	if err != nil {
		return -1, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusMethodNotAllowed || res.StatusCode == http.StatusNotImplemented {
		return -1, nil
	}
	if res.StatusCode/100 != 2 {
		return -1, fmt.Errorf("بررسی حجم ویدیو HTTP %d", res.StatusCode)
	}
	return res.ContentLength, nil
}

func (a *Aparat) getJSON(ctx context.Context, u string, dst any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "JokismBot/1.0")
	res, e := a.client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("آپارات HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(dst)
}
func (a *Aparat) Download(ctx context.Context, v Video, max int64) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, v.DownloadURL, nil)
	res, e := a.client.Do(req)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("دانلود HTTP %d", res.StatusCode)
	}
	if res.ContentLength > max {
		return "", fmt.Errorf("حجم ویدیو بیشتر از سقف تنظیم‌شده است")
	}
	f, e := os.CreateTemp("", "jokism-*.mp4")
	if e != nil {
		return "", e
	}
	path := filepath.Clean(f.Name())
	n, e := io.Copy(f, io.LimitReader(res.Body, max+1))
	ce := f.Close()
	if e != nil || ce != nil || n > max {
		os.Remove(path)
		if n > max {
			return "", fmt.Errorf("حجم ویدیو بیشتر از سقف تنظیم‌شده است")
		}
		if e != nil {
			return "", e
		}
		return "", ce
	}
	return path, nil
}
