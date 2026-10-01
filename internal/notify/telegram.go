// Package notify sends anomaly alerts to Telegram.
package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Telegram posts photos with captions through a bot.
type Telegram struct {
	Token       string
	DefaultChat string
	BaseURL     string
	client      *http.Client
}

// NewTelegram returns a notifier; Enabled is false without a token.
func NewTelegram(token, defaultChat, baseURL string) *Telegram {
	return &Telegram{Token: token, DefaultChat: defaultChat, BaseURL: baseURL,
		client: &http.Client{Timeout: 30 * time.Second}}
}

// Enabled reports whether a bot token is configured.
func (t *Telegram) Enabled() bool { return t.Token != "" }

// SendPhoto sends an image with an HTML caption to chat (or the default chat).
func (t *Telegram) SendPhoto(ctx context.Context, chat, caption, imagePath string) error {
	if chat == "" {
		chat = t.DefaultChat
	}
	if !t.Enabled() || chat == "" {
		return fmt.Errorf("telegram not configured")
	}
	img, err := os.ReadFile(imagePath)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", chat)
	_ = w.WriteField("caption", caption)
	_ = w.WriteField("parse_mode", "HTML")
	part, _ := w.CreateFormFile("photo", filepath.Base(imagePath))
	_, _ = part.Write(img)
	_ = w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/sendPhoto", t.BaseURL, t.Token), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram %d: %s", resp.StatusCode, msg)
	}
	return nil
}
