package botapi

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"syncgo/internal/config"
	"syncgo/internal/database"
	"syncgo/internal/importer"
	"syncgo/internal/telegram"
	"syncgo/internal/tmdb"
	"syncgo/internal/util"
	"syncgo/internal/xui"
)

type PollerDeps struct {
	API            *Client
	DB             *database.DB
	URLBase        string
	LogChannel     int64
	HashSecret     string
	Logger         *slog.Logger
	Importer       *importer.Importer // optional — auto-insert into XUI if set
	Cfg            *config.Config     // para rebuild do importer após /configurar
	TGPool         *telegram.Pool     // MTProto pool for large file uploads (>50MB)
	LogChannelPeer tg.InputPeerClass  // resolved InputPeer for LOG_CHANNEL
}

type Poller struct {
	deps       PollerDeps
	offset     int
	startTime  time.Time
	sessions   *sessionStore
	setups     *setupStore
	m3uAdds    *m3uStore
	xtreamJobs *xtreamJobStore
	appCtx     context.Context // main app lifetime context
}

func NewPoller(deps PollerDeps) *Poller {
	return &Poller{
		deps:       deps,
		startTime:  time.Now(),
		sessions:   newSessionStore(),
		setups:     newSetupStore(),
		m3uAdds:    newM3UStore(),
		xtreamJobs: newXtreamJobStore(),
	}
}

func (p *Poller) Run(ctx context.Context) error {
	p.appCtx = ctx
	p.deps.Logger.Info("bot api poller started")

	// Envia painel de controle ao owner/admins ao iniciar.
	if p.deps.Cfg != nil {
		targets := make([]int64, 0)
		if p.deps.Cfg.OwnerID != 0 {
			targets = append(targets, p.deps.Cfg.OwnerID)
		}
		for _, a := range p.deps.Cfg.Admins {
			targets = append(targets, a)
		}
		for _, id := range targets {
			_ = p.sendMenu(ctx, id)
		}
	}

	backoff := time.Second
	const maxBackoff = 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		updates, err := p.deps.API.GetUpdates(ctx, p.offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			p.deps.Logger.Warn("getUpdates", "err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = time.Second // reset no sucesso
		for _, u := range updates {
			if u.UpdateID >= p.offset {
				p.offset = u.UpdateID + 1
			}
			if u.Message != nil {
				if err := p.handleMessage(ctx, u.Message); err != nil {
					p.deps.Logger.Error("handleMessage", "err", err, "msg_id", u.Message.MessageID)
				}
			}
			if u.ChannelPost != nil {
				if err := p.handleChannelPost(ctx, u.ChannelPost); err != nil {
					p.deps.Logger.Error("handleChannelPost", "err", err, "msg_id", u.ChannelPost.MessageID)
				}
			}
			if u.CallbackQuery != nil {
				if err := p.handleCallbackQuery(ctx, u.CallbackQuery); err != nil {
					p.deps.Logger.Error("handleCallbackQuery", "err", err, "data", u.CallbackQuery.Data)
				}
			}
		}
	}
}

func (p *Poller) handleMessage(ctx context.Context, m *Message) error {
	// Wizard de configuração tem prioridade sobre tudo.
	if p.handleSetupInput(ctx, m) {
		return nil
	}
	// Sessão de adição de M3U tem segunda prioridade.
	if p.handleM3UInput(ctx, m) {
		return nil
	}
	// Sessão de edição de insert também tem prioridade.
	if p.handleEditInput(ctx, m) {
		return nil
	}

	text := strings.TrimSpace(m.Text)
	cmd := commandOf(text)
	switch cmd {
	case "/start":
		return p.handleStart(ctx, m)
	case "/menu":
		return p.sendMenu(ctx, m.Chat.ID)
	case "/help":
		return p.handleHelp(ctx, m)
	case "/ping":
		return p.handlePing(ctx, m)
	case "/id":
		return p.handleID(ctx, m)
	case "/stats":
		return p.handleStats(ctx, m)
	case "/info":
		return p.handleInfo(ctx, m)
	case "/configurar", "/setup":
		p.startSetup(ctx, m.Chat.ID)
		return nil
	case "/m3u", "/lista", "/canais":
		return p.sendM3UMenu(ctx, m.Chat.ID)
	case "/addtoken":
		return p.handleAddToken(ctx, m)
	case "/tokens":
		return p.handleListTokens(ctx, m)
	case "/rmtoken":
		return p.handleRemoveToken(ctx, m)
	case "/settings":
		return p.handleSettings(ctx, m)
	case "/set":
		return p.handleSet(ctx, m)
	case "/unset":
		return p.handleUnset(ctx, m)
	}
	if hasMedia(m) {
		return p.handleMedia(ctx, m)
	}
	return nil
}

func commandOf(text string) string {
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	end := len(text)
	for i, r := range text {
		if r == ' ' || r == '@' {
			end = i
			break
		}
	}
	return strings.ToLower(text[:end])
}

func (p *Poller) handleHelp(ctx context.Context, m *Message) error {
	body := "<b>Comandos:</b>\n" +
		"/start — boas-vindas\n" +
		"/help — esta lista\n" +
		"/ping — verifica se o bot está vivo\n" +
		"/id — mostra seu user ID e o ID do chat\n" +
		"/stats — estatísticas do servidor\n" +
		"/info — versão e configuração\n\n" +
		"<b>Como usar:</b> envie qualquer arquivo (vídeo, áudio, doc, foto) e eu retorno links de download e streaming.\n\n" +
		"<b>Adição automática ao painel:</b> nomeie o arquivo como <code>123456.mp4</code> (filme) ou <code>123456_S01E02.mp4</code> (série) para adicionar automaticamente ao painel."
	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID: m.Chat.ID, Text: body, ParseMode: "HTML",
	})
	return err
}

