package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	token string
	base  string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{
		token: token,
		base:  "https://api.telegram.org/bot" + token,
		http:  &http.Client{Timeout: 90 * time.Second},
	}
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type Video struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Duration     int    `json:"duration"`
}

type Audio struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	Duration     int    `json:"duration"`
}

type Voice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type Animation struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Duration     int    `json:"duration"`
}

type VideoNote struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	Length       int    `json:"length"`
	FileSize     int64  `json:"file_size"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type Sticker struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Type         string `json:"type"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	IsAnimated   bool   `json:"is_animated"`
	IsVideo      bool   `json:"is_video"`
	FileSize     int64  `json:"file_size"`
}

type Message struct {
	MessageID int          `json:"message_id"`
	From      *User        `json:"from"`
	Chat      Chat         `json:"chat"`
	Date      int64        `json:"date"`
	Text      string       `json:"text"`
	Caption   string       `json:"caption"`
	Document  *Document    `json:"document"`
	Video     *Video       `json:"video"`
	Audio     *Audio       `json:"audio"`
	Voice     *Voice       `json:"voice"`
	Animation *Animation   `json:"animation"`
	VideoNote *VideoNote   `json:"video_note"`
	Photo     []PhotoSize  `json:"photo"`
	Sticker   *Sticker     `json:"sticker"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *Message       `json:"message"`
	ChannelPost   *Message       `json:"channel_post"`
	EditedMessage *Message       `json:"edited_message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      T      `json:"result"`
}

func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	endpoint := c.base + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	type wrap struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	var w wrap
	if err := json.Unmarshal(body, &w); err != nil {
		return fmt.Errorf("decode (status %d): %w (body=%s)", resp.StatusCode, err, string(body))
	}
	if !w.OK {
		return fmt.Errorf("telegram error: %s", w.Description)
	}
	if out != nil && len(w.Result) > 0 {
		if err := json.Unmarshal(w.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

// callMultipart uploads a file via multipart/form-data and optionally decodes the result.
func (c *Client) callMultipart(ctx context.Context, method string, fields map[string]string, fileField, fileName string, fileData io.Reader, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	fw, err := w.CreateFormFile(fileField, fileName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, fileData); err != nil {
		return err
	}
	w.Close()

	endpoint := c.base + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	type wrap struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	var ww wrap
	if err := json.Unmarshal(body, &ww); err != nil {
		return fmt.Errorf("decode (status %d): %w (body=%s)", resp.StatusCode, err, string(body))
	}
	if !ww.OK {
		return fmt.Errorf("telegram error: %s", ww.Description)
	}
	if out != nil && len(ww.Result) > 0 {
		if err := json.Unmarshal(ww.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

type SendPhotoParams struct {
	ChatID      int64
	Caption     string
	ParseMode   string
	ReplyMarkup string
}

func (c *Client) SendPhoto(ctx context.Context, p SendPhotoParams, img io.Reader) (*SentMessage, error) {
	fields := map[string]string{
		"chat_id": strconv.FormatInt(p.ChatID, 10),
	}
	if p.Caption != "" {
		fields["caption"] = p.Caption
	}
	if p.ParseMode != "" {
		fields["parse_mode"] = p.ParseMode
	}
	if p.ReplyMarkup != "" {
		fields["reply_markup"] = p.ReplyMarkup
	}
	var out SentMessage
	if err := c.callMultipart(ctx, "sendPhoto", fields, "photo", "qr.png", img, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int, timeout int) ([]Update, error) {
	params := url.Values{}
	params.Set("offset", strconv.Itoa(offset))
	params.Set("timeout", strconv.Itoa(timeout))
	params.Set("allowed_updates", `["message","channel_post","callback_query"]`)
	var updates []Update
	if err := c.call(ctx, "getUpdates", params, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

type SendMessageParams struct {
	ChatID                int64
	Text                  string
	ParseMode             string
	ReplyToMessageID      int
	DisableWebPagePreview bool
	ReplyMarkup           string
}

type SentMessage struct {
	MessageID int   `json:"message_id"`
	Chat      Chat  `json:"chat"`
	Date      int64 `json:"date"`
}

func (c *Client) SendMessage(ctx context.Context, p SendMessageParams) (*SentMessage, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(p.ChatID, 10))
	params.Set("text", p.Text)
	if p.ParseMode != "" {
		params.Set("parse_mode", p.ParseMode)
	}
	if p.ReplyToMessageID != 0 {
		params.Set("reply_to_message_id", strconv.Itoa(p.ReplyToMessageID))
	}
	if p.DisableWebPagePreview {
		params.Set("disable_web_page_preview", "true")
	}
	if p.ReplyMarkup != "" {
		params.Set("reply_markup", p.ReplyMarkup)
	}
	var out SentMessage
	if err := c.call(ctx, "sendMessage", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ForwardMessageParams struct {
	ChatID     int64
	FromChatID int64
	MessageID  int
}

func (c *Client) ForwardMessage(ctx context.Context, p ForwardMessageParams) (*Message, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(p.ChatID, 10))
	params.Set("from_chat_id", strconv.FormatInt(p.FromChatID, 10))
	params.Set("message_id", strconv.Itoa(p.MessageID))
	var out Message
	if err := c.call(ctx, "forwardMessage", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type CopyMessageParams struct {
	ChatID      int64
	FromChatID  int64
	MessageID   int
	ReplyMarkup string
}

type CopyResult struct {
	MessageID int `json:"message_id"`
}

func (c *Client) CopyMessage(ctx context.Context, p CopyMessageParams) (*CopyResult, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(p.ChatID, 10))
	params.Set("from_chat_id", strconv.FormatInt(p.FromChatID, 10))
	params.Set("message_id", strconv.Itoa(p.MessageID))
	if p.ReplyMarkup != "" {
		params.Set("reply_markup", p.ReplyMarkup)
	}
	var out CopyResult
	if err := c.call(ctx, "copyMessage", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func InlineKeyboardJSON(rows [][]InlineButton) string {
	type out struct {
		InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
	}
	b, _ := json.Marshal(out{InlineKeyboard: rows})
	return string(b)
}

type InlineButton struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", url.Values{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackQueryID, text string) error {
	params := url.Values{}
	params.Set("callback_query_id", callbackQueryID)
	if text != "" {
		params.Set("text", text)
	}
	return c.call(ctx, "answerCallbackQuery", params, nil)
}

type EditMessageTextParams struct {
	ChatID      int64
	MessageID   int
	Text        string
	ParseMode   string
	ReplyMarkup string
}

func (c *Client) EditMessageText(ctx context.Context, p EditMessageTextParams) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(p.ChatID, 10))
	params.Set("message_id", strconv.Itoa(p.MessageID))
	params.Set("text", p.Text)
	if p.ParseMode != "" {
		params.Set("parse_mode", p.ParseMode)
	}
	if p.ReplyMarkup != "" {
		params.Set("reply_markup", p.ReplyMarkup)
	}
	return c.call(ctx, "editMessageText", params, nil)
}

func (c *Client) EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int, replyMarkup string) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("message_id", strconv.Itoa(messageID))
	params.Set("reply_markup", replyMarkup)
	return c.call(ctx, "editMessageReplyMarkup", params, nil)
}
