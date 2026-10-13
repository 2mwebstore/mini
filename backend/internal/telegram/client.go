// Package telegram is a minimal, send-only Telegram Bot API client: the API uses it
// for notifications and to point the bot's menu button at the Mini App. Nothing
// receives updates (there is no chat bot service).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// TokenSource returns the current bot token ("" = Telegram not configured). The
// token lives in company settings, so it can change while the API runs.
type TokenSource func(ctx context.Context) string

// ErrNotConfigured: no bot token has been set in Settings yet.
var ErrNotConfigured = errors.New("telegram: bot token not configured")

type Client struct {
	token TokenSource
	base  string
	http  *http.Client
}

func New(token TokenSource, base string) *Client {
	return &Client{token: token, base: base, http: &http.Client{Timeout: 30 * time.Second}}
}

// WithToken returns a client using a fixed token (to verify a token before saving it).
func (c *Client) WithToken(token string) *Client {
	return &Client{token: func(context.Context) string { return token }, base: c.base, http: c.http}
}

func (c *Client) endpoint(ctx context.Context, method string) (string, error) {
	tok := c.token(ctx)
	if tok == "" {
		return "", ErrNotConfigured
	}
	return fmt.Sprintf("%s/bot%s/%s", c.base, tok, method), nil
}

// WebAppInfo opens the Mini App (allowed on buttons in private chats only).
type WebAppInfo struct {
	URL string `json:"url"`
}

type InlineButton struct {
	Text   string      `json:"text"`
	URL    string      `json:"url,omitempty"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
}

type Message struct {
	MessageID int64       `json:"message_id"`
	Photo     []PhotoSize `json:"photo,omitempty"`
}

// LargestPhoto returns the biggest size of a sent photo (Telegram orders sizes ascending).
func (m *Message) LargestPhoto() *PhotoSize {
	if m == nil || len(m.Photo) == 0 {
		return nil
	}
	return &m.Photo[len(m.Photo)-1]
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// APIError is returned when Telegram answers ok=false.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram %d: %s", e.Code, e.Description) }

func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url, err := c.endpoint(ctx, method)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// Never include the URL: it contains the bot token.
		return fmt.Errorf("telegram request failed: %w", stripURL(err))
	}
	defer resp.Body.Close()
	var r apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return fmt.Errorf("telegram: decode response: %w", err)
	}
	if !r.OK {
		return &APIError{Code: r.ErrorCode, Description: r.Description}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboard) (*Message, error) {
	p := map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if kb != nil {
		p["reply_markup"] = kb
	}
	var m Message
	return &m, c.call(ctx, "sendMessage", p, &m)
}

// SendPhoto re-sends a photo by Telegram file_id (no download/upload needed).
func (c *Client) SendPhoto(ctx context.Context, chatID int64, fileID, caption string, kb *InlineKeyboard) (*Message, error) {
	p := map[string]any{"chat_id": chatID, "photo": fileID, "caption": caption, "parse_mode": "HTML"}
	if kb != nil {
		p["reply_markup"] = kb
	}
	var m Message
	return &m, c.call(ctx, "sendPhoto", p, &m)
}

// SendPhotoData uploads a photo (Mini App check-ins have no Telegram file_id yet).
// The returned message carries the new file_id, so later sends can use SendPhoto.
func (c *Client) SendPhotoData(ctx context.Context, chatID int64, data []byte, caption string, kb *InlineKeyboard) (*Message, error) {
	fields := map[string]string{"caption": caption, "parse_mode": "HTML"}
	if kb != nil {
		b, err := json.Marshal(kb)
		if err != nil {
			return nil, err
		}
		fields["reply_markup"] = string(b)
	}
	return c.upload(ctx, "sendPhoto", chatID, "photo", "photo.jpg", data, fields)
}

func (c *Client) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) (*Message, error) {
	return c.upload(ctx, "sendDocument", chatID, "document", filename, data, map[string]string{"caption": caption, "parse_mode": "HTML"})
}

func (c *Client) upload(ctx context.Context, method string, chatID int64, field, filename string, data []byte, fields map[string]string) (*Message, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", fmt.Sprint(chatID))
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(data); err != nil {
		return nil, err
	}
	w.Close()
	url, err := c.endpoint(ctx, method)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	var m Message
	return &m, c.do(req, &m)
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	return &u, c.call(ctx, "getMe", map[string]any{}, &u)
}

// SetMenuButton makes the chat menu button open the Mini App in every private chat
// with the bot (an empty url restores the default commands menu).
func (c *Client) SetMenuButton(ctx context.Context, text, url string) error {
	button := map[string]any{"type": "default"}
	if url != "" {
		button = map[string]any{"type": "web_app", "text": text, "web_app": WebAppInfo{URL: url}}
	}
	return c.call(ctx, "setChatMenuButton", map[string]any{"menu_button": button}, nil)
}

// Esc escapes user-supplied text for HTML parse mode.
func Esc(s string) string { return html.EscapeString(s) }

func stripURL(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok && u.Unwrap() != nil {
		return u.Unwrap()
	}
	return err
}
