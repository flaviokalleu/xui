package botapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"syncgo/internal/database"
	"syncgo/internal/importer"
	"syncgo/internal/util"
	"syncgo/internal/xui"
)

// buildXtreamImporter cria um XtreamImporter com a conexão XUI atual.
func (p *Poller) buildXtreamImporter(ctx context.Context) (*importer.XtreamImporter, error) {
	host, _ := p.deps.DB.GetSetting(ctx, "xui_host")
	user, _ := p.deps.DB.GetSetting(ctx, "xui_user")
	pass, _ := p.deps.DB.GetSetting(ctx, "xui_password")
	dbName, _ := p.deps.DB.GetSetting(ctx, "xui_database")
	portStr, _ := p.deps.DB.GetSetting(ctx, "xui_port")
	adminIDStr, _ := p.deps.DB.GetSetting(ctx, "xui_admin_id")
	port := 3306
	if n, err := strconv.Atoi(portStr); err == nil && n > 0 {
		port = n
	}
	adminID := p.deps.Config.XUIAdminID
	if n, err := strconv.Atoi(adminIDStr); err == nil && n > 0 {
		adminID = n
	}
	if dbName == "" {
		dbName = "xui"
	}
	if host == "" {
		return nil, fmt.Errorf("painel não configurado — use /configurar primeiro")
	}
	db, err := xui.Open(xui.Config{
		Host: host, Port: port, User: user, Password: pass,
		Database: dbName, ServerID: 1, AdminID: adminID,
	})
	if err != nil {
		return nil, fmt.Errorf("conexão com o painel falhou: %w", err)
	}
	return importer.NewXtreamImporter(db, p.deps.Logger), nil
}

// downloadStats acompanha bytes baixados, uploads e inserções no XUI.
type downloadStats struct {
	bytes   atomic.Int64
	ok      atomic.Int64
	failed  atomic.Int64
	xuiIns  atomic.Int64 // inseridos no XUI
	xuiUpd  atomic.Int64 // atualizados no XUI
	// Progresso do arquivo atual (zerado a cada novo arquivo)
	curName  atomic.Value // string — nome do arquivo sendo processado
	curPhase atomic.Value // string — "baixando" | "enviando"
	curSent  atomic.Int64 // bytes transferidos no arquivo atual
	curTotal atomic.Int64 // tamanho total do arquivo atual (0 = desconhecido)
}

// progressReader é um io.Reader que chama onChange a cada leitura.
type progressReader struct {
	r      io.Reader
	onRead func(n int64)
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 && p.onRead != nil {
		p.onRead(int64(n))
	}
	return n, err
}

// makeDownloadAndUploadFunc retorna um UploadFunc que:
//  1. Verifica se o item já foi enviado ao Telegram (status="done" no SQLite) — pula se sim.
//  2. Faz GET do arquivo no servidor Xtream.
//  3. Envia ao LOG_CHANNEL via MTProto (sem limite de 50MB) se disponível, ou Bot API HTTP como fallback.
//  4. Grava o registro no SQLite (xtream_downloads) com a URL final do Syncgo.
//  5. Retorna a URL de streaming para o importer usar no XUI (se MySQL disponível).
func (p *Poller) makeDownloadAndUploadFunc(sourceID int64, stats *downloadStats) importer.UploadFunc {
	client := &http.Client{Timeout: 6 * time.Hour}
	db := p.deps.DB
	log := p.deps.Logger
	useMTProto := p.deps.TGPool != nil && p.deps.LogChannelPeer != nil
	return func(ctx context.Context, srcURL, name string) (string, error) {
		// Já enviado anteriormente — reutiliza URL gerada
		existing, _ := db.XtreamDownloadGet(ctx, sourceID, srcURL)
		if existing != nil && existing.Status == "done" {
			stats.ok.Add(1)
			stats.bytes.Add(existing.FileSize)
			return existing.FinalURL, nil
		}

		kind := xtreamKind(srcURL)

		saveErr := func(msg string) {
			stats.failed.Add(1)
			stats.curName.Store("")
			_ = db.XtreamDownloadSave(ctx, database.XtreamDownload{
				SourceID: sourceID, StreamURL: srcURL, Name: name,
				Kind: kind, Status: "error", ErrorMsg: msg,
			})
		}

		// Download do arquivo Xtream
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srcURL, nil)
		if err != nil {
			saveErr(err.Error())
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")

		log.Info("xtream: downloading", "url", srcURL, "name", name)
		resp, err := client.Do(req)
		if err != nil {
			saveErr(err.Error())
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
			log.Warn("xtream: download failed", "url", srcURL, "status", resp.StatusCode, "content-type", resp.Header.Get("Content-Type"))
			saveErr(msg)
			return "", fmt.Errorf("%s", msg)
		}
		log.Info("xtream: download started", "url", srcURL, "content-type", resp.Header.Get("Content-Type"), "content-length", resp.ContentLength)

		contentType := resp.Header.Get("Content-Type")
		ext := xtreamExt(srcURL, contentType)
		fileName := xtreamSafeName(name) + ext

		// Inicia rastreamento de progresso por arquivo
		stats.curName.Store(fileName)
		stats.curPhase.Store("baixando")
		stats.curSent.Store(0)
		if resp.ContentLength > 0 {
			stats.curTotal.Store(resp.ContentLength)
		} else {
			stats.curTotal.Store(0)
		}

		var logMsgID int
		var fileSize int64

		if useMTProto {
			// Wrapping do body para rastrear bytes baixados (fase 1: Xtream → disco)
			pr := &progressReader{
				r: resp.Body,
				onRead: func(n int64) {
					stats.curSent.Add(n)
				},
			}
			// MTProto upload: sem limite de 50MB — baixa para disco e envia em chunks
			logMsgID, err = UploadDocumentMTProto(ctx, p.deps.TGPool, p.deps.LogChannelPeer, fileName, pr, func(sent, total int64) {
				fileSize = total
				// Fase 2: disco → Telegram
				stats.curPhase.Store("enviando")
				stats.curSent.Store(sent)
				stats.curTotal.Store(total)
			})
			if err != nil {
				saveErr(fmt.Sprintf("mtproto: %s", err.Error()))
				return "", fmt.Errorf("mtproto upload: %w", err)
			}
		} else {
			// Fallback: Bot API HTTP (limite de 50MB)
			counter := &countingReader{r: resp.Body}
			tgMsg, tgErr := p.deps.API.SendDocumentStream(ctx, p.deps.LogChannel, fileName, counter, resp.ContentLength)
			if tgErr != nil {
				saveErr(fmt.Sprintf("telegram: %s", tgErr.Error()))
				return "", fmt.Errorf("telegram upload: %w", tgErr)
			}
			logMsgID = tgMsg.MessageID
			fileSize = counter.n
		}
		stats.curName.Store("") // limpa ao terminar

		log.Info("xtream: uploaded to telegram", "name", fileName, "bytes", fileSize, "msg_id", logMsgID)

		secureHash := util.SecureHash(int64(logMsgID), p.deps.HashSecret)
		finalURL := fmt.Sprintf("%s/%s%d", p.deps.URLBase, secureHash, logMsgID)

		// Salva no banco de arquivos do Syncgo
		_ = db.SaveFile(ctx, database.FileRecord{
			MessageID:  int64(logMsgID),
			FileName:   fileName,
			FileSize:   fileSize,
			MimeType:   contentType,
			SecureHash: secureHash,
		})
		// Registra no histórico de downloads Xtream
		_ = db.XtreamDownloadSave(ctx, database.XtreamDownload{
			SourceID:  sourceID,
			StreamURL: srcURL,
			Name:      name,
			Kind:      kind,
			FileSize:  fileSize,
			Status:    "done",
			TgMsgID:   int64(logMsgID),
			FinalURL:  finalURL,
		})
		stats.bytes.Add(fileSize)
		stats.ok.Add(1)
		return finalURL, nil
	}
}