func (p *Poller) handlePing(ctx context.Context, m *Message) error {
	start := time.Now()
	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID: m.Chat.ID,
		Text:   fmt.Sprintf("🏓 pong (%.0fms)", float64(time.Since(start).Microseconds())/1000),
	})
	return err
}

func (p *Poller) handleID(ctx context.Context, m *Message) error {
	body := fmt.Sprintf("<b>Chat ID:</b> <code>%d</code>", m.Chat.ID)
	if m.From != nil {
		body += fmt.Sprintf("\n<b>User ID:</b> <code>%d</code>", m.From.ID)
		if m.From.Username != "" {
			body += fmt.Sprintf("\n<b>Username:</b> @%s", m.From.Username)
		}
	}
	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID: m.Chat.ID, Text: body, ParseMode: "HTML",
		ReplyToMessageID: m.MessageID,
	})
	return err
}

func (p *Poller) handleStats(ctx context.Context, m *Message) error {
	st, err := p.deps.DB.Stats(ctx)
	if err != nil {
		return err
	}
	uptime := time.Since(p.startTime).Round(time.Second)
	body := fmt.Sprintf(
		"<b>📊 Estatísticas Syncgo</b>\n\n"+
			"📁 <b>Arquivos:</b> %d\n"+
			"👤 <b>Usuários:</b> %d\n"+
			"📦 <b>Tamanho total:</b> %s\n"+
			"⏱ <b>Uptime:</b> %s",
		st.Files, st.Users, util.HumanBytes(st.TotalBytes), uptime,
	)
	_, err = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID: m.Chat.ID, Text: body, ParseMode: "HTML",
	})
	return err
}

func (p *Poller) handleInfo(ctx context.Context, m *Message) error {
	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID: m.Chat.ID, Text: p.menuText(ctx), ParseMode: "HTML",
	})
	return err
}

// sendMenu envia o painel de controle completo como nova mensagem.
func (p *Poller) sendMenu(ctx context.Context, chatID int64) error {
	isAdmin := p.isOwnerOrAdmin(chatID)
	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:      chatID,
		Text:        p.menuText(ctx),
		ParseMode:   "HTML",
		ReplyMarkup: menuKeyboard(isAdmin),
	})
	return err
}

// editMenu edita uma mensagem existente para mostrar o menu atualizado.
func (p *Poller) editMenu(ctx context.Context, chatID int64, msgID int) {
	isAdmin := p.isOwnerOrAdmin(chatID)
	_ = p.deps.API.EditMessageText(ctx, EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   msgID,
		Text:        p.menuText(ctx),
		ParseMode:   "HTML",
		ReplyMarkup: menuKeyboard(isAdmin),
	})
}

// menuText monta o texto de status completo exibido no menu.
func (p *Poller) menuText(ctx context.Context) string {
	var sb strings.Builder
	sb.WriteString("🤖 <b>Syncgo</b> — Painel de Controle\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// ── Painel XUI ──────────────────────────────
	if host, ok := p.deps.DB.GetSetting(ctx, "xui_host"); ok && host != "" {
		portStr, _ := p.deps.DB.GetSetting(ctx, "xui_port")
		if portStr == "" {
			portStr = "3306"
		}
		if p.deps.Importer != nil {
			fmt.Fprintf(&sb, "📡 <b>Painel XUI:</b> ✅ Conectado (<code>%s:%s</code>)\n", host, portStr)
		} else {
			fmt.Fprintf(&sb, "📡 <b>Painel XUI:</b> ⚠️ Configurado mas offline (<code>%s:%s</code>)\n", host, portStr)
		}
	} else {
		sb.WriteString("📡 <b>Painel XUI:</b> ❌ Não configurado\n")
	}

	// Modo de inserção
	switch mode, _ := p.deps.DB.GetSetting(ctx, "insert_mode"); mode {
	case "auto":
		sb.WriteString("⚙️ <b>Modo:</b> 🤖 Automático\n")
	case "manual":
		sb.WriteString("⚙️ <b>Modo:</b> 👤 Manual\n")
	default:
		sb.WriteString("⚙️ <b>Modo:</b> ❓ Não definido\n")
	}

	if mBq, ok := p.deps.DB.GetSetting(ctx, "default_movie_bouquet_name"); ok && mBq != "" {
		fmt.Fprintf(&sb, "🎬 <b>Filmes →</b> %s\n", mBq)
	}
	if sBq, ok := p.deps.DB.GetSetting(ctx, "default_series_bouquet_name"); ok && sBq != "" {
		fmt.Fprintf(&sb, "📺 <b>Séries →</b> %s\n", sBq)
	}

	// ── Bots & Tokens ────────────────────────────
	sb.WriteString("\n")
	if p.deps.TGPool != nil {
		all := p.deps.TGPool.All()
		ready := 0
		for _, c := range all {
			if c.API != nil {
				ready++
			}
		}
		slots := 0
		if p.deps.Cfg != nil {
			slots = ready * p.deps.Cfg.MaxStreamsPerToken
		}
		fmt.Fprintf(&sb, "🤖 <b>Bots MTProto:</b> %d/%d ativos · %d slots de stream\n", ready, len(all), slots)
	}

	// ── TMDB ─────────────────────────────────────
	sb.WriteString("\n")
	if key, ok := p.deps.DB.GetSetting(ctx, "tmdb_api_key"); ok && key != "" {
		lang, _ := p.deps.DB.GetSetting(ctx, "tmdb_language")
		if lang == "" {
			lang = "pt-BR"
		}
		fmt.Fprintf(&sb, "🎭 <b>TMDB:</b> ✅ Configurado · idioma: <code>%s</code>\n", lang)
	} else {
		sb.WriteString("🎭 <b>TMDB:</b> ❌ Sem chave API\n")
	}

	// ── Estatísticas rápidas ──────────────────────
	if st, err := p.deps.DB.Stats(ctx); err == nil {
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "📁 <b>Arquivos:</b> %d · <b>Usuários:</b> %d · <b>Tamanho:</b> %s\n",
			st.Files, st.Users, util.HumanBytes(st.TotalBytes))
	}

	// ── Uptime ───────────────────────────────────
	fmt.Fprintf(&sb, "⏱ <b>Uptime:</b> %s\n", time.Since(p.startTime).Round(time.Second))

	return sb.String()
}

