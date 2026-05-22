package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"syncgo/internal/config"
	"syncgo/internal/database"
	"syncgo/internal/streamer"
	"syncgo/internal/telegram"
	tmpl "syncgo/internal/template"
)

type Server struct {
	cfg          *config.Config
	db           *database.DB
	pool         *telegram.Pool
	mux          *http.ServeMux
	httpSrv      *http.Server
	logger       *slog.Logger
	startTime    time.Time
	channelCache sync.Map // channelID -> accessHash
	metaCache    sync.Map // messageID -> *cachedMeta
	metrics      *metrics
}

type cachedMeta struct {
	meta    *streamer.FileMeta
	expires time.Time
}

const metaCacheTTL = 10 * time.Minute

func New(cfg *config.Config, db *database.DB, pool *telegram.Pool, logger *slog.Logger) *Server {
	s := &Server{
		cfg:       cfg,
		db:        db,
		pool:      pool,
		mux:       http.NewServeMux(),
		logger:    logger,
		startTime: time.Now(),
	}
	if cfg.MetricsEnabled {
		s.metrics = newMetrics()
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	// Streaming endpoint: sem rate limit — o FloodWait do Telegram é o throttle natural.
	// Rate limit aqui bloquearia players de vídeo legítimos que fazem múltiplos Range requests.
	s.mux.HandleFunc("/", s.rootOrStream)
	s.mux.HandleFunc("/health", s.healthHandler)

	// /watch/ gera a página HTML do player — rate limit aqui faz sentido para evitar abuso.
	watchHandler := http.Handler(http.HandlerFunc(s.watchHandler))
	if s.cfg.RateLimitPerMin > 0 {
		watchHandler = rateLimit(s.cfg.RateLimitPerMin, watchHandler)
	}
	s.mux.Handle("/watch/", watchHandler)

	if s.metrics != nil {
		s.mux.Handle("/metrics", metricsHandler())
	}
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	ready := s.pool.ReadyCount() > 0
	status := http.StatusOK
	body := `{"status":"ok"}`
	if !ready {
		status = http.StatusServiceUnavailable
		body = `{"status":"starting"}`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, body)
}

func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.BindAddress, s.cfg.Port)
	// Rate limit é aplicado por rota em routes(), não globalmente.
	// O endpoint de streaming (/) não tem rate limit — Telegram FloodWait controla o throughput.
	handler := securityHeaders(s.mux)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	shutdownTimeout := time.Duration(s.cfg.ShutdownTimeout) * time.Second
	if shutdownTimeout <= 0 {
		shutdownTimeout = 30 * time.Second
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutdownCtx)
	}()

	// Limpeza periódica do metaCache para evitar crescimento ilimitado.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				s.metaCache.Range(func(k, v any) bool {
					if c, ok := v.(*cachedMeta); ok && now.After(c.expires) {
						s.metaCache.Delete(k)
					}
					return true
				})
			}
		}
	}()
	s.logger.Info("HTTP server listening",
		"addr", addr, "url", s.cfg.URLBase(),
		"rate_limit_per_min_watch", s.cfg.RateLimitPerMin,
		"stream_rate_limit", "none (Telegram FloodWait)",
		"shutdown_timeout", shutdownTimeout,
		"metrics", s.metrics != nil)
	if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) rootOrStream(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"server":"syncgo","status":"ok","uptime":%q}`, time.Since(s.startTime).String())
		return
	}
	s.streamHandler(w, r, path)
}

var pathRe = regexp.MustCompile(`^([a-zA-Z0-9_-]{10})(\d+)$`)
var idRe = regexp.MustCompile(`^(\d+)`)

func parsePath(path string, query string) (secureHash string, id int64, err error) {
	if m := pathRe.FindStringSubmatch(path); m != nil {
		secureHash = m[1]
		id, err = strconv.ParseInt(m[2], 10, 64)
		return
	}
	if m := idRe.FindStringSubmatch(path); m != nil {
		id, err = strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return "", 0, err
		}
		// Try query string for hash.
		// Query already parsed by caller.
		secureHash = query
		return
	}
	return "", 0, fmt.Errorf("invalid path")
}

func (s *Server) watchHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/watch/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	hash := r.URL.Query().Get("hash")
	secureHash, id, err := parsePath(path, hash)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	rec, err := s.db.GetFile(r.Context(), id)
	if err != nil {
		s.logger.Error("db.GetFile", "err", err, "id", id)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rec == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if rec.SecureHash != secureHash {
		http.Error(w, "invalid hash", http.StatusForbidden)
		return
	}

	streamURL := fmt.Sprintf("%s/%s%d", s.cfg.URLBase(), rec.SecureHash, rec.MessageID)
	page := tmpl.RenderPlayer(tmpl.PlayerData{
		Title:    rec.FileName,
		FileSize: rec.FileSize,
		MimeType: rec.MimeType,
		StreamURL: streamURL,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func (s *Server) streamHandler(w http.ResponseWriter, r *http.Request, path string) {
	hash := r.URL.Query().Get("hash")
	secureHash, id, err := parsePath(path, hash)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	rec, err := s.db.GetFile(r.Context(), id)
	if err != nil {
		s.logger.Error("db.GetFile", "err", err, "id", id)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rec == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if rec.SecureHash != secureHash {
		http.Error(w, "invalid hash", http.StatusForbidden)
		return
	}

	// Resolve metadados usando qualquer cliente disponível (meta é cacheada).
	anyClient := s.pool.Pick()
	if anyClient == nil || anyClient.API == nil {
		http.Error(w, "no telegram client available", http.StatusServiceUnavailable)
		return
	}

	channelID, accessHash, err := s.resolveLogChannel(r.Context(), anyClient)
	if err != nil {
		s.logger.Error("resolveLogChannel", "err", err)
		http.Error(w, "channel resolve error", http.StatusInternalServerError)
		return
	}

	meta, err := s.fetchMeta(r.Context(), anyClient.API, channelID, accessHash, rec.MessageID)
	if err != nil {
		s.logger.Error("streamer.GetMeta", "err", err, "msgid", rec.MessageID)
		http.Error(w, "file not found in channel", http.StatusNotFound)
		return
	}

	// ETag = hash do arquivo (imutável no Telegram).
	etag := fmt.Sprintf(`"%s%d"`, secureHash, id)

	// Responde 304 se Cloudflare/browser já tem o conteúdo em cache.
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	fileSize := meta.Size
	from, to, hasRange, err := parseRange(r.Header.Get("Range"), fileSize)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	mimeType := meta.MimeType
	fileName := meta.FileName
	if fileName == "" {
		fileName = rec.FileName
	}
	if mimeType == "" {
		mimeType = rec.MimeType
	}
	if mimeType == "" && fileName != "" {
		mimeType = mime.TypeByExtension(filepath.Ext(fileName))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if fileName == "" {
		fileName = fmt.Sprintf("file_%d", rec.MessageID)
	}

	disposition := "inline"
	if r.URL.Query().Get("dl") == "1" {
		disposition = "attachment"
	}

	// Seleciona cliente preferindo o DC do arquivo + com slot disponível.
	client := s.pool.PickForStream(meta.DC)
	if client == nil || client.API == nil {
		http.Error(w, "no telegram client available", http.StatusServiceUnavailable)
		return
	}
	acquired := client.Acquire()
	defer func() {
		if acquired {
			client.Release()
		} else {
			// Degradação graciosa: semáforo cheio mas prossegue mesmo assim.
			client.Workload.Add(-1)
		}
	}()
	if !acquired {
		// Race entre PickForStream e Acquire (outro goroutine tomou o slot).
		// Força entrada para não rejeitar o usuário; loga para monitoramento.
		client.Workload.Add(1)
		s.logger.Warn("stream slot at capacity, proceeding anyway",
			"dc", client.DC, "workload", client.Workload.Load())
	}

	// ── Cabeçalhos para cache no Cloudflare ─────────────────────────────────
	// Arquivos do Telegram são imutáveis (mesmo hash = mesmos bytes).
	// Cache-Control: public permite que o Cloudflare guarde na borda.
	// s-maxage=31536000 = 1 ano na CDN; max-age=3600 = 1h no browser.
	w.Header().Set("Cache-Control", "public, s-maxage=31536000, max-age=3600, stale-while-revalidate=86400")
	w.Header().Set("ETag", etag)
	w.Header().Set("Vary", "Range")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(to-from+1, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename=%q`, disposition, fileName))
	if hasRange {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, fileSize))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	st := streamer.New(client.API)
	cw := &countingWriter{w: w, m: s.metrics}
	if err := st.StreamRange(r.Context(), meta.Location, from, to, cw); err != nil {
		if !isClientGone(err) {
			s.logger.Error("StreamRange", "err", err, "msgid", rec.MessageID, "from", from, "to", to)
			if s.metrics != nil {
				s.metrics.streamErrors.WithLabelValues("stream_range").Inc()
			}
		}
	}
	if s.metrics != nil {
		status := "200"
		if hasRange {
			status = "206"
		}
		s.metrics.streamRequests.WithLabelValues("stream", status).Inc()
	}
}