// countingReader conta bytes lidos (usado para registrar tamanho do arquivo enviado).
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// xtreamExt tenta adivinhar a extensão correta pelo URL ou Content-Type.
func xtreamExt(srcURL, contentType string) string {
	lower := strings.ToLower(srcURL)
	for _, ext := range []string{".mkv", ".mp4", ".avi", ".mov", ".ts", ".m4v", ".wmv", ".flv"} {
		if strings.Contains(lower, ext) {
			return ext
		}
	}
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "matroska"):
		return ".mkv"
	case strings.Contains(ct, "mp4"):
		return ".mp4"
	case strings.Contains(ct, "mpeg"), strings.Contains(ct, "/ts"):
		return ".ts"
	}
	return ".mp4"
}

// xtreamKind retorna "episode" para URLs de séries, "movie" para o restante.
func xtreamKind(srcURL string) string {
	if strings.Contains(srcURL, "/series/") {
		return "episode"
	}
	return "movie"
}

// xtreamSafeName sanitiza o nome do arquivo removendo caracteres inválidos.
func xtreamSafeName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			sb.WriteRune('_')
		default:
			sb.WriteRune(r)
		}
	}
	s := sb.String()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// sendM3UMenu exibe o menu de gerenciamento de fontes M3U.
func (p *Poller) sendM3UMenu(ctx context.Context, chatID int64) error {
	sources, err := p.deps.DB.ListM3USources(ctx)
	if err != nil {
		return fmt.Errorf("list m3u sources: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("📋 <b>Minhas Fontes</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")

	var rows [][]InlineButton

	if len(sources) == 0 {
		sb.WriteString("Nenhuma fonte cadastrada ainda.\nClique em <b>➕ Adicionar</b> para cadastrar uma URL M3U.")
	} else {
		for _, s := range sources {
			name := s.Name
			if name == "" {
				name = fmt.Sprintf("Fonte #%d", s.ID)
			}
			syncInfo := "nunca atualizado"
			if s.LastSync != nil {
				syncInfo = fmt.Sprintf("atualizado: %s (%d itens)", s.LastSync.Format("02/01 15:04"), s.LastCount)
			}
			isXtream := importer.IsXtreamURL(s.URL)
			tag := ""
			if isXtream {
				tag = " 🎬"
			}
			sb.WriteString(fmt.Sprintf("• <b>%s%s</b>\n  <code>%s</code>\n  %s\n\n",
				util.HTMLEscape(name), tag,
				util.HTMLEscape(truncateURL(s.URL, 60)),
				syncInfo,
			))
			if isXtream {
				// Xtream: botões de download (baixar + enviar ao Telegram)
				rows = append(rows, []InlineButton{
					{Text: fmt.Sprintf("⬇️ Filmes — %s", name), CallbackData: fmt.Sprintf("m3u:xtream:dl:movies:%d", s.ID)},
					{Text: fmt.Sprintf("⬇️ Séries — %s", name), CallbackData: fmt.Sprintf("m3u:xtream:dl:series:%d", s.ID)},
				})
				rows = append(rows, []InlineButton{
					{Text: fmt.Sprintf("⬇️ Tudo — %s", name), CallbackData: fmt.Sprintf("m3u:xtream:dl:all:%d", s.ID)},
					{Text: "🗑 Remover", CallbackData: fmt.Sprintf("m3u:del:%d", s.ID)},
				})
			} else {
				rows = append(rows, []InlineButton{
					{Text: fmt.Sprintf("🔄 Atualizar: %s", name), CallbackData: fmt.Sprintf("m3u:sync:%d", s.ID)},
					{Text: "🗑 Remover", CallbackData: fmt.Sprintf("m3u:del:%d", s.ID)},
				})
			}
		}
		if len(sources) > 1 {
			rows = append(rows, []InlineButton{
				{Text: "🔄 Atualizar todas", CallbackData: "m3u:syncall"},
			})
		}
	}

	rows = append(rows, []InlineButton{
		{Text: "➕ Adicionar fonte", CallbackData: "m3u:add"},
	})
	rows = append(rows, []InlineButton{
		{Text: "◀️ Voltar", CallbackData: "menu:status"},
	})

	_, err = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:      chatID,
		Text:        sb.String(),
		ParseMode:   "HTML",
		ReplyMarkup: InlineKeyboardJSON(rows),
	})
	return err
}