// menuKeyboard monta o teclado inline do menu principal.
func menuKeyboard(isAdmin bool) string {
	rows := [][]InlineButton{
		// Painel XUI
		{
			{Text: "⚙️ Configurar XUI", CallbackData: "menu:configurar"},
			{Text: "🔌 Testar XUI", CallbackData: "menu:testxui"},
		},
		// Conteúdo
		{
			{Text: "📋 Fontes M3U/Xtream", CallbackData: "menu:m3u"},
		},
		// Info
		{
			{Text: "📊 Estatísticas", CallbackData: "menu:stats"},
			{Text: "ℹ️ Sistema", CallbackData: "menu:info"},
			{Text: "🏓 Ping", CallbackData: "menu:ping"},
		},
	}

	if isAdmin {
		rows = append(rows, []InlineButton{
			{Text: "📋 Tokens", CallbackData: "menu:tokens"},
			{Text: "➕ Add Token", CallbackData: "menu:addtoken"},
		})
		rows = append(rows, []InlineButton{
			{Text: "⚙️ Configurações", CallbackData: "menu:settings"},
		})
	}

	rows = append(rows, []InlineButton{
		{Text: "ℹ️ Ajuda", CallbackData: "menu:ajuda"},
		{Text: "🔄 Atualizar", CallbackData: "menu:refresh"},
	})

	return InlineKeyboardJSON(rows)
}

func (p *Poller) helpText() string {
	return "<b>ℹ️ Como usar o Syncgo</b>\n\n" +
		"<b>Enviar conteúdo:</b>\n" +
		"Nomeie o arquivo com o ID do TMDB antes de enviar:\n" +
		"  • <code>240022.mp4</code> → Filme\n" +
		"  • <code>240022_S01E02.mp4</code> → Série S01E02\n\n" +
		"<b>Comandos principais:</b>\n" +
		"/menu — Painel de controle completo\n" +
		"/configurar — Conectar ao painel XUI\n" +
		"/m3u — Gerenciar fontes M3U/Xtream\n" +
		"/stats — Estatísticas\n" +
		"/info — Info do sistema\n" +
		"/ping — Verificar latência\n" +
		"/id — Ver seu Telegram ID\n\n" +
		"<b>Comandos de admin:</b>\n" +
		"/settings — Ver todas as configurações\n" +
		"/set &lt;chave&gt; &lt;valor&gt; — Alterar configuração\n" +
		"/unset &lt;chave&gt; — Remover configuração\n" +
		"/tokens — Listar tokens de bot\n" +
		"/addtoken &lt;token&gt; — Adicionar token\n" +
		"/rmtoken &lt;id&gt; — Remover token\n\n" +
		"<b>ID do TMDB:</b> acesse <a href=\"https://www.themoviedb.org\">themoviedb.org</a> e copie o número da URL."
}

func hasMedia(m *Message) bool {
	return m.Document != nil || m.Video != nil || m.Audio != nil ||
		m.Voice != nil || m.Animation != nil || m.VideoNote != nil ||
		m.Sticker != nil || len(m.Photo) > 0
}

func (p *Poller) handleStart(ctx context.Context, m *Message) error {
	if m.From != nil {
		_ = p.deps.DB.AddUser(ctx, m.From.ID, displayName(m.From))
	}

	name := "usuário"
	if m.From != nil && m.From.FirstName != "" {
		name = m.From.FirstName
	}

	text := fmt.Sprintf(
		"👋 Olá, <b>%s</b>! Bem-vindo ao <b>Syncgo</b>.\n\n"+
			"Este bot transforma vídeos do Telegram em links de streaming e os adiciona automaticamente ao seu painel.\n\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"📁 <b>Como enviar um vídeo</b>\n\n"+
			"Nomeie o arquivo com o ID do filme ou série antes de enviar:\n\n"+
			"🎬 <b>Filme:</b>\n"+
			"  <code>240022.mp4</code>\n\n"+
			"📺 <b>Série / Episódio:</b>\n"+
			"  <code>240022_S01E02.mp4</code>\n\n"+
			"💡 Encontre o ID em <a href=\"https://www.themoviedb.org\">themoviedb.org</a> — está na URL da página.\n\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"🔧 <b>Primeira vez?</b> Configure o painel com /configurar\n\n"+
			"📋 <b>Comandos disponíveis:</b>\n"+
			"/menu — Painel de controle\n"+
			"/configurar — Conectar ao painel\n"+
			"/stats — Estatísticas\n"+
			"/ping — Verificar bot\n"+
			"/id — Ver seu ID",
		util.HTMLEscape(name),
	)

	var markup string
	_, hasHost := p.deps.DB.GetSetting(ctx, "xui_host")
	if !hasHost {
		markup = InlineKeyboardJSON([][]InlineButton{
			{{Text: "⚙️ Configurar agora", CallbackData: "menu:configurar"}},
			{{Text: "📋 Ver painel", CallbackData: "menu:status"}},
		})
	} else {
		markup = InlineKeyboardJSON([][]InlineButton{
			{{Text: "📋 Ver painel de controle", CallbackData: "menu:status"}},
		})
	}

	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:      m.Chat.ID,
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: markup,
	})
	return err
}

