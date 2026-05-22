package botapi

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// isOwnerOrAdmin returns true if the sender is the bot owner or a configured admin.
func (p *Poller) isOwnerOrAdmin(userID int64) bool {
	if p.deps.Cfg == nil {
		return false
	}
	if p.deps.Cfg.OwnerID != 0 && userID == p.deps.Cfg.OwnerID {
		return true
	}
	return slices.Contains(p.deps.Cfg.Admins, userID)
}

// handleAddToken processes "/addtoken <BOT_TOKEN>" — registers a new extra bot token.
// The token is validated by calling getMe, then stored in the DB and hot-loaded into
// the MTProto pool without requiring a restart.
func (p *Poller) handleAddToken(ctx context.Context, m *Message) error {
	if !p.isOwnerOrAdmin(senderID(m)) {
		return p.reply(ctx, m, "⛔ Apenas o owner/admin pode usar este comando.")
	}

	parts := strings.Fields(m.Text)
	if len(parts) < 2 {
		return p.reply(ctx, m, "Uso: /addtoken <TOKEN>\nEx: /addtoken 123456:AAABBB...")
	}
	token := parts[1]

	// Validate token via Bot API before storing.
	tmpClient := New(token)
	me, err := tmpClient.GetMe(ctx)
	if err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Token inválido ou bot inacessível: %v", err))
	}
	username := me.Username

	// Persist to DB.
	if err := p.deps.DB.AddBotToken(ctx, token, username); err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Erro ao salvar token: %v", err))
	}

	// Hot-load into the running MTProto pool (no restart needed).
	if p.deps.TGPool != nil {
		if _, err := p.deps.TGPool.AddClient(p.appCtx, token, p.deps.Logger); err != nil {
			p.deps.Logger.Error("hot-load token failed", "username", username, "err", err)
			return p.reply(ctx, m,
				fmt.Sprintf("✅ Token @%s salvo no banco, mas falhou ao iniciar conexão MTProto: %v\n"+
					"Reinicie o bot para ativar.", username, err))
		}
	}

	return p.reply(ctx, m,
		fmt.Sprintf("✅ Token @%s adicionado e ativo!\n"+
			"Slots adicionais de stream: +%d", username, p.deps.Cfg.MaxStreamsPerToken))
}

// handleListTokens processes "/tokens" — lists all registered extra tokens.
func (p *Poller) handleListTokens(ctx context.Context, m *Message) error {
	if !p.isOwnerOrAdmin(senderID(m)) {
		return p.reply(ctx, m, "⛔ Apenas o owner/admin pode usar este comando.")
	}

	tokens, err := p.deps.DB.ListBotTokens(ctx)
	if err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Erro: %v", err))
	}
	if len(tokens) == 0 {
		return p.reply(ctx, m, "Nenhum token extra cadastrado.\nUse /addtoken <TOKEN> para adicionar.")
	}

	var sb strings.Builder
	sb.WriteString("<b>Tokens extras cadastrados:</b>\n\n")
	for _, t := range tokens {
		status := "✅ ativo"
		if !t.Active {
			status = "❌ inativo"
		}
		masked := maskToken(t.Token)
		fmt.Fprintf(&sb, "<code>ID %d</code> — @%s — %s\n<code>%s</code>\n\n",
			t.ID, t.Username, status, masked)
	}
	sb.WriteString("Use /rmtoken &lt;ID&gt; para remover.")

	_, err = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID: m.Chat.ID, Text: sb.String(), ParseMode: "HTML",
	})
	return err
}

// handleRemoveToken processes "/rmtoken <ID>" — deactivates an extra token by DB ID.
func (p *Poller) handleRemoveToken(ctx context.Context, m *Message) error {
	if !p.isOwnerOrAdmin(senderID(m)) {
		return p.reply(ctx, m, "⛔ Apenas o owner/admin pode usar este comando.")
	}

	parts := strings.Fields(m.Text)
	if len(parts) < 2 {
		return p.reply(ctx, m, "Uso: /rmtoken <ID>\nVeja os IDs com /tokens")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return p.reply(ctx, m, "ID inválido — use um número inteiro.")
	}

	bt, err := p.deps.DB.BotTokenByID(ctx, id)
	if err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Erro: %v", err))
	}
	if bt == nil {
		return p.reply(ctx, m, fmt.Sprintf("Token com ID %d não encontrado.", id))
	}

	if err := p.deps.DB.DeleteBotToken(ctx, id); err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Erro ao remover: %v", err))
	}

	return p.reply(ctx, m,
		fmt.Sprintf("🗑 Token ID %d (@%s) removido.\n"+
			"A conexão MTProto existente encerrará quando o contexto for cancelado.\n"+
			"Reinicie o bot para garantir que não será mais usado.", id, bt.Username))
}

// reply is a convenience wrapper for sending plain-text replies.
func (p *Poller) reply(ctx context.Context, m *Message, text string) error {
	_, err := p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID: m.Chat.ID, Text: text,
	})
	return err
}

// senderID extracts the user ID from a message.
func senderID(m *Message) int64 {
	if m == nil || m.From == nil {
		return 0
	}
	return m.From.ID
}

// maskToken shows only the bot ID portion, hiding the secret part.
func maskToken(token string) string {
	id, _, found := strings.Cut(token, ":")
	if !found {
		return "***"
	}
	return id + ":***"
}