// handleM3UCallback processa callbacks com prefixo "m3u:".
func (p *Poller) handleM3UCallback(ctx context.Context, cb *CallbackQuery, data string) error {
	chatID := int64(0)
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
	}
	if chatID == 0 {
		return nil
	}

	switch {
	case data == "add":
		p.m3uAdds.set(&m3uAddSession{
			ChatID:  chatID,
			Stage:   m3uStageURL,
			Expires: time.Now().Add(10 * time.Minute),
		})
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    chatID,
			Text:      "📋 <b>Adicionar nova fonte</b>\n\nCole o link da sua lista de conteúdo:\n\n<i>Exemplo: http://servidor.com/get.php?username=xxx&password=yyy&type=m3u_plus</i>",
			ParseMode: "HTML",
		})

	case data == "syncall":
		return p.syncAllM3U(ctx, chatID)

	case strings.HasPrefix(data, "sync:"):
		idStr := strings.TrimPrefix(data, "sync:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil
		}
		return p.syncM3USource(ctx, chatID, id)

	case strings.HasPrefix(data, "xtream:dl:movies:"):
		idStr := strings.TrimPrefix(data, "xtream:dl:movies:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil
		}
		return p.startXtreamSync(ctx, chatID, id, "movies")

	case strings.HasPrefix(data, "xtream:dl:series:"):
		idStr := strings.TrimPrefix(data, "xtream:dl:series:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil
		}
		return p.startXtreamSync(ctx, chatID, id, "series")

	case strings.HasPrefix(data, "xtream:dl:all:"):
		idStr := strings.TrimPrefix(data, "xtream:dl:all:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil
		}
		return p.startXtreamSync(ctx, chatID, id, "all")

	case strings.HasPrefix(data, "xtream:cancel:"):
		idStr := strings.TrimPrefix(data, "xtream:cancel:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil
		}
		if job, ok := p.xtreamJobs.get(id); ok {
			job.Cancel()
			p.xtreamJobs.del(id)
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID: chatID, Text: "⛔ Download cancelado.", ParseMode: "HTML",
			})
		}

	case strings.HasPrefix(data, "del:"):
		idStr := strings.TrimPrefix(data, "del:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil
		}
		return p.deleteM3USource(ctx, chatID, id)
	}
	return nil
}

// handleM3UInput intercepta mensagens de texto quando o usuário está adicionando uma URL M3U.
func (p *Poller) handleM3UInput(ctx context.Context, m *Message) bool {
	if m.Text == "" || m.From == nil {
		return false
	}
	sess, ok := p.m3uAdds.get(m.Chat.ID)
	if !ok {
		return false
	}

	text := strings.TrimSpace(m.Text)

	switch sess.Stage {
	case m3uStageURL:
		if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
			_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
				ChatID:    m.Chat.ID,
				Text:      "⚠️ URL inválida. Deve começar com <code>http://</code> ou <code>https://</code>. Tente novamente:",
				ParseMode: "HTML",
			})
			return true
		}
		sess.URL = text
		sess.Stage = m3uStageName
		sess.Expires = time.Now().Add(5 * time.Minute)
		p.m3uAdds.set(sess)
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    m.Chat.ID,
			Text:      "📝 Digite um nome para esta fonte (ou envie <code>-</code> para usar o padrão):",
			ParseMode: "HTML",
		})

	case m3uStageName:
		name := text
		if name == "-" {
			name = ""
		}
		p.m3uAdds.del(m.Chat.ID)
		_ = p.saveM3UAndOffer(ctx, m.Chat.ID, sess.URL, name)
	}
	return true
}