func (p *Poller) handleMedia(ctx context.Context, m *Message) error {
	fileID, fileName, fileSize, mimeType, _ := extractMediaInfo(m)
	if fileID == "" {
		return fmt.Errorf("media without file_id")
	}

	fwd, err := p.deps.API.ForwardMessage(ctx, ForwardMessageParams{
		ChatID:     p.deps.LogChannel,
		FromChatID: m.Chat.ID,
		MessageID:  m.MessageID,
	})
	if err != nil {
		return fmt.Errorf("forward: %w", err)
	}

	logMsgID := fwd.MessageID
	secureHash := util.SecureHash(int64(logMsgID), p.deps.HashSecret)

	if err := p.deps.DB.SaveFile(ctx, database.FileRecord{
		MessageID:  int64(logMsgID),
		FileName:   fileName,
		FileSize:   fileSize,
		MimeType:   mimeType,
		SecureHash: secureHash,
		OwnerID:    ownerID(m),
	}); err != nil {
		p.deps.Logger.Error("save file", "err", err)
	}

	dlURL, streamURL := p.urlsFor(secureHash, logMsgID)
	body, markup := p.buildLinkMessage(fileName, fileSize, dlURL, streamURL)

	if _, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:                m.Chat.ID,
		Text:                  body,
		ParseMode:             "HTML",
		ReplyToMessageID:      m.MessageID,
		DisableWebPagePreview: true,
		ReplyMarkup:           markup,
	}); err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:                p.deps.LogChannel,
		Text:                  body,
		ParseMode:             "HTML",
		ReplyToMessageID:      logMsgID,
		DisableWebPagePreview: true,
		ReplyMarkup:           markup,
	})

	p.startInsertSession(ctx, fileName, dlURL, m.Chat.ID, m.MessageID)
	return nil
}

func (p *Poller) handleChannelPost(ctx context.Context, m *Message) error {
	if m.Chat.ID != p.deps.LogChannel || !hasMedia(m) {
		return nil
	}
	_, fileName, fileSize, mimeType, _ := extractMediaInfo(m)
	logMsgID := m.MessageID
	secureHash := util.SecureHash(int64(logMsgID), p.deps.HashSecret)

	if err := p.deps.DB.SaveFile(ctx, database.FileRecord{
		MessageID:  int64(logMsgID),
		FileName:   fileName,
		FileSize:   fileSize,
		MimeType:   mimeType,
		SecureHash: secureHash,
		OwnerID:    0,
	}); err != nil {
		p.deps.Logger.Error("save file (channel)", "err", err)
	}

	dlURL, streamURL := p.urlsFor(secureHash, logMsgID)
	body, markup := p.buildLinkMessage(fileName, fileSize, dlURL, streamURL)
	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:                p.deps.LogChannel,
		Text:                  body,
		ParseMode:             "HTML",
		ReplyToMessageID:      logMsgID,
		DisableWebPagePreview: true,
		ReplyMarkup:           markup,
	})
	return err
}

func (p *Poller) urlsFor(secureHash string, msgID int) (dl, stream string) {
	dl = fmt.Sprintf("%s/%s%d", p.deps.URLBase, secureHash, msgID)
	stream = fmt.Sprintf("%s/watch/%s%d", p.deps.URLBase, secureHash, msgID)
	return
}

func (p *Poller) buildLinkMessage(fileName string, fileSize int64, dlURL, streamURL string) (body, markup string) {
	body = fmt.Sprintf(
		"<b>Link gerado!</b>\n\n📂 <b>Arquivo:</b> %s\n📦 <b>Tamanho:</b> %s\n\n📥 <b>Download:</b> %s\n🖥 <b>Assistir:</b> %s",
		util.HTMLEscape(displayFileName(fileName)),
		util.HumanBytes(fileSize),
		dlURL,
		streamURL,
	)
	if util.IsPublicURL(dlURL) {
		markup = InlineKeyboardJSON([][]InlineButton{
			{{Text: "📥 Download", URL: dlURL}, {Text: "🖥 Assistir", URL: streamURL}},
		})
	}
	return
}

// ── Insert session flow ──────────────────────────────────────────────────────

