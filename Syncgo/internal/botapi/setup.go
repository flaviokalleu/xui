package botapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"syncgo/internal/importer"
	"syncgo/internal/tmdb"
	"syncgo/internal/xui"
)

// startSetup inicia o wizard de configuração do XUI.
func (p *Poller) startSetup(ctx context.Context, chatID int64) {
	// Cancela qualquer setup anterior do mesmo chat.
	p.setups.del(chatID)

	sess := &setupSession{
		ChatID:  chatID,
		Stage:   ssHost,
		Expires: time.Now().Add(20 * time.Minute),
	}

	// Preenche com valores atuais do banco para facilitar edição.
	if v, ok := p.deps.DB.GetSetting(ctx, "xui_host"); ok {
		sess.Host = v
	}
	if v, ok := p.deps.DB.GetSetting(ctx, "xui_port"); ok {
		sess.Port = v
	} else {
		sess.Port = "3306"
	}
	if v, ok := p.deps.DB.GetSetting(ctx, "xui_user"); ok {
		sess.User = v
	}
	if v, ok := p.deps.DB.GetSetting(ctx, "xui_password"); ok {
		sess.Pass = v
	}
	if v, ok := p.deps.DB.GetSetting(ctx, "xui_database"); ok {
		sess.DbName = v
	} else {
		sess.DbName = "xui"
	}
	if v, ok := p.deps.DB.GetSetting(ctx, "insert_mode"); ok {
		sess.Mode = v
	}
	sess.MovieBouquetID = p.deps.DB.GetSettingInt64(ctx, "default_movie_bouquet_id", 0)
	if v, ok := p.deps.DB.GetSetting(ctx, "default_movie_bouquet_name"); ok {
		sess.MovieBouquetName = v
	}
	sess.SeriesBouquetID = p.deps.DB.GetSettingInt64(ctx, "default_series_bouquet_id", 0)
	if v, ok := p.deps.DB.GetSetting(ctx, "default_series_bouquet_name"); ok {
		sess.SeriesBouquetName = v
	}

	p.setups.set(sess)
	p.sendSetupStep(ctx, sess)
}

// sendSetupStep envia a mensagem/teclado correspondente ao estágio atual do wizard.
func (p *Poller) sendSetupStep(ctx context.Context, sess *setupSession) {
	switch sess.Stage {
	case ssHost:
		hint := ""
		if sess.Host != "" {
			hint = fmt.Sprintf("\nAtual: <code>%s</code>", sess.Host)
		}
		p.setupSend(ctx, sess.ChatID,
			"🔧 <b>Conectar ao Painel — Passo 1/5</b>\n\n"+
				"Digite o <b>endereço do servidor</b> (IP ou domínio):"+hint, "")

	case ssPort:
		p.setupSend(ctx, sess.ChatID,
			"🔧 <b>Passo 2/5 — Porta do servidor</b>\n\nDigite a porta (padrão: <code>3306</code>):",
			InlineKeyboardJSON([][]InlineButton{
				{{Text: "3306 (padrão)", CallbackData: fmt.Sprintf("setup:port:3306:%d", sess.ChatID)}},
			}),
		)

	case ssUser:
		hint := ""
		if sess.User != "" {
			hint = fmt.Sprintf("\nAtual: <code>%s</code>", sess.User)
		}
		p.setupSend(ctx, sess.ChatID,
			"🔧 <b>Passo 3/5 — Usuário</b>\n\nDigite o <b>usuário</b> do servidor:"+hint, "")

	case ssPass:
		p.setupSend(ctx, sess.ChatID,
			"🔧 <b>Passo 4/5 — Senha</b>\n\nDigite a <b>senha</b> do servidor:", "")

	case ssDbName:
		p.setupSend(ctx, sess.ChatID,
			"🔧 <b>Passo 5/5 — Nome do banco</b>\n\nDigite o nome do banco (padrão: <code>xui</code>):",
			InlineKeyboardJSON([][]InlineButton{
				{{Text: "xui (padrão)", CallbackData: fmt.Sprintf("setup:db:xui:%d", sess.ChatID)}},
			}),
		)

	case ssMode:
		p.setupSend(ctx, sess.ChatID,
			"⚙️ <b>Modo de adição</b>\n\n"+
				"🤖 <b>Automático</b>\n"+
				"O bot detecta o tipo pelo nome do arquivo (<code>240022.mp4</code> = filme, <code>240022_S01E02.mp4</code> = episódio) "+
				"e adiciona ao painel sem perguntar nada, usando as categorias padrão que você vai configurar a seguir.\n\n"+
				"👤 <b>Manual</b>\n"+
				"Antes de adicionar, o bot exibe uma tela de confirmação onde você pode corrigir o ID, temporada/episódio e escolher a categoria.\n\n"+
				"Qual modo prefere?",
			InlineKeyboardJSON([][]InlineButton{
				{
					{Text: "🤖 Automático", CallbackData: fmt.Sprintf("setup:mode:auto:%d", sess.ChatID)},
					{Text: "👤 Manual", CallbackData: fmt.Sprintf("setup:mode:manual:%d", sess.ChatID)},
				},
			}),
		)

	case ssMovieBq:
		p.sendBouquetPicker(ctx, sess, "movie")

	case ssNewMovieBq:
		p.setupSend(ctx, sess.ChatID,
			"🎬 Digite o nome da nova categoria para <b>Filmes</b>:", "")

	case ssSeriesBq:
		p.sendBouquetPicker(ctx, sess, "series")

	case ssNewSeriesBq:
		p.setupSend(ctx, sess.ChatID,
			"📺 Digite o nome da nova categoria para <b>Séries</b>:", "")
	}
}