// saveM3UAndOffer salva a fonte no banco e pergunta se quer sincronizar agora.
func (p *Poller) saveM3UAndOffer(ctx context.Context, chatID int64, url, name string) error {
	id, err := p.deps.DB.AddM3USource(ctx, name, url)
	if err != nil {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    chatID,
			Text:      fmt.Sprintf("❌ Erro ao salvar: %s", util.HTMLEscape(err.Error())),
			ParseMode: "HTML",
		})
		return nil
	}
	displayName := name
	if displayName == "" {
		displayName = fmt.Sprintf("Fonte #%d", id)
	}

	isXtream := importer.IsXtreamURL(url)
	var syncMarkup string
	if isXtream {
		syncMarkup = InlineKeyboardJSON([][]InlineButton{
			{
				{Text: "⬇️ Só Filmes", CallbackData: fmt.Sprintf("m3u:xtream:dl:movies:%d", id)},
				{Text: "⬇️ Só Séries", CallbackData: fmt.Sprintf("m3u:xtream:dl:series:%d", id)},
			},
			{
				{Text: "⬇️ Tudo (Filmes + Séries)", CallbackData: fmt.Sprintf("m3u:xtream:dl:all:%d", id)},
				{Text: "Agora não", CallbackData: "menu:m3u"},
			},
		})
	} else {
		syncMarkup = InlineKeyboardJSON([][]InlineButton{
			{
				{Text: "🔄 Sincronizar agora", CallbackData: fmt.Sprintf("m3u:sync:%d", id)},
				{Text: "Não agora", CallbackData: "menu:m3u"},
			},
		})
	}

	tag := ""
	if isXtream {
		tag = " 🎬 <i>(servidor de filmes)</i>"
	}
	_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:    chatID,
		ParseMode: "HTML",
		Text: fmt.Sprintf("✅ <b>Fonte salva:</b> %s%s\n<code>%s</code>\n\nO que deseja sincronizar?",
			util.HTMLEscape(displayName), tag, util.HTMLEscape(truncateURL(url, 80))),
		ReplyMarkup: syncMarkup,
	})
	return nil
}

// ── Sync canais M3U normais ─────────────────────────────────────────────────

// syncM3USource importa uma fonte M3U normal (canais ao vivo).
func (p *Poller) syncM3USource(ctx context.Context, chatID, sourceID int64) error {
	src, err := p.deps.DB.GetM3USource(ctx, sourceID)
	if err != nil || src == nil {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID: chatID, Text: "⚠️ Fonte não encontrada.",
		})
		return nil
	}

	name := src.Name
	if name == "" {
		name = fmt.Sprintf("Fonte #%d", src.ID)
	}

	notif, _ := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:    chatID,
		Text:      fmt.Sprintf("🔄 Sincronizando canais de <b>%s</b>...", util.HTMLEscape(name)),
		ParseMode: "HTML",
	})

	chImp, err := p.buildChannelImporter(ctx)
	if err != nil {
		p.editOrSend(ctx, chatID, notif, fmt.Sprintf("⚠️ %s", util.HTMLEscape(err.Error())))
		return nil
	}
	defer chImp.Close()
	res, err := chImp.ImportFromURL(ctx, src.URL)
	if err != nil {
		p.editOrSend(ctx, chatID, notif, fmt.Sprintf("❌ Erro ao sincronizar <b>%s</b>:\n<code>%s</code>",
			util.HTMLEscape(name), util.HTMLEscape(err.Error())))
		return nil
	}

	total := res.Inserted + res.Updated
	_ = p.deps.DB.UpdateM3USourceSync(ctx, sourceID, total)

	p.editOrSend(ctx, chatID, notif, fmt.Sprintf(
		"✅ <b>%s</b> atualizado!\n\n"+
			"📋 <b>Lidos:</b> %d\n"+
			"✨ <b>Inseridos:</b> %d\n"+
			"🔄 <b>Atualizados:</b> %d\n"+
			"📂 <b>Categorias:</b> %d",
		util.HTMLEscape(name),
		res.TotalRead, res.Inserted, res.Updated, res.Categories,
	))
	return nil
}

// syncAllM3U sincroniza todas as fontes salvas.
func (p *Poller) syncAllM3U(ctx context.Context, chatID int64) error {
	sources, err := p.deps.DB.ListM3USources(ctx)
	if err != nil || len(sources) == 0 {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID: chatID, Text: "ℹ️ Nenhuma fonte M3U cadastrada.",
		})
		return nil
	}

	notif, _ := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:    chatID,
		Text:      fmt.Sprintf("🔄 Sincronizando %d fonte(s)...", len(sources)),
		ParseMode: "HTML",
	})

	var xtreamSources, m3uSources []database.M3USource
	for _, src := range sources {
		if importer.IsXtreamURL(src.URL) {
			xtreamSources = append(xtreamSources, src)
		} else {
			m3uSources = append(m3uSources, src)
		}
	}

	var totalInserted, totalUpdated, totalRead int
	var failures []string

	// Sync fontes M3U normais (canais)
	if len(m3uSources) > 0 {
		chImp, chErr := p.buildChannelImporter(ctx)
		if chErr != nil {
			failures = append(failures, "Canais M3U: "+chErr.Error())
		} else {
			defer chImp.Close()
			for _, src := range m3uSources {
				name := src.Name
				if name == "" {
					name = fmt.Sprintf("Fonte #%d", src.ID)
				}
				res, err := chImp.ImportFromURL(ctx, src.URL)
				if err != nil {
					failures = append(failures, fmt.Sprintf("• %s: %s", name, err.Error()))
					continue
				}
				totalRead += res.TotalRead
				totalInserted += res.Inserted
				totalUpdated += res.Updated
				_ = p.deps.DB.UpdateM3USourceSync(ctx, src.ID, res.Inserted+res.Updated)
			}
		}
	}

	// Sync fontes Xtream (iniciado em background individualmente)
	for _, src := range xtreamSources {
		_ = p.startXtreamSync(ctx, chatID, src.ID, "all")
	}

	if len(m3uSources) > 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("✅ <b>Canais M3U sincronizados</b> (%d fonte(s))\n\n", len(m3uSources)))
		sb.WriteString(fmt.Sprintf("✨ <b>Inseridos:</b> %d\n", totalInserted))
		sb.WriteString(fmt.Sprintf("🔄 <b>Atualizados:</b> %d\n", totalUpdated))
		if totalRead > 0 {
			sb.WriteString(fmt.Sprintf("📋 <b>Lidos:</b> %d\n", totalRead))
		}
		if len(failures) > 0 {
			sb.WriteString(fmt.Sprintf("\n⚠️ <b>Falhas (%d):</b>\n%s", len(failures), strings.Join(failures, "\n")))
		}
		p.editOrSend(ctx, chatID, notif, sb.String())
	}
	return nil
}