// startInsertSession cria uma sessão de confirmação ou insere diretamente (modo auto).
func (p *Poller) startInsertSession(ctx context.Context, fileName, streamURL string, chatID int64, replyMsgID int) {
	if p.deps.Importer == nil {
		return
	}

	// Modo automático: insere diretamente usando bouquets padrão configurados.
	if mode, _ := p.deps.DB.GetSetting(ctx, "insert_mode"); mode == "auto" {
		parsed := parseToSession(fileName)
		sess := &insertSession{
			Key: sessKey(chatID, replyMsgID), FileName: fileName, StreamURL: streamURL,
			ChatID: chatID, ReplyMsgID: replyMsgID,
			Kind: parsed.Kind, TMDBID: parsed.TMDBID, Season: parsed.Season, Episode: parsed.Episode,
		}
		var bqID int64
		if parsed.Kind == ctMovie {
			bqID = p.deps.DB.GetSettingInt64(ctx, "default_movie_bouquet_id", 0)
		} else {
			bqID = p.deps.DB.GetSettingInt64(ctx, "default_series_bouquet_id", 0)
		}
		p.doInsert(ctx, sess, bqID)
		return
	}

	parsed := parseToSession(fileName)
	sess := &insertSession{
		Key:        sessKey(chatID, replyMsgID),
		FileName:   fileName,
		StreamURL:  streamURL,
		ChatID:     chatID,
		ReplyMsgID: replyMsgID,
		Kind:       parsed.Kind,
		TMDBID:     parsed.TMDBID,
		Season:     parsed.Season,
		Episode:    parsed.Episode,
		State:      stConfirm,
		Expires:    time.Now().Add(15 * time.Minute),
	}
	p.sessions.set(sess)

	text, kb := p.renderConfirm(sess)
	sent, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:           chatID,
		Text:             text,
		ParseMode:        "HTML",
		ReplyToMessageID: replyMsgID,
		ReplyMarkup:      kb,
	})
	if err == nil && sent != nil {
		sess.ConfirmMsgID = sent.MessageID
		p.sessions.set(sess)
	}
}