func (p *Poller) sendBouquetPicker(ctx context.Context, sess *setupSession, kind string) {
	label := "Filmes"
	if kind == "series" {
		label = "Séries"
	}

	var bouquets []xui.BouquetInfo
	if sess.XUIDB != nil {
		bouquets, _ = sess.XUIDB.ListBouquets(ctx)
	}

	var rows [][]InlineButton
	var row []InlineButton
	for _, b := range bouquets {
		row = append(row, InlineButton{
			Text:         b.Name,
			CallbackData: fmt.Sprintf("setup:bq:%s:%d:%d", kind, b.ID, sess.ChatID),
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
		Text:         "➕ Criar nova categoria",
		CallbackData: fmt.Sprintf("setup:bq:new:%s:%d", kind, sess.ChatID),
	}})

	p.setupSend(ctx, sess.ChatID,
		fmt.Sprintf("📂 <b>Categoria padrão — %s</b>\n\nSelecione qual categoria receberá os %s adicionados automaticamente:", label, strings.ToLower(label)),
		InlineKeyboardJSON(rows),
	)
}

func (p *Poller) setupSend(ctx context.Context, chatID int64, text, markup string) {
	params := SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	}
	if markup != "" {
		params.ReplyMarkup = markup
	}
	_, _ = p.deps.API.SendMessage(ctx, params)
}

// handleSetupInput intercepta mensagens de texto durante o wizard de configuração.
// Retorna true se a mensagem foi consumida.
func (p *Poller) handleSetupInput(ctx context.Context, m *Message) bool {
	sess, ok := p.setups.get(m.Chat.ID)
	if !ok {
		return false
	}
	// Estágios que esperam texto do usuário.
	switch sess.Stage {
	case ssHost, ssPort, ssUser, ssPass, ssDbName, ssNewMovieBq, ssNewSeriesBq:
		// ok, processa
	default:
		return false // estágio usa botões inline
	}

	text := strings.TrimSpace(m.Text)
	if text == "" {
		return true
	}

	switch sess.Stage {
	case ssHost:
		sess.Host = text
		sess.Stage = ssPort
		p.setups.set(sess)
		p.sendSetupStep(ctx, sess)

	case ssPort:
		port, err := strconv.Atoi(text)
		if err != nil || port <= 0 || port > 65535 {
			p.setupSend(ctx, sess.ChatID, "⚠️ Porta inválida. Digite um número entre 1 e 65535:", "")
			return true
		}
		sess.Port = text
		sess.Stage = ssUser
		p.setups.set(sess)
		p.sendSetupStep(ctx, sess)

	case ssUser:
		sess.User = text
		sess.Stage = ssPass
		p.setups.set(sess)
		p.sendSetupStep(ctx, sess)

	case ssPass:
		sess.Pass = text
		sess.Stage = ssDbName
		p.setups.set(sess)
		p.sendSetupStep(ctx, sess)

	case ssDbName:
		sess.DbName = text
		p.tryConnectXUI(ctx, sess)

	case ssNewMovieBq:
		if sess.XUIDB == nil {
			return true
		}
		bqID, err := sess.XUIDB.GetOrCreateBouquet(ctx, text)
		if err != nil {
			p.setupSend(ctx, sess.ChatID, fmt.Sprintf("⚠️ Erro ao criar categoria: %s", err), "")
			return true
		}
		sess.MovieBouquetID = bqID
		sess.MovieBouquetName = text
		sess.Stage = ssSeriesBq
		p.setups.set(sess)
		p.sendSetupStep(ctx, sess)

	case ssNewSeriesBq:
		if sess.XUIDB == nil {
			return true
		}
		bqID, err := sess.XUIDB.GetOrCreateBouquet(ctx, text)
		if err != nil {
			p.setupSend(ctx, sess.ChatID, fmt.Sprintf("⚠️ Erro ao criar categoria: %s", err), "")
			return true
		}
		sess.SeriesBouquetID = bqID
		sess.SeriesBouquetName = text
		p.finishSetup(ctx, sess)
	}
	return true
}