// ── Sync Xtream em background com progresso ─────────────────────────────────

// startXtreamSync inicia uma importação Xtream em goroutine de fundo.
// kind: "movies" | "series" | "all"
func (p *Poller) startXtreamSync(ctx context.Context, chatID, sourceID int64, kind string) error {
	src, err := p.deps.DB.GetM3USource(ctx, sourceID)
	if err != nil || src == nil {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID: chatID, Text: "⚠️ Fonte não encontrada.",
		})
		return nil
	}

	// Verifica se já está rodando
	if existing, ok := p.xtreamJobs.get(sourceID); ok {
		_ = existing // já tem job ativo
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    chatID,
			Text:      "⏳ Já existe uma importação em andamento para esta fonte.\nAguarde terminar ou cancele primeiro.",
			ParseMode: "HTML",
			ReplyMarkup: InlineKeyboardJSON([][]InlineButton{
				{{Text: "⛔ Cancelar importação", CallbackData: fmt.Sprintf("m3u:xtream:cancel:%d", sourceID)}},
			}),
		})
		return nil
	}

	creds, parseErr := importer.ParseXtreamURL(src.URL)
	if parseErr != nil {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    chatID,
			Text:      fmt.Sprintf("❌ URL inválida: %s", util.HTMLEscape(parseErr.Error())),
			ParseMode: "HTML",
		})
		return nil
	}

	name := src.Name
	if name == "" {
		name = fmt.Sprintf("Fonte #%d", src.ID)
	}

	kindLabel := map[string]string{
		"movies": "🎬 Filmes",
		"series": "📺 Séries",
		"all":    "🎬📺 Filmes + Séries",
	}[kind]

	notif, _ := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:    chatID,
		ParseMode: "HTML",
		Text:      fmt.Sprintf("⏳ <b>%s</b> — iniciando <b>%s</b>...", util.HTMLEscape(name), kindLabel),
		ReplyMarkup: InlineKeyboardJSON([][]InlineButton{
			{{Text: "⛔ Cancelar", CallbackData: fmt.Sprintf("m3u:xtream:cancel:%d", sourceID)}},
		}),
	})

	jobCtx, cancel := context.WithCancel(p.appCtx)
	job := &xtreamJob{
		ChatID:     chatID,
		NotifMsgID: 0,
		Kind:       kind,
		Cancel:     cancel,
	}
	if notif != nil {
		job.NotifMsgID = notif.MessageID
	}
	p.xtreamJobs.set(sourceID, job)

	go func() {
		defer func() {
			cancel()
			p.xtreamJobs.del(sourceID)
		}()

		xImp, xErr := p.buildXtreamImporter(jobCtx)
		if xErr != nil {
			p.sendProgress(chatID, notif, fmt.Sprintf(
				"⚠️ <b>Painel offline</b> — arquivos serão enviados sem salvar\n<i>%s</i>",
				util.HTMLEscape(xErr.Error()),
			))
			xImp = importer.NewXtreamImporterDryRun(p.deps.Logger)
		}
		defer xImp.Close()

		// Baixa cada arquivo e envia ao Telegram LOG_CHANNEL, gerando URL de streaming
		stats := &downloadStats{}
		xImp.Upload = p.makeDownloadAndUploadFunc(sourceID, stats)

		// Contabiliza inserções/atualizações no XUI em tempo real
		xImp.OnXUIUpdate = func(inserted bool) {
			if inserted {
				stats.xuiIns.Add(1)
			} else {
				stats.xuiUpd.Add(1)
			}
		}

		// Contadores atômicos de série — escritos pelo import loop, lidos pelo ticker.
		var seriesDoneA, seriesTotalA atomic.Int64
		var tickSeriesA, tickEpA atomic.Value
		tickSeriesA.Store("")
		tickEpA.Store("")
		stats.curName.Store("")
		stats.curPhase.Store("")

		setSeriesProgress := func(done, total int) {
			seriesDoneA.Store(int64(done))
			seriesTotalA.Store(int64(total))
		}

		// OnSeriesStart: atualiza a barra ANTES dos episódios de cada série.
		xImp.OnSeriesStart = func(done, total int, sName string) {
			setSeriesProgress(done, total)
		}

		// OnEpisode: registra série/episódio atual; o ticker cuida da atualização contínua.
		xImp.OnEpisode = func(seriesName, epLabel string) {
			tickSeriesA.Store(seriesName)
			tickEpA.Store(epLabel)
		}

		// Ticker de progresso em tempo real — atualiza a mensagem a cada 3s.
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					sName, _ := tickSeriesA.Load().(string)
					if sName == "" {
						continue
					}
					epLbl, _ := tickEpA.Load().(string)
					done := int(seriesDoneA.Load())
					tot := int(seriesTotalA.Load())
					sent := stats.curSent.Load()
					total := stats.curTotal.Load()
					phase, _ := stats.curPhase.Load().(string)
					curFile, _ := stats.curName.Load().(string)

					bar := ""
					if tot > 0 {
						bar = progressBar(done, tot) + "\n"
					}

					fileInfo := ""
					if curFile != "" {
						phaseLabel := "⬇️ Baixando"
						if phase == "enviando" {
							phaseLabel = "⬆️ Enviando"
						}
						if total > 0 {
							pct := sent * 100 / total
							fileInfo = fmt.Sprintf("\n%s: %s / %s (%d%%)",
								phaseLabel, formatBytes(sent), formatBytes(total), pct)
						} else if sent > 0 {
							fileInfo = fmt.Sprintf("\n%s: %s", phaseLabel, formatBytes(sent))
						} else {
							fileInfo = fmt.Sprintf("\n%s...", phaseLabel)
						}
					}

					p.sendProgress(chatID, notif, fmt.Sprintf(
						"📺 <b>%s</b>\n"+
							"%s"+
							"⬇️⬆️ <b>%s</b>%s\n\n"+
							"✅ Enviados: %d | ❌ Falha: %d\n"+
							"📋 Painel: ✨%d inseridos | 🔄%d atualizados\n"+
							"💾 Total enviado: %s",
						util.HTMLEscape(sName),
						bar,
						util.HTMLEscape(epLbl),
						fileInfo,
						stats.ok.Load(), stats.failed.Load(),
						stats.xuiIns.Load(), stats.xuiUpd.Load(),
						formatBytes(stats.bytes.Load()),
					))
				case <-jobCtx.Done():
					return
				}
			}
		}()

		// Expõe setSeriesProgress para as funções runXtream*.
		_ = setSeriesProgress // usado via OnSeriesStart; mantido para runXtream* também

		switch kind {
		case "movies":
			p.runXtreamMovies(jobCtx, chatID, sourceID, name, notif, xImp, creds, stats, setSeriesProgress)
		case "series":
			p.runXtreamSeries(jobCtx, chatID, sourceID, name, notif, xImp, creds, stats, setSeriesProgress)
		case "all":
			p.runXtreamAll(jobCtx, chatID, sourceID, name, notif, xImp, creds, stats, setSeriesProgress)
		}
	}()

	return nil
}

