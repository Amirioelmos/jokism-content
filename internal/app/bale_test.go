package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestBaleSendVideo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendVideo" { t.Errorf("path = %s", r.URL.Path) }
		if err := r.ParseMultipartForm(1 << 20); err != nil { t.Fatal(err) }
		if r.FormValue("chat_id") != "@channel" { t.Errorf("chat = %s", r.FormValue("chat_id")) }
		f, _, err := r.FormFile("video"); if err != nil { t.Fatal(err) }; defer f.Close()
		data, _ := io.ReadAll(f); if string(data) != "content" { t.Errorf("file = %q", data) }
		io.WriteString(w, `{"ok":true}`)
	})); defer ts.Close()
	f, err := os.CreateTemp("", "bale-test-*.mp4"); if err != nil { t.Fatal(err) }
	path := f.Name(); defer os.Remove(path); _, _ = f.WriteString("content"); _ = f.Close()
	b := Bale{client: ts.Client(), base: ts.URL, token: "token"}
	if err := b.SendVideo(context.Background(), "@channel", path, "caption"); err != nil { t.Fatal(err) }
}