// handleSetupCallback processa callbacks do wizard (prefixo "setup:").
func (p *Poller) handleSetupCallback(ctx context.Context, cb *CallbackQuery, data string) {
	// Formatos:
	//   setup:port:{valor}:{chatID}
	//   setup:db:{valor}:{chatID}
	//   setup:mode:{auto|manual}:{chatID}
	//   setup:bq:{movie|series}:{bouquetID}:{chatID}
	//   setup:bq:new:{movie|series}:{chatID}
	//   setup:cancel:{chatID}

	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return
	}

	// chatID está sempre no último segmento.
	chatID, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return
	}

	sess, ok := p.setups.get(chatID)
	if !ok {
		_ = p.deps.API.AnswerCallbackQuery(ctx, cb.ID, "⚠️ Sessão expirada. Use /configurar novamente.")
		return
	}
	_ = p.deps.API.AnswerCallbackQuery(ctx, cb.ID, "")

	action := parts[0]
	switch action {
	case "port":
		if len(parts) < 3 {
			return
		}
		sess.Port = parts[1]
		sess.Stage = ssUser
		p.setups.set(sess)
		p.sendSetupStep(ctx, sess)

	case "db":
		if len(parts) < 3 {
			return
		}
		sess.DbName = parts[1]
		p.tryConnectXUI(ctx, sess)

	case "mode":
		if len(parts) < 3 {
			return
		}
		sess.Mode = parts[1]
		sess.Stage = ssMovieBq
		p.setups.set(sess)
		p.sendSetupStep(ctx, sess)

	case "bq":
		// setup:bq:new:{kind}:{chatID}  →  parts = [bq, new, kind, chatID]
		// setup:bq:{kind}:{bouquetID}:{chatID}  →  parts = [bq, kind, bouquetID, chatID]
		if len(parts) < 4 {
			return
		}
		if parts[1] == "new" {
			kind := parts[2]
			if kind == "movie" {
				sess.Stage = ssNewMovieBq
			} else {
				sess.Stage = ssNewSeriesBq
			}
			p.setups.set(sess)
			p.sendSetupStep(ctx, sess)
			return
		}
		// seleção de bouquet existente
		kind := parts[1]
		bouquetID, _ := strconv.ParseInt(parts[2], 10, 64)
		var bouquetName string
		if sess.XUIDB != nil {
			bqs, _ := sess.XUIDB.ListBouquets(ctx)
			for _, b := range bqs {
				if b.ID == bouquetID {
					bouquetName = b.Name
					break
				}
			}
		}
		if kind == "movie" {
			sess.MovieBouquetID = bouquetID
			sess.MovieBouquetName = bouquetName
			sess.Stage = ssSeriesBq
			p.setups.set(sess)
			p.sendSetupStep(ctx, sess)
		} else {
			sess.SeriesBouquetID = bouquetID
			sess.SeriesBouquetName = bouquetName
			p.finishSetup(ctx, sess)
		}

	case "cancel":
		p.setups.del(chatID)
		if cb.Message != nil {
			_ = p.deps.API.EditMessageReplyMarkup(ctx, cb.Message.Chat.ID, cb.Message.MessageID, `{"inline_keyboard":[]}`)
		}
		p.setupSend(ctx, chatID, "❌ Configuração cancelada.", "")
	}
}