// runXtreamMovies importa somente filmes com progresso a cada 10 itens.
func (p *Poller) runXtreamMovies(ctx context.Context, chatID, sourceID int64, name string, notif *SentMessage, xImp *importer.XtreamImporter, creds *importer.XtreamCreds, stats *downloadStats, setProgress func(int, int)) {
	p.sendProgress(chatID, notif, fmt.Sprintf("📡 <b>%s</b> — buscando lista de filmes do servidor...", util.HTMLEscape(name)))

	lastUpdate := time.Now()

	inserted, updated, _, err := xImp.ImportMovies(ctx, creds, func(done, total int, label string) {
		if setProgress != nil {
			setProgress(done, total)
		}
		if time.Since(lastUpdate) > 10*time.Second {
			lastUpdate = time.Now()
			pct := 0
			if total > 0 {
				pct = done * 100 / total
			}
			p.sendProgress(chatID, notif, fmt.Sprintf(
				"🎬 <b>%s</b> — ⬇️⬆️ Filmes\n\n"+
					"%s\n"+
					"📊 %d/%d (%d%%)\n"+
					"✅ Enviados: %d | ❌ Falha: %d\n"+
					"📋 Painel: ✨%d inseridos | 🔄%d atualizados\n"+
					"💾 Enviado: %s\n"+
					"📌 <i>%s</i>",
				util.HTMLEscape(name),
				progressBar(done, total),
				done, total, pct,
				stats.ok.Load(), stats.failed.Load(),
				stats.xuiIns.Load(), stats.xuiUpd.Load(),
				formatBytes(stats.bytes.Load()),
				util.HTMLEscape(truncateURL(label, 50)),
			))
		}
	})

	if err != nil && ctx.Err() == nil {
		p.sendProgress(chatID, notif, fmt.Sprintf("❌ Erro ao baixar filmes:\n<code>%s</code>", util.HTMLEscape(err.Error())))
		return
	}

	if xImp.DryRun {
		p.sendProgress(chatID, notif, fmt.Sprintf(
			"✅ <b>%s</b> — Filmes enviados!\n\n"+
				"✅ <b>Enviados:</b> %d | ❌ <b>Falha:</b> %d\n"+
				"💾 <b>Total enviado:</b> %s\n"+
				"<i>Painel offline — links guardados localmente.</i>",
			util.HTMLEscape(name),
			stats.ok.Load(), stats.failed.Load(),
			formatBytes(stats.bytes.Load()),
		))
		return
	}

	total := inserted + updated
	_ = p.deps.DB.UpdateM3USourceSync(ctx, sourceID, total)
	p.sendProgress(chatID, notif, fmt.Sprintf(
		"✅ <b>%s</b> — Filmes prontos!\n\n"+
			"✅ <b>Enviados:</b> %d | ❌ <b>Falha:</b> %d\n"+
			"💾 <b>Total enviado:</b> %s\n"+
			"📋 <b>Painel:</b> ✨%d inseridos | 🔄%d atualizados",
		util.HTMLEscape(name),
		stats.ok.Load(), stats.failed.Load(),
		formatBytes(stats.bytes.Load()),
		stats.xuiIns.Load(), stats.xuiUpd.Load(),
	))
}

