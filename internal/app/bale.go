package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Bale struct {
	client      *http.Client
	base, token string
}

type BaleError struct {
	StatusCode  int
	Description string
}

func (e *BaleError) Error() string {
	return fmt.Sprintf("خطای بله HTTP %d: %s", e.StatusCode, e.Description)
}

func (b *Bale) SendVideo(ctx context.Context, chat, path, caption string) error {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	writeErr := make(chan error, 1)
	go func() {
		fail := func(err error) { _ = writer.CloseWithError(err); writeErr <- err }
		if err := form.WriteField("chat_id", chat); err != nil {
			fail(err)
			return
		}
		if err := form.WriteField("caption", caption); err != nil {
			fail(err)
			return
		}
		part, err := form.CreateFormFile("video", filepath.Base(path))
		if err != nil {
			fail(err)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			fail(err)
			return
		}
		_, copyErr := io.Copy(part, file)
		closeErr := file.Close()
		if copyErr != nil {
			fail(copyErr)
			return
		}
		if closeErr != nil {
			fail(closeErr)
			return
		}
		if err = form.Close(); err != nil {
			fail(err)
			return
		}
		writeErr <- writer.Close()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/bot"+b.token+"/sendVideo", reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err = <-writeErr; err != nil {
		return err
	}
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	if res.StatusCode/100 != 2 || !out.OK {
		return &BaleError{StatusCode: res.StatusCode, Description: out.Description}
	}
	return nil
}

func (b *Bale) SendVideoURL(ctx context.Context, chat, videoURL, caption string) error {
	form := url.Values{}
	form.Set("chat_id", chat)
	form.Set("video", videoURL)
	form.Set("caption", caption)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/bot"+b.token+"/sendVideo", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	if res.StatusCode/100 != 2 || !out.OK {
		return &BaleError{StatusCode: res.StatusCode, Description: out.Description}
	}
	return nil
}

func (b *Bale) SendAudioURL(ctx context.Context, chat, audioURL, caption, title, performer string) error {
	form := url.Values{}
	form.Set("chat_id", chat)
	form.Set("audio", audioURL)
	form.Set("caption", caption)
	if title != "" {
		form.Set("title", title)
	}
	if performer != "" {
		form.Set("performer", performer)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/bot"+b.token+"/sendAudio", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	if res.StatusCode/100 != 2 || !out.OK {
		return &BaleError{StatusCode: res.StatusCode, Description: out.Description}
	}
	return nil
}
