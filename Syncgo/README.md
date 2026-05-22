# Syncgo

Bot do Telegram + servidor HTTP que transforma o Telegram em CDN para vídeos, áudios e documentos. Opcionalmente insere automaticamente no painel **XUI ONE / XtreamUI**.

## O que ele faz

```
Você manda um arquivo pro bot     →  Bot gera link HTTP de download/streaming
                                  →  (opcional) Insere automático no painel XUI
                                  →  (opcional) Importa canais a partir de M3U
```

**Fluxo automático para usuário leigo:**

1. Você nomeia o arquivo `240022.mp4` (filme) ou `240022_S01E02.mp4` (episódio) usando o ID do TMDB.
2. Manda no privado do bot.
3. Bot devolve 2 links: 📥 Download direto e 🖥 Player web.
4. Se XUI estiver configurado, ele já aparece no painel com capa, sinopse, gênero, ano, etc.

## Instalação rápida

### 1. Pré-requisitos

- **Go 1.26+** (o binário já vem compilado, ignore se for usar `syncgo.exe`)
- **Painel XUI ONE** com MySQL acessível (opcional)
- Conta no Telegram

### 2. Criar bot e canal

1. Abra `@BotFather` → `/newbot` → copie o **token**.
2. Crie um canal privado no Telegram → adicione o bot como **administrador** com permissão de "Postar mensagens".
3. Pegue o ID do canal:
   - Abra https://web.telegram.org/k/ → entre no canal → veja a URL `#-XXXXXXXXX`.
   - Use no `.env` como `LOG_CHANNEL=-100XXXXXXXXX` (prefixe com `-100`).

### 3. Configurar `.env`

Copie `.env.example` para `.env` e preencha:

```env
BOT_TOKEN=123456:ABC...     # do BotFather
LOG_CHANNEL=-1003718091048  # ID do canal (com -100)
FQDN=meudominio.com         # domínio público para os links
```

Se quiser auto-insert no XUI:

```env
XUI_HOST=ip-do-painel
XUI_USER=usuario-mysql
XUI_PASSWORD=senha-mysql
XUI_DATABASE=xui
TMDB_API_KEY=sua-chave-tmdb
```

### 4. Rodar

```bash
./syncgo.exe
```

Pronto. Bot fica online ouvindo o Telegram, servidor HTTP escutando na porta 8080.

## Comandos

| Comando | O que faz |
|---|---|
| `syncgo` | Roda o bot + servidor HTTP (modo padrão) |
| `syncgo channels import lista.m3u` | Importa canais de TV ao vivo de um arquivo M3U pro XUI |
| `syncgo help` | Mostra ajuda |

## Como nomear os arquivos

| Tipo | Padrão | Exemplo |
|---|---|---|
| Filme | `<TMDB_ID>.<ext>` | `550.mp4` (Fight Club) |
| Episódio | `<TMDB_ID>_S<temp>E<ep>.<ext>` | `1399_S01E01.mkv` (Game of Thrones T1E1) |

O bot busca os metadados (título, capa, sinopse, elenco, gênero) automaticamente no TMDB, cria a categoria certa, e adiciona ao bouquet `FILMES` ou `SÉRIES`.

Como achar o TMDB ID: https://www.themoviedb.org/search → abre o filme/série → o ID está na URL (`themoviedb.org/movie/550-fight-club` → ID = `550`).

## Importar canais de TV

```bash
./syncgo.exe channels import ./m3u/canais.m3u
```

O importer:
- Agrupa canais com o mesmo nome base (Globo SP, Globo RJ → "Rede Globo")
- Detecta categoria pelo nome (Globo, SBT, Esportes, Filmes, Infantis…)
- Cria categorias que faltarem
- Cria/atualiza o bouquet "CANAIS"
- Aceita o atributo `group-title="..."` do M3U como categoria explícita

## URLs geradas

- `http://seudominio:8080/{hash}{message_id}` — download/streaming direto (suporta seek de vídeo via HTTP Range)
- `http://seudominio:8080/watch/{hash}{message_id}` — player HTML5 simples

## Arquitetura

```
internal/
├── botapi/      Bot API HTTP (long polling getUpdates) — recebe mensagens
├── telegram/    MTProto client (gotd/td) — usado pra streaming
├── streamer/    Lê chunks de 1 MiB do Telegram via upload.GetFile
├── server/      net/http com suporte a Range, /watch, etc.
├── database/    SQLite local (mapping hash↔arquivo, users)
├── xui/         Driver MySQL pro painel XUI ONE
├── tmdb/        Cliente TMDB (filmes, séries, episódios)
├── parser/      Detecta filename pattern (movie / episode)
├── importer/    Cola tudo: parser → tmdb → xui (auto-insert)
└── m3u/         Parser de listas M3U/M3U8
```

## Observações

- **Bot API HTTP é usado pra receber mensagens** porque MTProto bot às vezes não recebe push updates depois de mudanças de modo. HTTP polling sempre funciona.
- **MTProto é usado só pra fazer streaming** dos bytes (rota `GET /xyz`).
- **Chunks de 1 MiB** com alinhamento de 4 KiB conforme exige o protocolo Telegram.
- **DC switching** (cross-DC) é tratado automaticamente pelo `gotd/td`.
- **localhost** não funciona em botões inline do Telegram — use domínio público ou ngrok.

## Solução de problemas

| Problema | Causa | Solução |
|---|---|---|
| Bot não responde | Mensagens antigas presas em fila Bot API | Já tratado por HTTP polling |
| Botão não aparece | URL é localhost/IP privado | Defina `FQDN=` com domínio público |
| `CHANNEL_PRIVATE` ao forwardar | Bot não é admin do `LOG_CHANNEL` | Adicione bot como admin |
| Auto-insert "skipped" | Filename não casa padrão | Renomeie pra `<TMDB_ID>.mp4` ou `<TMDB_ID>_S01E01.mp4` |
| TMDB 404 | ID errado | Confira em themoviedb.org |
| MySQL connection refused | Firewall / IP do XUI bloqueia | Libere o IP que roda o Syncgo |