func (p *Poller) renderConfirm(sess *insertSession) (text, keyboard string) {
	k := sess.Key
	var sb strings.Builder
	sb.WriteString("<b>📋 Adicionar ao painel — Confirmação</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(&sb, "📁 <b>Arquivo:</b> %s\n", util.HTMLEscape(displayFileName(sess.FileName)))
	fmt.Fprintf(&sb, "📋 <b>Tipo:</b> %s\n", sess.Kind.label())
	if sess.TMDBID != 0 {
		fmt.Fprintf(&sb, "🆔 <b>TMDB ID:</b> <code>%d</code>\n", sess.TMDBID)
	} else {
		sb.WriteString("🆔 <b>TMDB ID:</b> ❓ <i>(não detectado)</i>\n")
	}
	if sess.Kind == ctEpisode {
		fmt.Fprintf(&sb, "📅 <b>Temp.</b> %d  •  <b>Ep.</b> %d\n", sess.Season, sess.Episode)
	}
	text = sb.String()

	var rows [][]InlineButton
	// Linha 1: tipo
	if sess.Kind != ctMovie {
		rows = append(rows, []InlineButton{
			{Text: "🎬 Filme", CallbackData: fmt.Sprintf("xi:tm:%s", k)},
			{Text: "📺 Série", CallbackData: fmt.Sprintf("xi:ts:%s", k)},
		})
	} else {
		rows = append(rows, []InlineButton{
			{Text: "🎬 Filme ✓", CallbackData: fmt.Sprintf("xi:tm:%s", k)},
			{Text: "📺 Série", CallbackData: fmt.Sprintf("xi:ts:%s", k)},
		})
	}
	// Linha 2: editar campos
	editRow := []InlineButton{{Text: "✏️ TMDB ID", CallbackData: fmt.Sprintf("xi:et:%s", k)}}
	if sess.Kind == ctEpisode {
		editRow = append(editRow, InlineButton{Text: "✏️ Temp/Ep", CallbackData: fmt.Sprintf("xi:es:%s", k)})
	}
	rows = append(rows, editRow)
	// Linha 3: confirmar / cancelar
	rows = append(rows, []InlineButton{
		{Text: "✅ Confirmar", CallbackData: fmt.Sprintf("xi:ok:%s", k)},
		{Text: "❌ Cancelar", CallbackData: fmt.Sprintf("xi:no:%s", k)},
	})
	keyboard = InlineKeyboardJSON(rows)
	return
}

func (p *Poller) handleCallbackQuery(ctx context.Context, cb *CallbackQuery) error {
	data := cb.Data

	// Callbacks do menu principal.
	if strings.HasPrefix(data, "menu:") {
		_ = p.deps.API.AnswerCallbackQuery(ctx, cb.ID, "")
		action := strings.TrimPrefix(data, "menu:")
		var chatID int64
		if cb.Message != nil {
			chatID = cb.Message.Chat.ID
		}
		if chatID == 0 {
			return nil
		}
		isAdmin := p.isOwnerOrAdmin(chatID)

		switch action {
		case "configurar":
			p.startSetup(ctx, chatID)

		case "m3u":
			return p.sendM3UMenu(ctx, chatID)

		// Legacy — mantido por compatibilidade
		case "status":
			if cb.Message != nil {
				p.editMenu(ctx, chatID, cb.Message.MessageID)
			} else {
				_ = p.sendMenu(ctx, chatID)
			}

		case "refresh":
			if cb.Message != nil {
				p.editMenu(ctx, chatID, cb.Message.MessageID)
			}

		case "testxui":
			go func() {
				msg := p.pingAndRebuildXUI(ctx)
				_, _ = p.deps.API.SendMessage(context.Background(), SendMessageParams{
					ChatID: chatID, Text: msg, ParseMode: "HTML",
				})
			}()

		case "stats":
			st, err := p.deps.DB.Stats(ctx)
			if err != nil {
				_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{ChatID: chatID, Text: "❌ " + err.Error()})
				break
			}
			uptime := time.Since(p.startTime).Round(time.Second)
			body := fmt.Sprintf(
				"<b>📊 Estatísticas</b>\n\n"+
					"📁 <b>Arquivos:</b> %d\n"+
					"👤 <b>Usuários:</b> %d\n"+
					"📦 <b>Total:</b> %s\n"+
					"⏱ <b>Uptime:</b> %s",
				st.Files, st.Users, util.HumanBytes(st.TotalBytes), uptime)
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{ChatID: chatID, Text: body, ParseMode: "HTML"})

		case "info":
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID: chatID, Text: p.menuText(ctx), ParseMode: "HTML",
			})

		case "ping":
			start := time.Now()
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID: chatID,
				Text:   fmt.Sprintf("🏓 pong (%.0fms)", float64(time.Since(start).Microseconds())/1000),
			})

		case "ajuda":
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID: chatID, Text: p.helpText(), ParseMode: "HTML",
				DisableWebPagePreview: true,
			})

		// ── Admin-only ──────────────────────────────────────────────────
		case "tokens":
			if !isAdmin {
				break
			}
			_ = p.handleListTokens(ctx, &Message{Chat: Chat{ID: chatID}, From: &User{ID: chatID}})

		case "addtoken":
			if !isAdmin {
				break
			}
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID:    chatID,
				ParseMode: "HTML",
				Text: "➕ <b>Adicionar Token</b>\n\nEnvie o comando:\n" +
					"<code>/addtoken 123456789:AABBcc...</code>\n\n" +
					"O token será validado, salvo no banco e ativado imediatamente.",
			})

		case "settings":
			if !isAdmin {
				break
			}
			_ = p.handleSettings(ctx, &Message{Chat: Chat{ID: chatID}, From: &User{ID: chatID}})
		}
		return nil
	}

	// Callbacks de gerenciamento de M3U.
	if strings.HasPrefix(data, "m3u:") {
		_ = p.deps.API.AnswerCallbackQuery(ctx, cb.ID, "")
		return p.handleM3UCallback(ctx, cb, strings.TrimPrefix(data, "m3u:"))
	}

	// Callbacks do wizard de configuração.
	if strings.HasPrefix(data, "setup:") {
		_ = p.deps.API.AnswerCallbackQuery(ctx, cb.ID, "")
		p.handleSetupCallback(ctx, cb, strings.TrimPrefix(data, "setup:"))
		return nil
	}

	_ = p.deps.API.AnswerCallbackQuery(ctx, cb.ID, "")

	if !strings.HasPrefix(data, "xi:") {
		return nil
	}
	// format: xi:{action}:{sessKey}  or  xi:bq:{sessKey}:{bouquetID}
	parts := strings.SplitN(strings.TrimPrefix(data, "xi:"), ":", 2)
	if len(parts) < 2 {
		return nil
	}
	action, rest := parts[0], parts[1]

	// bouquet selection has an extra segment: {sessKey}:{bouquetID}
	var sessKeyStr string
	var bouquetID int64
	if action == "bq" {
		lastColon := strings.LastIndex(rest, ":")
		if lastColon < 0 {
			return nil
		}
		sessKeyStr = rest[:lastColon]
		bouquetID, _ = strconv.ParseInt(rest[lastColon+1:], 10, 64)
	} else {
		sessKeyStr = rest
	}

	// Resolve session by key → chatID
	// Key format: {chatID}:{msgID}
	chatIDStr, _, found := strings.Cut(sessKeyStr, ":")
	if !found {
		return nil
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return nil
	}

	sess, ok := p.sessions.get(chatID)
	if !ok || sess.Key != sessKeyStr {
		if cb.Message != nil {
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID: cb.Message.Chat.ID,
				Text:   "⚠️ Sessão expirada. Reenvie o arquivo.",
			})
		}
		return nil
	}

	switch action {
	case "tm":
		sess.Kind = ctMovie
		sess.Season, sess.Episode = 0, 0
		sess.State = stConfirm
		p.sessions.set(sess)
		p.refreshConfirm(ctx, sess)

	case "ts":
		sess.Kind = ctEpisode
		if sess.Season == 0 {
			sess.Season = 1
		}
		if sess.Episode == 0 {
			sess.Episode = 1
		}
		sess.State = stConfirm
		p.sessions.set(sess)
		p.refreshConfirm(ctx, sess)

	case "et":
		sess.State = stEditTMDB
		p.sessions.set(sess)
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    sess.ChatID,
			Text:      "✏️ Digite o novo <b>TMDB ID</b> (somente números):",
			ParseMode: "HTML",
		})

	case "es":
		sess.State = stEditSE
		p.sessions.set(sess)
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    sess.ChatID,
			Text:      "✏️ Digite a temporada e episódio no formato <code>S01E02</code>:",
			ParseMode: "HTML",
		})

	case "ok":
		if sess.TMDBID == 0 {
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID:    sess.ChatID,
				Text:      "⚠️ Defina o TMDB ID antes de confirmar.",
				ParseMode: "HTML",
			})
			return nil
		}
		// Avança para seleção de bouquet
		p.askBouquet(ctx, sess)

	case "bq":
		p.sessions.del(sess.ChatID)
		if cb.Message != nil {
			_ = p.deps.API.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, `{"inline_keyboard":[]}`)
		}
		p.doInsert(ctx, sess, bouquetID)

	case "no":
		p.sessions.del(sess.ChatID)
		if sess.ConfirmMsgID != 0 {
			_ = p.deps.API.EditMessageReplyMarkup(ctx, sess.ChatID, sess.ConfirmMsgID, `{"inline_keyboard":[]}`)
		}
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID: sess.ChatID,
			Text:   "❌ Inserção cancelada.",
		})
	}
	return nil
}