// runXtreamSeries importa somente séries com progresso a cada série.
func (p *Poller) runXtreamSeries(ctx context.Context, chatID, sourceID int64, name string, notif *SentMessage, xImp *importer.XtreamImporter, creds *importer.XtreamCreds, stats *downloadStats, setProgress func(int, int)) {
	p.sendProgress(chatID, notif, fmt.Sprintf("📡 <b>%s</b> — buscando lista de séries do servidor...", util.HTMLEscape(name)))

	lastUpdate := time.Now()

	seriesIns, seriesUpd, _, _, err := xImp.ImportSeries(ctx, creds, 0, func(done, total int, label string) {
		if setProgress != nil {
			setProgress(done, total)
		}
		if time.Since(lastUpdate) > 10*time.Second {
			lastUpdate = time.Now()
			pct := 0
			if total > 0 {
				pct = done * 100 / total
			}
			p.sendProgress(chatID, notif, fmt.Sprintf(
				"📺 <b>%s</b> — ⬇️⬆️ Séries\n\n"+
					"%s\n"+
					"📊 %d/%d séries (%d%%)\n"+
					"✅ Enviados: %d | ❌ Falha: %d\n"+
					"📋 Painel: ✨%d ep inseridos | 🔄%d atualizados\n"+
					"💾 Enviado: %s\n"+
					"📌 <i>%s</i>",
				util.HTMLEscape(name),
				progressBar(done, total),
				done, total, pct,
				stats.ok.Load(), stats.failed.Load(),
				stats.xuiIns.Load(), stats.xuiUpd.Load(),
				formatBytes(stats.bytes.Load()),
				util.HTMLEscape(truncateURL(label, 50)),
			))
		}
	})

	if err != nil && ctx.Err() == nil {
		p.sendProgress(chatID, notif, fmt.Sprintf("❌ Erro ao baixar séries:\n<code>%s</code>", util.HTMLEscape(err.Error())))
		return
	}

	if xImp.DryRun {
		p.sendProgress(chatID, notif, fmt.Sprintf(
			"✅ <b>%s</b> — Séries enviadas!\n\n"+
				"✅ <b>Enviados:</b> %d | ❌ <b>Falha:</b> %d\n"+
				"💾 <b>Total enviado:</b> %s\n"+
				"<i>Painel offline — links guardados localmente.</i>",
			util.HTMLEscape(name),
			stats.ok.Load(), stats.failed.Load(),
			formatBytes(stats.bytes.Load()),
		))
		return
	}

	total := seriesIns + seriesUpd
	_ = p.deps.DB.UpdateM3USourceSync(ctx, sourceID, total)
	p.sendProgress(chatID, notif, fmt.Sprintf(
		"✅ <b>%s</b> — Séries prontas!\n\n"+
			"✅ <b>Enviados:</b> %d | ❌ <b>Falha:</b> %d\n"+
			"💾 <b>Total enviado:</b> %s\n"+
			"📋 <b>Painel:</b> ✨%d ep inseridos | 🔄%d atualizados",
		util.HTMLEscape(name),
		stats.ok.Load(), stats.failed.Load(),
		formatBytes(stats.bytes.Load()),
		stats.xuiIns.Load(), stats.xuiUpd.Load(),
	))
}

// runXtreamAll importa filmes e depois séries em sequência.
func (p *Poller) runXtreamAll(ctx context.Context, chatID, sourceID int64, name string, notif *SentMessage, xImp *importer.XtreamImporter, creds *importer.XtreamCreds, stats *downloadStats, setProgress func(int, int)) {
	p.sendProgress(chatID, notif, fmt.Sprintf("📡 <b>%s</b> — buscando lista de filmes do servidor...", util.HTMLEscape(name)))

	lastUpdate := time.Now()

	// Fase 1: filmes
	movIns, movUpd, _, movErr := xImp.ImportMovies(ctx, creds, func(done, total int, label string) {
		if setProgress != nil {
			setProgress(done, total)
		}
		if time.Since(lastUpdate) > 10*time.Second {
			lastUpdate = time.Now()
			pct := 0
			if total > 0 {
				pct = done * 100 / total
			}
			p.sendProgress(chatID, notif, fmt.Sprintf(
				"🎬 <b>%s</b> — ⬇️⬆️ Fase 1/2: Filmes\n\n"+
					"%s\n"+
					"📊 %d/%d (%d%%)\n"+
					"✅ Enviados: %d | ❌ Falha: %d | 💾 %s\n"+
					"📌 <i>%s</i>",
				util.HTMLEscape(name),
				progressBar(done, total),
				done, total, pct,
				stats.ok.Load(), stats.failed.Load(), formatBytes(stats.bytes.Load()),
				util.HTMLEscape(truncateURL(label, 50)),
			))
		}
	})
	if movErr != nil && ctx.Err() == nil {
		p.sendProgress(chatID, notif, fmt.Sprintf("❌ Erro filmes:\n<code>%s</code>", util.HTMLEscape(movErr.Error())))
		return
	}
	if ctx.Err() != nil {
		return
	}

	p.sendProgress(chatID, notif, fmt.Sprintf(
		"🎬 Filmes ✅ %d ok / %d falha / 💾 %s\n\n"+
			"📋 <b>Fase 2/2: Séries</b> — buscando lista do servidor...",
		stats.ok.Load(), stats.failed.Load(), formatBytes(stats.bytes.Load()),
	))

	// Fase 2: séries
	lastUpdate = time.Now()
	serIns, serUpd, _, _, serErr := xImp.ImportSeries(ctx, creds, 0, func(done, total int, label string) {
		if setProgress != nil {
			setProgress(done, total)
		}
		if time.Since(lastUpdate) > 10*time.Second {
			lastUpdate = time.Now()
			pct := 0
			if total > 0 {
				pct = done * 100 / total
			}
			p.sendProgress(chatID, notif, fmt.Sprintf(
				"🎬 Filmes ✅ (%d ok)\n"+
					"📺 <b>⬇️⬆️ Fase 2/2: Séries</b>\n\n"+
					"%s\n"+
					"📊 %d/%d séries (%d%%)\n"+
					"✅ Enviados: %d | ❌ Falha: %d | 💾 %s\n"+
					"📌 <i>%s</i>",
				movIns+movUpd,
				progressBar(done, total),
				done, total, pct,
				stats.ok.Load(), stats.failed.Load(), formatBytes(stats.bytes.Load()),
				util.HTMLEscape(truncateURL(label, 50)),
			))
		}
	})
	if serErr != nil && ctx.Err() == nil {
		p.sendProgress(chatID, notif, fmt.Sprintf("❌ Erro séries:\n<code>%s</code>", util.HTMLEscape(serErr.Error())))
		return
	}

	total := movIns + movUpd + serIns + serUpd
	_ = p.deps.DB.UpdateM3USourceSync(ctx, sourceID, total)
	p.sendProgress(chatID, notif, fmt.Sprintf(
		"✅ <b>%s</b> — Tudo pronto!\n\n"+
			"🎬 <b>Filmes no painel:</b> ✨%d inseridos / 🔄%d atualizados\n"+
			"📺 <b>Séries no painel:</b> ✨%d inseridas / 🔄%d atualizadas\n"+
			"✅ <b>Enviados:</b> %d | ❌ <b>Falha:</b> %d\n"+
			"💾 <b>Total enviado:</b> %s",
		util.HTMLEscape(name),
		movIns, movUpd, serIns, serUpd,
		stats.ok.Load(), stats.failed.Load(),
		formatBytes(stats.bytes.Load()),
	))
}