// tryConnectXUI testa a conexão com o painel usando as credenciais da sessão.
func (p *Poller) tryConnectXUI(ctx context.Context, sess *setupSession) {
	p.setupSend(ctx, sess.ChatID, "🔄 Testando conexão com o servidor...", "")

	port, _ := strconv.Atoi(sess.Port)
	if port == 0 {
		port = 3306
	}

	adminIDStr, _ := p.deps.DB.GetSetting(ctx, "xui_admin_id")
	adminID := p.deps.Config.XUIAdminID
	if n, err := strconv.Atoi(adminIDStr); err == nil && n > 0 {
		adminID = n
	}

	sess.closeXUI()
	db, err := xui.Open(xui.Config{
		Host:     sess.Host,
		Port:     port,
		User:     sess.User,
		Password: sess.Pass,
		Database: sess.DbName,
		ServerID: 1,
		AdminID:  adminID,
	})
	if err != nil {
		p.setupSend(ctx, sess.ChatID,
			fmt.Sprintf("❌ <b>Não consegui conectar:</b>\n<code>%s</code>\n\nVerifique os dados e tente novamente.\nDigite o endereço do servidor:", err),
			"")
		sess.Stage = ssHost
		p.setups.set(sess)
		return
	}

	sess.XUIDB = db
	sess.Stage = ssMode
	p.setups.set(sess)
	p.setupSend(ctx, sess.ChatID, "✅ <b>Conectado com sucesso!</b>", "")
	p.sendSetupStep(ctx, sess)
}

// finishSetup salva todas as configurações e reconecta o importer.
func (p *Poller) finishSetup(ctx context.Context, sess *setupSession) {
	db := p.deps.DB
	port, _ := strconv.Atoi(sess.Port)
	if port == 0 {
		port = 3306
	}

	// Salva credenciais XUI.
	for k, v := range map[string]string{
		"xui_host":     sess.Host,
		"xui_port":     sess.Port,
		"xui_user":     sess.User,
		"xui_password": sess.Pass,
		"xui_database": sess.DbName,
		"insert_mode":  sess.Mode,
	} {
		_ = db.SetSetting(ctx, k, v)
	}

	// Salva bouquets.
	_ = db.SetSetting(ctx, "default_movie_bouquet_id", strconv.FormatInt(sess.MovieBouquetID, 10))
	_ = db.SetSetting(ctx, "default_movie_bouquet_name", sess.MovieBouquetName)
	_ = db.SetSetting(ctx, "default_series_bouquet_id", strconv.FormatInt(sess.SeriesBouquetID, 10))
	_ = db.SetSetting(ctx, "default_series_bouquet_name", sess.SeriesBouquetName)

	// Reconecta o importer usando a conexão já aberta no wizard.
	xuiDB := sess.XUIDB
	sess.XUIDB = nil // transfere responsabilidade para o importer
	p.setups.del(sess.ChatID)

	// Fecha o importer anterior para liberar a conexão MySQL.
	if p.deps.Importer != nil {
		p.deps.Importer.Close()
		p.deps.Importer = nil
	}

	var newImp *importer.Importer
	if p.deps.Cfg != nil && p.deps.Cfg.TMDBAPIKey != "" {
		tmdbClient := tmdb.New(p.deps.Cfg.TMDBAPIKey, p.deps.Cfg.TMDBLanguage)
		newImp = importer.New(xuiDB, tmdbClient, nil, p.deps.Logger)
	} else {
		_ = xuiDB.Close()
	}
	p.deps.Importer = newImp

	modeText := "👤 Manual (você confirma cada adição)"
	if sess.Mode == "auto" {
		modeText = "🤖 Automático (adiciona direto sem perguntar)"
	}

	p.setupSend(ctx, sess.ChatID, fmt.Sprintf(
		"✅ <b>Painel conectado com sucesso!</b>\n\n"+
			"⚙️ <b>Modo:</b> %s\n"+
			"🎬 <b>Categoria Filmes:</b> %s\n"+
			"📺 <b>Categoria Séries:</b> %s\n\n"+
			"Tudo pronto! Envie um arquivo de vídeo para testar.",
		modeText,
		bqLabel(sess.MovieBouquetName, sess.MovieBouquetID),
		bqLabel(sess.SeriesBouquetName, sess.SeriesBouquetID),
	), "")
}

func bqLabel(name string, id int64) string {
	if name != "" {
		return name
	}
	if id != 0 {
		return fmt.Sprintf("#%d", id)
	}
	return "padrão (FILMES/SÉRIES)"
}