// handleEditInput intercepta mensagens de texto quando o usuário está em modo de edição.
// Retorna true se a mensagem foi consumida pela sessão.
func (p *Poller) handleEditInput(ctx context.Context, m *Message) bool {
	if m.Text == "" || m.From == nil {
		return false
	}
	sess, ok := p.sessions.get(m.Chat.ID)
	if !ok || sess.State == stConfirm {
		return false
	}

	text := strings.TrimSpace(m.Text)
	switch sess.State {
	case stEditTMDB:
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil || id <= 0 {
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID:    m.Chat.ID,
				Text:      "⚠️ TMDB ID inválido. Digite somente números (ex: <code>240022</code>).",
				ParseMode: "HTML",
			})
			return true
		}
		sess.TMDBID = id
		sess.State = stConfirm
		p.sessions.set(sess)
		p.refreshConfirm(ctx, sess)

	case stEditSE:
		var s, e int
		// aceita S01E02, s01e02, 1x02, ou "1 2"
		if _, err := fmt.Sscanf(strings.ToUpper(text), "S%dE%d", &s, &e); err != nil {
			if _, err2 := fmt.Sscanf(text, "%dx%d", &s, &e); err2 != nil {
				if _, err3 := fmt.Sscanf(text, "%d %d", &s, &e); err3 != nil {
					_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
						ChatID:    m.Chat.ID,
						Text:      "⚠️ Formato inválido. Use <code>S01E02</code>, <code>1x02</code> ou <code>1 2</code>.",
						ParseMode: "HTML",
					})
					return true
				}
			}
		}
		if s <= 0 || e <= 0 {
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID: m.Chat.ID, Text: "⚠️ Temporada e episódio devem ser maiores que zero.",
			})
			return true
		}
		sess.Kind = ctEpisode
		sess.Season = s
		sess.Episode = e
		sess.State = stConfirm
		p.sessions.set(sess)
		p.refreshConfirm(ctx, sess)
	}
	return true
}

func (p *Poller) refreshConfirm(ctx context.Context, sess *insertSession) {
	text, kb := p.renderConfirm(sess)
	if sess.ConfirmMsgID != 0 {
		_ = p.deps.API.EditMessageText(ctx, EditMessageTextParams{
			ChatID:      sess.ChatID,
			MessageID:   sess.ConfirmMsgID,
			Text:        text,
			ParseMode:   "HTML",
			ReplyMarkup: kb,
		})
	}
}

func (p *Poller) askBouquet(ctx context.Context, sess *insertSession) {
	bouquets, err := p.deps.Importer.ListBouquets(ctx)
	if err != nil || len(bouquets) == 0 {
		// sem bouquets → insere direto com padrão
		p.sessions.del(sess.ChatID)
		p.doInsert(ctx, sess, 0)
		return
	}

	var rows [][]InlineButton
	var row []InlineButton
	for _, b := range bouquets {
		row = append(row, InlineButton{
			Text:         b.Name,
			CallbackData: fmt.Sprintf("xi:bq:%s:%d", sess.Key, b.ID),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []InlineButton{{
		Text:         "⏭ Sem categoria (padrão)",
		CallbackData: fmt.Sprintf("xi:bq:%s:0", sess.Key),
	}})

	_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:      sess.ChatID,
		Text:        "📂 <b>Selecione a categoria:</b>",
		ParseMode:   "HTML",
		ReplyMarkup: InlineKeyboardJSON(rows),
	})
}

func (p *Poller) doInsert(ctx context.Context, sess *insertSession, bouquetID int64) {
	if p.deps.Importer == nil {
		return
	}
	// Constrói um fileName sintético a partir dos dados da sessão para o importer.
	// O importer usa o parser para detectar tipo/TMDB/S/E — por isso usamos o
	// nome original se os dados baterem, ou construímos um nome normalizado.
	fileName := buildFileName(sess)
	res, err := p.deps.Importer.HandleUpload(ctx, fileName, sess.StreamURL, bouquetID)
	if err != nil {
		p.deps.Logger.Error("auto-insert failed", "err", err, "file", sess.FileName)
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:           sess.ChatID,
			Text:             fmt.Sprintf("⚠️ Não consegui adicionar ao painel: %s", util.HTMLEscape(err.Error())),
			ParseMode:        "HTML",
			ReplyToMessageID: sess.ReplyMsgID,
		})
		return
	}
	if res == nil || res.Kind == "skipped" {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:           sess.ChatID,
			Text:             "ℹ️ Não reconheci o arquivo — nomeie como: <code>123456.mp4</code>",
			ParseMode:        "HTML",
			ReplyToMessageID: sess.ReplyMsgID,
		})
		return
	}
	var msg string
	switch res.Kind {
	case "movie":
		if res.WasExisting {
			msg = "✅ Filme já estava no painel — link atualizado."
		} else {
			msg = fmt.Sprintf("✅ Filme adicionado ao painel: <b>%s</b>", util.HTMLEscape(res.Title))
		}
	case "episode":
		if res.WasExisting {
			msg = fmt.Sprintf("✅ Episódio S%02dE%02d já estava no painel.", res.Season, res.Episode)
		} else {
			msg = fmt.Sprintf("✅ Episódio adicionado: <b>%s</b> S%02dE%02d",
				util.HTMLEscape(res.Title), res.Season, res.Episode)
		}
	}
	_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:           sess.ChatID,
		Text:             msg,
		ParseMode:        "HTML",
		ReplyToMessageID: sess.ReplyMsgID,
	})
}

