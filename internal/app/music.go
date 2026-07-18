package app

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type Track struct {
	ID, Title, Artist, Song, Genre, PageURL, AudioURL, CoverURL string
}

type MusicFa struct {
	client *http.Client
	base   string
}

var (
	musicArticleRE = regexp.MustCompile(`(?s)<article[^>]+class="[^"]*mf_pst[^"]*"[^>]*>.*?</article>`)
	attrRE         = regexp.MustCompile(`\s([a-zA-Z0-9_-]+)=['"]([^'"]*)['"]`)
	linkRE         = regexp.MustCompile(`href=["']([^"']+)["']`)
	audioRE        = regexp.MustCompile(`data-song=["']([^"']+\.mp3[^"']*)["']`)
	tagRE          = regexp.MustCompile(`<a[^>]+rel=["']tag["'][^>]*>([^<]+)</a>`)
)

func (m *MusicFa) Latest(ctx context.Context, limit int) ([]Track, error) {
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

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u200c", " ")
	return strings.Join(strings.Fields(s), " ")
}