type countingWriter struct {
	w http.ResponseWriter
	m *metrics
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.m != nil {
		c.m.bytesStreamed.Add(float64(n))
	}
	return n, err
}

func (s *Server) fetchMeta(ctx context.Context, api *tg.Client, channelID, accessHash, messageID int64) (*streamer.FileMeta, error) {
	if v, ok := s.metaCache.Load(messageID); ok {
		c := v.(*cachedMeta)
		if time.Now().Before(c.expires) {
			return c.meta, nil
		}
		s.metaCache.Delete(messageID)
	}
	meta, err := streamer.GetMeta(ctx, api, channelID, accessHash, int(messageID))
	if err != nil {
		return nil, err
	}
	s.metaCache.Store(messageID, &cachedMeta{meta: meta, expires: time.Now().Add(metaCacheTTL)})
	return meta, nil
}

func (s *Server) resolveLogChannel(ctx context.Context, client *telegram.Client) (int64, int64, error) {
	// LOG_CHANNEL is the negative ID like -100xxxxxxx → channel ID xxxxxxx.
	raw := s.cfg.LogChannel
	channelID := raw
	if raw < -1000000000000 {
		channelID = -raw - 1000000000000
	} else if raw < 0 {
		channelID = -raw
	}
	// Need access_hash. Use channels.GetChannels with InputChannelFromMessage is needed if we don't have it.
	// Simpler: use channels.GetFullChannel with InputChannel{ChannelID: channelID, AccessHash: 0} after getDialogs.
	// Best path: cache after first resolution via getChannels.
	if cached, ok := s.channelCache.Load(channelID); ok {
		if hash, ok := cached.(int64); ok {
			return channelID, hash, nil
		}
		s.channelCache.Delete(channelID) // valor corrompido, força re-resolve
	}
	res, err := client.API.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID, AccessHash: 0},
	})
	if err != nil {
		return 0, 0, err
	}
	chats := res.GetChats()
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == channelID {
			s.channelCache.Store(channelID, ch.AccessHash)
			return channelID, ch.AccessHash, nil
		}
	}
	return 0, 0, fmt.Errorf("channel %d not found", channelID)
}

func parseRange(header string, size int64) (from, to int64, has bool, err error) {
	if header == "" {
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false, fmt.Errorf("invalid range header")
	}
	spec := strings.TrimPrefix(header, "bytes=")
	parts := strings.SplitN(spec, ",", 2)[0]
	startStr, endStr, found := strings.Cut(parts, "-")
	if !found {
		return 0, 0, false, fmt.Errorf("invalid range")
	}
	startStr = strings.TrimSpace(startStr)
	endStr = strings.TrimSpace(endStr)

	if startStr == "" {
		// Suffix range: bytes=-N → last N bytes.
		n, perr := strconv.ParseInt(endStr, 10, 64)
		if perr != nil || n <= 0 {
			return 0, 0, false, fmt.Errorf("invalid suffix range")
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, nil
	}

	from, err = strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		return 0, 0, false, err
	}
	if endStr == "" {
		to = size - 1
	} else {
		to, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil {
			return 0, 0, false, err
		}
	}
	if from < 0 || to >= size || from > to {
		return 0, 0, false, fmt.Errorf("range out of bounds")
	}
	return from, to, true, nil
}

func isClientGone(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "context canceled") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection reset")
}