// formatBytes formata bytes em string legível (KB, MB, GB).
func formatBytes(b int64) string {
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

// sendProgress edita a mensagem de progresso ou envia nova se não houver.
func (p *Poller) sendProgress(chatID int64, notif *SentMessage, text string) {
	bgCtx := context.Background()
	if notif != nil {
		_ = p.deps.API.EditMessageText(bgCtx, EditMessageTextParams{
			ChatID:    chatID,
			MessageID: notif.MessageID,
			Text:      text,
			ParseMode: "HTML",
		})
		return
	}
	_, _ = p.deps.API.SendMessage(bgCtx, SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	})
}

// ── Remover fonte ────────────────────────────────────────────────────────────

func (p *Poller) deleteM3USource(ctx context.Context, chatID, sourceID int64) error {
	src, err := p.deps.DB.GetM3USource(ctx, sourceID)
	if err != nil || src == nil {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID: chatID, Text: "⚠️ Fonte não encontrada.",
		})
		return nil
	}
	name := src.Name
	if name == "" {
		name = fmt.Sprintf("Fonte #%d", src.ID)
	}
	if err := p.deps.DB.DeleteM3USource(ctx, sourceID); err != nil {
		_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
			ChatID:    chatID,
			Text:      fmt.Sprintf("❌ Erro ao remover: %s", util.HTMLEscape(err.Error())),
			ParseMode: "HTML",
		})
		return nil
	}
	_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:    chatID,
		Text:      fmt.Sprintf("🗑 <b>%s</b> removida.", util.HTMLEscape(name)),
		ParseMode: "HTML",
	})
	return p.sendM3UMenu(ctx, chatID)
}

// ── Utilitários ──────────────────────────────────────────────────────────────

// canImportChannels verifica se o XUI está configurado para importação de canais.
func (p *Poller) canImportChannels(ctx context.Context) bool {
	host, ok := p.deps.DB.GetSetting(ctx, "xui_host")
	return ok && host != ""
}

// buildChannelImporter cria um ChannelImporter com a conexão XUI atual.
func (p *Poller) buildChannelImporter(ctx context.Context) (*importer.ChannelImporter, error) {
	host, _ := p.deps.DB.GetSetting(ctx, "xui_host")
	user, _ := p.deps.DB.GetSetting(ctx, "xui_user")
	pass, _ := p.deps.DB.GetSetting(ctx, "xui_password")
	dbName, _ := p.deps.DB.GetSetting(ctx, "xui_database")
	portStr, _ := p.deps.DB.GetSetting(ctx, "xui_port")
	adminIDStr, _ := p.deps.DB.GetSetting(ctx, "xui_admin_id")
	port := 3306
	if n, err := strconv.Atoi(portStr); err == nil && n > 0 {
		port = n
	}
	adminID := p.deps.Config.XUIAdminID
	if n, err := strconv.Atoi(adminIDStr); err == nil && n > 0 {
		adminID = n
	}
	if dbName == "" {
		dbName = "xui"
	}
	if host == "" {
		return nil, fmt.Errorf("painel não configurado — use /configurar primeiro")
	}
	db, err := xui.Open(xui.Config{
		Host: host, Port: port, User: user, Password: pass,
		Database: dbName, ServerID: 1, AdminID: adminID,
	})
	if err != nil {
		return nil, fmt.Errorf("conexão com o painel falhou: %w", err)
	}
	return importer.NewChannelImporter(db, nil, p.deps.Logger), nil
}

// editOrSend edita a mensagem anterior ou envia nova.
func (p *Poller) editOrSend(ctx context.Context, chatID int64, prev *SentMessage, text string) {
	if prev != nil {
		_ = p.deps.API.EditMessageText(ctx, EditMessageTextParams{
			ChatID:    chatID,
			MessageID: prev.MessageID,
			Text:      text,
			ParseMode: "HTML",
		})
		return
	}
	_, _ = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	})
}

func truncateURL(url string, max int) string {
	if len(url) <= max {
		return url
	}
	return url[:max-3] + "..."
}

// progressBar retorna uma barra visual de progresso.
// Exemplo: [▓▓▓▓▓░░░░░] 50%
func progressBar(done, total int) string {
	const width = 10
	if total <= 0 {
		return ""
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	pct := done * 100 / total
	return fmt.Sprintf("[%s%s] %d%%",
		strings.Repeat("▓", filled),
		strings.Repeat("░", width-filled),
		pct,
	)
}
