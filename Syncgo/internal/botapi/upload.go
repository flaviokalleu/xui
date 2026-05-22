package botapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

var bigHTTPClient = &http.Client{Timeout: 6 * time.Hour}

// maxTelegramUpload é o limite de upload via Bot API pública (50 MB).
// Arquivos maiores são rejeitados pelo Telegram antes de consumir o body inteiro,
// causando "closed pipe". Use o local Bot API para arquivos maiores.
const maxTelegramUpload = 50 * 1024 * 1024

// SendDocumentStream envia um arquivo para um chat Telegram lendo bytes de r.
// O upload é feito em streaming (sem buffer local).
// Se Content-Length informado em contentLength > 50 MB, retorna erro antes de tentar.
func (c *Client) SendDocumentStream(ctx context.Context, chatID int64, fileName string, r io.Reader, contentLength int64) (*Message, error) {
	// Rejeita antecipadamente arquivos maiores que o limite da Bot API
	if contentLength > 0 && contentLength > maxTelegramUpload {
		return nil, fmt.Errorf("arquivo muito grande para Bot API (%s > 50 MB) — use local Bot API", humanBytes(contentLength))
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	pipeDone := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			mw.Close()
			pw.CloseWithError(err)
			pipeDone <- err
		}()
		if err = mw.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
			return
		}
		if err = mw.WriteField("supports_streaming", "true"); err != nil {
			return
		}
		var fw io.Writer
		fw, err = mw.CreateFormFile("document", fileName)
		if err != nil {
			return
		}
		_, err = io.Copy(fw, r)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/sendDocument", pr)
	if err != nil {
		pw.CloseWithError(err)
		<-pipeDone
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, httpErr := bigHTTPClient.Do(req)
	pipeErr := <-pipeDone

	if httpErr != nil {
		return nil, httpErr
	}
	defer resp.Body.Close()

	// Parseia a resposta do Telegram ANTES de verificar o pipeErr.
	// Quando o Telegram rejeita o arquivo (ex: muito grande), ele responde com
	// {"ok":false} antes de consumir o body inteiro — isso fecha o pipe e gera
	// o erro "io: read/write on closed pipe" no goroutine de escrita.
	// O erro real está na resposta JSON, não no pipe.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		OK          bool    `json:"ok"`
		Description string  `json:"description"`
		Result      Message `json:"result"`
	}
	if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
		// Resposta não parseável: report pipe error se existir, senão decode error
		if pipeErr != nil {
			return nil, fmt.Errorf("upload interrompido: %w", pipeErr)
		}
		return nil, fmt.Errorf("decode: %w (body=%s)", jsonErr, string(body))
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram: %s", result.Description)
	}
	return &result.Result, nil
}

func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