// buildFileName constrói um nome de arquivo normalizado para o importer a partir da sessão.
func buildFileName(sess *insertSession) string {
	switch sess.Kind {
	case ctMovie:
		return fmt.Sprintf("%d.mp4", sess.TMDBID)
	case ctEpisode:
		return fmt.Sprintf("%d_S%02dE%02d.mp4", sess.TMDBID, sess.Season, sess.Episode)
	default:
		return sess.FileName
	}
}

// pingAndRebuildXUI testa a conexão MySQL do XUI e, se bem-sucedido, reconstrói
// o importer para que auto-insert volte a funcionar (útil quando MySQL volta online).
func (p *Poller) pingAndRebuildXUI(ctx context.Context) string {
	host, _ := p.deps.DB.GetSetting(ctx, "xui_host")
	if host == "" {
		return "❌ Painel não configurado — use /configurar primeiro"
	}
	user, _ := p.deps.DB.GetSetting(ctx, "xui_user")
	pass, _ := p.deps.DB.GetSetting(ctx, "xui_password")
	dbName, _ := p.deps.DB.GetSetting(ctx, "xui_database")
	portStr, _ := p.deps.DB.GetSetting(ctx, "xui_port")
	port := 3306
	if n, err := strconv.Atoi(portStr); err == nil && n > 0 {
		port = n
	}
	if dbName == "" {
		dbName = "xui"
	}

	type result struct {
		db      *xui.DB
		latency time.Duration
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		start := time.Now()
		db, err := xui.Open(xui.Config{
			Host: host, Port: port, User: user, Password: pass,
			Database: dbName, ServerID: 1,
		})
		ch <- result{db, time.Since(start), err}
	}()

	var res result
	select {
	case res = <-ch:
	case <-time.After(8 * time.Second):
		return fmt.Sprintf("⏱ <b>Painel não respondeu</b> (&gt;8s)\n<code>%s:%d</code> sem resposta", host, port)
	}

	if res.err != nil {
		p.deps.Importer = nil
		return fmt.Sprintf("🔴 <b>Painel offline</b>\n<code>%s:%d</code>\n\n<i>%s</i>",
			host, port, util.HTMLEscape(res.err.Error()))
	}

	// Reconstrói importer para auto-insert funcionar
	rebuilt := ""
	if p.deps.Cfg != nil && p.deps.Cfg.TMDBAPIKey != "" {
		tmdbClient := tmdb.New(p.deps.Cfg.TMDBAPIKey, p.deps.Cfg.TMDBLanguage)
		p.deps.Importer = importer.New(res.db, tmdbClient, nil, p.deps.Logger)
		rebuilt = "\n✅ <b>Adição automática ao painel reativada!</b>"
	} else {
		res.db.Close()
		rebuilt = "\n⚠️ Chave de filmes não configurada — adição automática permanece inativa"
	}

	return fmt.Sprintf("🟢 <b>Painel conectado</b> — <code>%s:%d</code>\n⏱ Latência: <b>%dms</b>%s",
		host, port, res.latency.Milliseconds(), rebuilt)
}

func extractMediaInfo(m *Message) (fileID, fileName string, fileSize int64, mimeType, uniqueID string) {
	switch {
	case m.Video != nil:
		return m.Video.FileID, m.Video.FileName, m.Video.FileSize, m.Video.MimeType, m.Video.FileUniqueID
	case m.Document != nil:
		return m.Document.FileID, m.Document.FileName, m.Document.FileSize, m.Document.MimeType, m.Document.FileUniqueID
	case m.Audio != nil:
		name := m.Audio.FileName
		if name == "" {
			name = "audio.mp3"
		}
		return m.Audio.FileID, name, m.Audio.FileSize, m.Audio.MimeType, m.Audio.FileUniqueID
	case m.Voice != nil:
		return m.Voice.FileID, "voice.ogg", m.Voice.FileSize, defaultMime(m.Voice.MimeType, "audio/ogg"), m.Voice.FileUniqueID
	case m.Animation != nil:
		name := m.Animation.FileName
		if name == "" {
			name = "animation.mp4"
		}
		return m.Animation.FileID, name, m.Animation.FileSize, defaultMime(m.Animation.MimeType, "video/mp4"), m.Animation.FileUniqueID
	case m.VideoNote != nil:
		return m.VideoNote.FileID, "video_note.mp4", m.VideoNote.FileSize, "video/mp4", m.VideoNote.FileUniqueID
	case m.Sticker != nil:
		ext := "webp"
		mt := "image/webp"
		if m.Sticker.IsVideo {
			ext = "webm"
			mt = "video/webm"
		} else if m.Sticker.IsAnimated {
			ext = "tgs"
			mt = "application/x-tgsticker"
		}
		return m.Sticker.FileID, "sticker." + ext, m.Sticker.FileSize, mt, m.Sticker.FileUniqueID
	case len(m.Photo) > 0:
		largest := m.Photo[0]
		for _, ph := range m.Photo[1:] {
			if ph.FileSize > largest.FileSize {
				largest = ph
			}
		}
		return largest.FileID, "photo.jpg", largest.FileSize, "image/jpeg", largest.FileUniqueID
	}
	return "", "", 0, "", ""
}

func defaultMime(have, fallback string) string {
	if have != "" {
		return have
	}
	return fallback
}

func ownerID(m *Message) int64 {
	if m == nil || m.From == nil {
		return 0
	}
	return m.From.ID
}

func displayName(u *User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if name == "" {
		name = u.Username
	}
	if name == "" {
		name = fmt.Sprintf("user-%d", u.ID)
	}
	return name
}

func displayFileName(name string) string {
	if name == "" {
		return "(sem nome)"
	}
	return filepath.Base(name)
}
