package botapi

import (
	"context"
	"fmt"
	"strings"
)

// settingsMeta describes every key that can be managed via /set and /settings.
var settingsMeta = []settingInfo{
	// Streaming
	{key: "max_streams_per_token", label: "Streams por token", group: "Streaming", sensitive: false},
	{key: "rate_limit_per_min", label: "Rate limit /watch (req/min, 0=off)", group: "Streaming", sensitive: false},
	{key: "multi_tokens", label: "Tokens extras (espaço/vírgula)", group: "Streaming", sensitive: true},
	// XUI MySQL
	{key: "xui_host", label: "XUI host", group: "XUI MySQL", sensitive: false},
	{key: "xui_port", label: "XUI porta", group: "XUI MySQL", sensitive: false},
	{key: "xui_user", label: "XUI usuário", group: "XUI MySQL", sensitive: false},
	{key: "xui_password", label: "XUI senha", group: "XUI MySQL", sensitive: true},
	{key: "xui_database", label: "XUI banco", group: "XUI MySQL", sensitive: false},
	{key: "xui_server_id", label: "XUI server_id", group: "XUI MySQL", sensitive: false},
	// XUI SSH reload
	{key: "xui_ssh_host", label: "SSH host (padrão=xui_host)", group: "XUI SSH", sensitive: false},
	{key: "xui_ssh_port", label: "SSH porta", group: "XUI SSH", sensitive: false},
	{key: "xui_ssh_user", label: "SSH usuário", group: "XUI SSH", sensitive: false},
	{key: "xui_ssh_password", label: "SSH senha", group: "XUI SSH", sensitive: true},
	{key: "xui_reload_cmd", label: "Reload cmd", group: "XUI SSH", sensitive: false},
	{key: "xui_reload_debounce_sec", label: "Reload debounce (s)", group: "XUI SSH", sensitive: false},
	// TMDB
	{key: "tmdb_api_key", label: "TMDB API key", group: "TMDB", sensitive: true},
	{key: "tmdb_language", label: "TMDB idioma (ex: pt-BR)", group: "TMDB", sensitive: false},
	// BotFarm
	{key: "botfarm_phone", label: "BotFarm telefone (+55...)", group: "BotFarm", sensitive: false},
	// Segurança / misc
	{key: "hash_secret", label: "Hash secret (assina URLs)", group: "Segurança", sensitive: true},
	{key: "auto_insert", label: "Auto-insert ao receber mídia (true/false)", group: "Geral", sensitive: false},
	{key: "metrics_enabled", label: "Métricas Prometheus (true/false)", group: "Geral", sensitive: false},
	{key: "shutdown_timeout", label: "Shutdown timeout (s)", group: "Geral", sensitive: false},
}

type settingInfo struct {
	key       string
	label     string
	group     string
	sensitive bool
}

func findSetting(key string) (settingInfo, bool) {
	for _, s := range settingsMeta {
		if s.key == key {
			return s, true
		}
	}
	return settingInfo{}, false
}

// handleSettings handles "/settings" — lists all known settings with current values.
func (p *Poller) handleSettings(ctx context.Context, m *Message) error {
	if !p.isOwnerOrAdmin(senderID(m)) {
		return p.reply(ctx, m, "⛔ Apenas o owner/admin pode ver as configurações.")
	}

	all, err := p.deps.DB.AllSettings(ctx)
	if err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Erro ao ler configurações: %v", err))
	}

	var sb strings.Builder
	sb.WriteString("<b>⚙️ Configurações do banco</b>\n")
	sb.WriteString("<i>Use /set chave valor para alterar</i>\n\n")

	currentGroup := ""
	for _, info := range settingsMeta {
		if info.group != currentGroup {
			currentGroup = info.group
			fmt.Fprintf(&sb, "\n<b>— %s —</b>\n", currentGroup)
		}
		val, ok := all[info.key]
		display := "<i>(padrão)</i>"
		if ok && val != "" {
			if info.sensitive {
				display = maskValue(val)
			} else {
				display = "<code>" + val + "</code>"
			}
		}
		fmt.Fprintf(&sb, "  <code>%s</code>: %s\n", info.key, display)
	}

	_, err = p.deps.API.SendMessage(ctx, SendMessageParams{
		ChatID:    m.Chat.ID,
		Text:      sb.String(),
		ParseMode: "HTML",
	})
	return err
}

// handleSet handles "/set <key> <value>" — persists a setting to the database.
func (p *Poller) handleSet(ctx context.Context, m *Message) error {
	if !p.isOwnerOrAdmin(senderID(m)) {
		return p.reply(ctx, m, "⛔ Apenas o owner/admin pode alterar configurações.")
	}

	parts := strings.SplitN(strings.TrimSpace(m.Text), " ", 3)
	if len(parts) < 3 {
		return p.reply(ctx, m,
			"Uso: /set <chave> <valor>\n"+
				"Ex: /set tmdb_api_key abc123\n"+
				"    /set tmdb_language pt-BR\n\n"+
				"Veja todas as chaves disponíveis com /settings")
	}
	key := strings.ToLower(parts[1])
	value := parts[2]

	meta, known := findSetting(key)
	if !known {
		return p.reply(ctx, m,
			fmt.Sprintf("❌ Chave desconhecida: <code>%s</code>\nVeja as chaves disponíveis com /settings", key))
	}

	if err := p.deps.DB.SetSetting(ctx, key, value); err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Erro ao salvar: %v", err))
	}

	display := "<code>" + value + "</code>"
	if meta.sensitive {
		display = maskValue(value)
	}

	return p.reply(ctx, m,
		fmt.Sprintf("✅ <b>%s</b> atualizado para %s\n\n"+
			"⚠️ Reinicie o bot para aplicar mudanças que afetam conexões (XUI, BotFarm, pool MTProto).\n"+
			"Mudanças de rate limit e TMDB são aplicadas no próximo restart automaticamente.",
			meta.label, display))
}

// handleUnset handles "/unset <key>" — removes a setting, reverting to the default.
func (p *Poller) handleUnset(ctx context.Context, m *Message) error {
	if !p.isOwnerOrAdmin(senderID(m)) {
		return p.reply(ctx, m, "⛔ Apenas o owner/admin pode alterar configurações.")
	}

	parts := strings.Fields(m.Text)
	if len(parts) < 2 {
		return p.reply(ctx, m, "Uso: /unset <chave>")
	}
	key := strings.ToLower(parts[1])

	if _, known := findSetting(key); !known {
		return p.reply(ctx, m,
			fmt.Sprintf("❌ Chave desconhecida: <code>%s</code>", key))
	}

	if err := p.deps.DB.DelSetting(ctx, key); err != nil {
		return p.reply(ctx, m, fmt.Sprintf("❌ Erro: %v", err))
	}

	return p.reply(ctx, m,
		fmt.Sprintf("🗑 <code>%s</code> removido — voltará ao valor padrão no próximo restart.", key))
}

// maskValue shows only the first 4 chars of a sensitive string.
func maskValue(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return v[:4] + strings.Repeat("*", len(v)-4)
}
