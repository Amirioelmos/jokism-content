package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAparatSearchResolveDownload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/etc/api/videoBySearch/text/comedy/perpage/20", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"videobysearch":[{"uid":"abc","title":"fun","url":"https://example/video"}]}`) })
	mux.HandleFunc("/etc/api/video/videohash/abc", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"video":[{"title":"fun","url":"https://example/video","file_link_all":[{"profile":"480p","url":"http://`+r.Host+`/file.mp4"}]}]}`) })
	mux.HandleFunc("/file.mp4", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "video") })
	ts := httptest.NewServer(mux); defer ts.Close()
	a := Aparat{client: ts.Client(), base: ts.URL}
	items, err := a.Search(context.Background(), "comedy")
	if err != nil || len(items) != 1 || items[0].ID != "abc" { t.Fatalf("search: %#v, %v", items, err) }
	v, err := a.Resolve(context.Background(), items[0]); if err != nil || v.DownloadURL == "" { t.Fatalf("resolve: %#v, %v", v, err) }
	path, err := a.Download(context.Background(), v, 1024); if err != nil { t.Fatal(err) }; defer os.Remove(path)
	b, _ := os.ReadFile(path); if string(b) != "video" { t.Fatalf("download = %q", b) }
}
