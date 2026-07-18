package app

import "testing"

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
