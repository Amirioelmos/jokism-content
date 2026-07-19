package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type Track struct {
	ID, Title, Artist, Song, Genre, PageURL, AudioURL, CoverURL string
}

type MusicSource interface {
	Latest(context.Context, int) ([]Track, error)
}

type MusicClient struct {
	client *http.Client
	base   string
	pick   func(int) int
}

var (
	musicArticleRE = regexp.MustCompile(`(?s)<article[^>]+class="[^"]*mf_pst[^"]*"[^>]*>.*?</article>`)
	attrRE         = regexp.MustCompile(`\s([a-zA-Z0-9_-]+)=['"]([^'"]*)['"]`)
	linkRE         = regexp.MustCompile(`href=["']([^"']+)["']`)
	audioRE        = regexp.MustCompile(`data-song=["']([^"']+\.mp3[^"']*)["']`)
	tagRE          = regexp.MustCompile(`<a[^>]+rel=["']tag["'][^>]*>([^<]+)</a>`)

	rjMP3LinkRE   = regexp.MustCompile(`https?://(?:www\.)?radiojavan\.com/mp3s/mp3/[^"' <]+|/mp3s/mp3/[^"' <]+`)
	rjJSONAudioRE = regexp.MustCompile(`(?i)"(?:audio|mp3|song|url|downloadUrl|download_url)"\s*:\s*"([^"]+\.mp3[^"]*)"`)
	rjOGImageRE   = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:image["'][^>]+content=["']([^"']+)["']`)
	rjOGTitleRE   = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["']([^"']+)["']`)
	rjTitleRE     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func NewMusicSource(client *http.Client) MusicSource {
	provider := strings.ToLower(strings.TrimSpace(env("MUSIC_PROVIDER", "itunes")))
	switch provider {
	case "musics-fa", "musicfa", "musicsfa":
		return &MusicClient{client: client, base: env("MUSIC_BASE_URL", "https://musics-fa.com"), pick: rand.Intn}
	case "radiojavan", "radio-javan", "rj":
		return &MusicClient{client: client, base: env("MUSIC_BASE_URL", "https://www.radiojavan.com"), pick: rand.Intn}
	case "iraniandj", "iranian-dj":
		return &MusicClient{client: client, base: env("MUSIC_BASE_URL", "https://pro.iraniandj.ir"), pick: rand.Intn}
	default:
		return &MusicClient{client: client, base: env("MUSIC_BASE_URL", "https://api.itunes.ir"), pick: rand.Intn}
	}
}

func (m *MusicClient) Latest(ctx context.Context, limit int) ([]Track, error) {
	provider := strings.ToLower(strings.TrimSpace(env("MUSIC_PROVIDER", "itunes")))
	switch provider {
	case "musics-fa", "musicfa", "musicsfa":
		return m.latestMusicFa(ctx, limit)
	case "radiojavan", "radio-javan", "rj":
		return m.latestRadioJavan(ctx, limit)
	case "iraniandj", "iranian-dj":
		return m.latestIranianDJ(ctx, limit)
	default:
		return m.latestITunes(ctx, limit)
	}
}

func (m *MusicClient) latestMusicFa(ctx context.Context, limit int) ([]Track, error) {
	u := env("MUSIC_SOURCE_URL", m.base+"/download-songs/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "JokismBot/1.0")
	res, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("موزیکفا HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	out := make([]Track, 0, limit)
	for _, article := range musicArticleRE.FindAllString(string(b), -1) {
		t := parseTrack(article)
		if t.AudioURL == "" || t.ID == "" {
			continue
		}
		out = append(out, t)
		if len(out) >= limit {
			return out, nil
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("آهنگ قابل ارسال در موزیکفا پیدا نشد")
	}
	return out, nil
}

type wpPost struct {
	ID      int                       `json:"id"`
	Link    string                    `json:"link"`
	Title   struct{ Rendered string } `json:"title"`
	Content struct{ Rendered string } `json:"content"`
	Yoast   struct {
		OGImage []struct {
			URL string `json:"url"`
		} `json:"og_image"`
	} `json:"yoast_head_json"`
}

type itunesTrackList struct {
	Count   int           `json:"count"`
	Results []itunesTrack `json:"results"`
}

type itunesTrack struct {
	ID         int            `json:"id"`
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	SlugURL    string         `json:"slug_url"`
	Cover      string         `json:"cover"`
	CoverThumb string         `json:"cover_thumb"`
	CoverList  []string       `json:"cover_list"`
	AudioHQ    string         `json:"audio_hq"`
	AudioLQ    string         `json:"audio_lq"`
	Singers    []itunesSinger `json:"singers"`
}

type itunesSinger struct {
	Name   string `json:"name"`
	Family string `json:"family"`
	SlugFA string `json:"slug_fa"`
}

func (m *MusicClient) latestITunes(ctx context.Context, limit int) ([]Track, error) {
	u := env("MUSIC_SOURCE_URL", m.base+"/v1/cnt/track/?page_size=20")
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Del("genre")
	if limit > 0 {
		query.Set("page_size", fmt.Sprintf("%d", limit))
	}
	parsed.RawQuery = query.Encode()
	body, err := m.get(ctx, parsed.String(), 24<<20)
	if err != nil {
		return nil, err
	}
	var list itunesTrackList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 20
	}
	pageCount := (list.Count + pageSize - 1) / pageSize
	if pageCount > 1 {
		pick := m.pick
		if pick == nil {
			pick = rand.Intn
		}
		// Page one was only used to discover pagination. Pull music from one
		// of the other public track-list pages, without applying a category.
		query.Set("page", fmt.Sprintf("%d", 2+pick(pageCount-1)))
		parsed.RawQuery = query.Encode()
		body, err = m.get(ctx, parsed.String(), 24<<20)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, err
		}
	}
	out := make([]Track, 0, limit)
	for _, item := range list.Results {
		t := parseITunesTrack(item)
		if t.AudioURL == "" {
			continue
		}
		out = append(out, t)
		if len(out) >= limit {
			return out, nil
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("آهنگ قابل ارسال در iTunes.ir پیدا نشد")
	}
	return out, nil
}

func (m *MusicClient) latestIranianDJ(ctx context.Context, limit int) ([]Track, error) {
	u := env("MUSIC_SOURCE_URL", m.base+"/wp-json/wp/v2/posts?per_page=20")
	body, err := m.get(ctx, u, 24<<20)
	if err != nil {
		return nil, err
	}
	var posts []wpPost
	if err := json.Unmarshal(body, &posts); err != nil {
		return nil, err
	}
	out := make([]Track, 0, limit)
	for _, post := range posts {
		for _, track := range parseIranianDJPost(post) {
			if track.AudioURL == "" {
				continue
			}
			out = append(out, track)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("آهنگ قابل ارسال در IranianDJ پیدا نشد")
	}
	return out, nil
}

func (m *MusicClient) latestRadioJavan(ctx context.Context, limit int) ([]Track, error) {
	u := env("MUSIC_SOURCE_URL", m.base+"/mp3s/browse/latest")
	body, err := m.get(ctx, u, 8<<20)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	pageLinks := make([]string, 0, limit*3)
	for _, raw := range rjMP3LinkRE.FindAllString(string(body), -1) {
		link := absoluteURL(m.base, cleanText(raw))
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		pageLinks = append(pageLinks, link)
		if len(pageLinks) >= limit*3 {
			break
		}
	}
	if len(pageLinks) == 0 {
		return nil, fmt.Errorf("آهنگ قابل ارسال در رادیوجوان پیدا نشد")
	}
	out := make([]Track, 0, limit)
	for _, link := range pageLinks {
		page, err := m.get(ctx, link, 8<<20)
		if err != nil {
			continue
		}
		t := parseRadioJavanTrack(link, string(page))
		if t.ID == "" {
			t.ID = stableID(link)
		}
		if t.AudioURL == "" {
			continue
		}
		out = append(out, t)
		if len(out) >= limit {
			return out, nil
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("لینک mp3 قابل ارسال در صفحه‌های رادیوجوان پیدا نشد")
	}
	return out, nil
}

func (m *MusicClient) get(ctx context.Context, link string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; JokismBot/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json")
	res, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("رادیوجوان HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, max))
}

func parseTrack(article string) Track {
	var t Track
	for _, m := range attrRE.FindAllStringSubmatch(article, -1) {
		switch m[1] {
		case "data-id":
			t.ID = cleanText(m[2])
		case "data-artist":
			t.Artist = cleanText(m[2])
		case "data-song":
			t.Song = cleanText(m[2])
		case "data-cover":
			t.CoverURL = cleanText(m[2])
		}
	}
	if m := audioRE.FindStringSubmatch(article); len(m) > 1 {
		t.AudioURL = cleanText(m[1])
	}
	if m := linkRE.FindStringSubmatch(article); len(m) > 1 {
		t.PageURL = cleanText(m[1])
	}
	if m := tagRE.FindStringSubmatch(article); len(m) > 1 {
		t.Genre = cleanText(m[1])
	}
	t.Title = strings.TrimSpace(strings.Join([]string{t.Artist, t.Song}, " - "))
	if t.Title == "-" {
		t.Title = ""
	}
	return t
}

func parseRadioJavanTrack(pageURL, page string) Track {
	var t Track
	t.PageURL = pageURL
	if m := rjJSONAudioRE.FindStringSubmatch(page); len(m) > 1 {
		t.AudioURL = unescapeURL(m[1])
	}
	if m := rjOGImageRE.FindStringSubmatch(page); len(m) > 1 {
		t.CoverURL = unescapeURL(m[1])
	}
	title := ""
	if m := rjOGTitleRE.FindStringSubmatch(page); len(m) > 1 {
		title = cleanText(stripRadioJavanSuffix(m[1]))
	}
	if title == "" {
		if m := rjTitleRE.FindStringSubmatch(page); len(m) > 1 {
			title = cleanText(stripRadioJavanSuffix(m[1]))
		}
	}
	if title == "" {
		title = strings.TrimPrefix(pathBase(pageURL), "mp3/")
		title = strings.ReplaceAll(title, "-", " ")
		title = cleanText(title)
	}
	t.Title = title
	t.Artist, t.Song = splitArtistSong(title)
	t.ID = stableID(firstNonEmpty(t.AudioURL, pageURL))
	return t
}

func parseITunesTrack(item itunesTrack) Track {
	artist := itunesArtist(item.Singers)
	audio := firstNonEmpty(item.AudioHQ, item.AudioLQ)
	cover := firstNonEmpty(item.Cover, item.CoverThumb)
	for _, c := range item.CoverList {
		cover = firstNonEmpty(cover, c)
	}
	pageURL := ""
	if item.ID > 0 {
		pageURL = fmt.Sprintf("https://itunes.ir/track/%d/%s/", item.ID, item.SlugURL)
	}
	return Track{
		ID:       fmt.Sprintf("itunes:%d", item.ID),
		Title:    strings.TrimSpace(strings.Join([]string{artist, item.Title}, " - ")),
		Artist:   artist,
		Song:     cleanText(item.Title),
		PageURL:  pageURL,
		AudioURL: cleanText(audio),
		CoverURL: cleanText(cover),
	}
}

func itunesArtist(singers []itunesSinger) string {
	parts := make([]string, 0, len(singers))
	for _, s := range singers {
		name := cleanText(strings.TrimSpace(s.Name + " " + s.Family))
		if name == "" {
			name = cleanText(s.SlugFA)
		}
		if name != "" {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, "، ")
}

func parseIranianDJPost(post wpPost) []Track {
	page := post.Content.Rendered
	title := cleanText(stripTags(post.Title.Rendered))
	artist := extractLabelValue(page, "Artist")
	song := firstNonEmpty(extractLabelValue(page, "Track"), extractLabelValue(page, "Album"), title)
	cover := firstNonEmpty(extractAttr(page, "data-albumArt"), extractAttr(page, "artwork"))
	if cover == "" && len(post.Yoast.OGImage) > 0 {
		cover = post.Yoast.OGImage[0].URL
	}
	audioLinks := uniqueStrings(append(extractAttrs(page, "data-audiopath"), extractMP3Links(page)...))
	out := make([]Track, 0, len(audioLinks))
	for i, audio := range audioLinks {
		trackSong := song
		if len(audioLinks) > 1 {
			trackTitle := cleanText(extractTrackTitleNearAudio(page, audio))
			if trackTitle != "" && trackTitle != "-" {
				trackSong = song + " - " + trackTitle
			}
		}
		out = append(out, Track{
			ID:       stableID(firstNonEmpty(audio, post.Link)),
			Title:    strings.TrimSpace(strings.Join([]string{artist, trackSong}, " - ")),
			Artist:   artist,
			Song:     strings.TrimSpace(trackSong),
			PageURL:  post.Link,
			AudioURL: unescapeURL(audio),
			CoverURL: unescapeURL(cover),
		})
		if out[i].Title == "-" {
			out[i].Title = ""
		}
	}
	return out
}

func stripRadioJavanSuffix(s string) string {
	s = strings.ReplaceAll(s, "Radio Javan", "")
	s = strings.Trim(s, " -|")
	return s
}

func splitArtistSong(title string) (string, string) {
	for _, sep := range []string{" - ", " – ", " — ", " by "} {
		if parts := strings.SplitN(title, sep, 2); len(parts) == 2 {
			return cleanText(parts[0]), cleanText(parts[1])
		}
	}
	return "", cleanText(title)
}

func absoluteURL(base, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		return u.String()
	}
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return b.ResolveReference(u).String()
}

func unescapeURL(s string) string {
	s = strings.ReplaceAll(s, `\/`, `/`)
	return cleanText(s)
}

func stableID(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func pathBase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.Trim(strings.TrimPrefix(u.Path, "/mp3s/"), "/")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func extractLabelValue(page, label string) string {
	re := regexp.MustCompile(`(?is)` + regexp.QuoteMeta(label) + `\s*:\s*([^<\r\n]+)`)
	if m := re.FindStringSubmatch(page); len(m) > 1 {
		return cleanText(stripTags(m[1]))
	}
	return ""
}

func extractAttr(page, name string) string {
	if vals := extractAttrs(page, name); len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func extractAttrs(page, name string) []string {
	re := regexp.MustCompile(regexp.QuoteMeta(name) + `=["']([^"']+)["']`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		out = append(out, cleanText(m[1]))
	}
	return uniqueStrings(out)
}

func extractMP3Links(page string) []string {
	re := regexp.MustCompile(`https?:\\?/\\?/[^"' <]+\.mp3`)
	var out []string
	for _, raw := range re.FindAllString(page, -1) {
		out = append(out, unescapeURL(raw))
	}
	return uniqueStrings(out)
}

func extractTrackTitleNearAudio(page, audio string) string {
	idx := strings.Index(page, audio)
	if idx < 0 {
		idx = strings.Index(page, strings.ReplaceAll(audio, "/", `\/`))
	}
	if idx < 0 {
		return ""
	}
	end := idx + 1200
	if end > len(page) {
		end = len(page)
	}
	return extractAttr(page[idx:end], "data-trackTitle")
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func stripTags(s string) string {
	re := regexp.MustCompile(`(?s)<[^>]+>`)
	return re.ReplaceAllString(s, " ")
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u200c", " ")
	return strings.Join(strings.Fields(s), " ")
}
