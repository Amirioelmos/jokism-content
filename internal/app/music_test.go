package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type retryMusicSource struct{ calls int }

func (s *retryMusicSource) Latest(context.Context, int) ([]Track, error) {
	s.calls++
	if s.calls == 1 {
		return nil, errors.New("temporary source error")
	}
	return []Track{{ID: "next", AudioURL: "https://cdn.example/next.mp3"}}, nil
}

func TestEnqueueMusicRetriesUntilTrackIsQueued(t *testing.T) {
	t.Setenv("MUSIC_RETRY_SECONDS", "1")
	settings := defaults()
	settings.MusicChannelID = "@music"
	settings.MusicBatchCount = 1
	source := &retryMusicSource{}
	a := &App{
		store:     &Store{Settings: settings, Sent: map[string]time.Time{}},
		music:     source,
		musicJobs: make(chan MusicJob, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	a.enqueueMusicUntilQueued(ctx)

	if source.calls != 2 {
		t.Fatalf("source calls = %d", source.calls)
	}
	select {
	case job := <-a.musicJobs:
		if job.Track.ID != "next" {
			t.Fatalf("queued track = %q", job.Track.ID)
		}
	default:
		t.Fatal("no music job queued")
	}
}

func TestLatestITunesChoosesAnotherPageWithoutCategory(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cnt/track/":
			requests++
			if got := r.URL.Query().Get("genre"); got != "" {
				t.Errorf("genre query = %q", got)
			}
			if got := r.URL.Query().Get("page_size"); got != "20" {
				t.Errorf("page_size = %q", got)
			}
			if requests == 1 {
				_, _ = w.Write([]byte(`{"count":100,"results":[{"id":1,"title":"first","audio_hq":"https://cdn.example/first.mp3"}]}`))
				return
			}
			if got := r.URL.Query().Get("page"); got != "4" {
				t.Errorf("page = %q", got)
			}
			_, _ = w.Write([]byte(`{"count":100,"results":[{"id":10,"title":"آواز","audio_hq":"https://cdn.example/song.mp3"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &MusicClient{client: server.Client(), base: server.URL, pick: func(n int) int {
		if n != 4 {
			t.Fatalf("other page count = %d", n)
		}
		return 2
	}}
	tracks, err := client.latestITunes(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks len = %d", len(tracks))
	}
	if tracks[0].ID != "itunes:10" {
		t.Fatalf("track id = %q", tracks[0].ID)
	}
}

func TestParseRadioJavanTrack(t *testing.T) {
	html := `<html>
<head>
<meta property="og:title" content="Sogand - Tehran Radio Javan">
<meta property="og:image" content="https://assets.rjassets.com/static/mp3/sogand-tehran.jpg">
</head>
<body>
<script>{"downloadUrl":"https:\/\/host2.rj-mw1.com\/media\/mp3\/mp3-320\/Sogand%20-%20Tehran.mp3"}</script>
</body>
</html>`
	track := parseRadioJavanTrack("https://www.radiojavan.com/mp3s/mp3/Sogand-Tehran", html)
	if track.Artist != "Sogand" {
		t.Fatalf("artist = %q", track.Artist)
	}
	if track.Song != "Tehran" {
		t.Fatalf("song = %q", track.Song)
	}
	if track.CoverURL != "https://assets.rjassets.com/static/mp3/sogand-tehran.jpg" {
		t.Fatalf("cover = %q", track.CoverURL)
	}
	if track.AudioURL != "https://host2.rj-mw1.com/media/mp3/mp3-320/Sogand%20-%20Tehran.mp3" {
		t.Fatalf("audio = %q", track.AudioURL)
	}
	if track.ID == "" {
		t.Fatal("id is empty")
	}
}

func TestParseIranianDJPost(t *testing.T) {
	var post wpPost
	post.ID = 30993
	post.Link = "https://pro.iraniandj.ir/albums/some1-message-from-the-deep/"
	post.Title.Rendered = "Some1 &#8211; Message from the Deep"
	post.Content.Rendered = `<p>Artist : Some1</p>
<p>Album : Message from the Deep</p>
<li data-audiopath="https://dl.iraniandj.ir/2025/10/18/1.mp3"
    data-albumArt="http://pro.iraniandj.ir/wp-content/uploads/2026/06/Some1-Message-from-the-Deep.jpg"
    data-trackTitle="1"></li>
<li data-audiopath="https://dl.iraniandj.ir/2025/10/18/2.mp3"
    data-albumArt="http://pro.iraniandj.ir/wp-content/uploads/2026/06/Some1-Message-from-the-Deep.jpg"
    data-trackTitle="2"></li>`
	tracks := parseIranianDJPost(post)
	if len(tracks) != 2 {
		t.Fatalf("tracks len = %d", len(tracks))
	}
	if tracks[0].Artist != "Some1" {
		t.Fatalf("artist = %q", tracks[0].Artist)
	}
	if tracks[0].Song != "Message from the Deep - 1" {
		t.Fatalf("song = %q", tracks[0].Song)
	}
	if tracks[0].AudioURL != "https://dl.iraniandj.ir/2025/10/18/1.mp3" {
		t.Fatalf("audio = %q", tracks[0].AudioURL)
	}
	if tracks[0].CoverURL != "http://pro.iraniandj.ir/wp-content/uploads/2026/06/Some1-Message-from-the-Deep.jpg" {
		t.Fatalf("cover = %q", tracks[0].CoverURL)
	}
}

func TestParseITunesTrack(t *testing.T) {
	track := parseITunesTrack(itunesTrack{
		ID:         36416,
		SlugURL:    "آریایی",
		Title:      "آریایی",
		Cover:      "https://api.itunes.ir/itunes-public/img/track_cover/1784016697.jpg",
		CoverThumb: "https://api.itunes.ir/itunes-public/Aron-Afshar-Jonibeik-Murdov-Ariyayi.jpg",
		AudioHQ:    "https://cdn24.dooorbin.com/itunes-salable/aud/track_audio/1784016696.mp3",
		Singers: []itunesSinger{{
			Name:   "آرون",
			Family: "افشار",
		}},
	})
	if track.ID != "itunes:36416" {
		t.Fatalf("id = %q", track.ID)
	}
	if track.Artist != "آرون افشار" {
		t.Fatalf("artist = %q", track.Artist)
	}
	if track.Song != "آریایی" {
		t.Fatalf("song = %q", track.Song)
	}
	if track.AudioURL != "https://cdn24.dooorbin.com/itunes-salable/aud/track_audio/1784016696.mp3" {
		t.Fatalf("audio = %q", track.AudioURL)
	}
	if track.CoverURL != "https://api.itunes.ir/itunes-public/img/track_cover/1784016697.jpg" {
		t.Fatalf("cover = %q", track.CoverURL)
	}
}
