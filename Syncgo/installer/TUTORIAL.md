# Syncgo — Tutorial de Instalação e Uso

## O que é o Syncgo?

O **Syncgo** é um bot para Telegram que transforma um canal privado em servidor de streaming. Ele faz upload de vídeos pelo protocolo MTProto do Telegram e gera links HTTP para assistir diretamente em players como VLC, Kodi ou painéis IPTV (XUI ONE).

Funcionalidades principais:

- Streaming de vídeos hospedados no Telegram via HTTP
- Integração com painel XUI ONE (MySQL) para inserção automática de VODs e séries
- Importação de canais a partir de listas M3U/M3U8
- Importação de filmes e séries via Xtream Codes JSON API
- Metadados automáticos via TMDB (títulos, capas, categorias)
- Suporte a múltiplos tokens de bot para maior throughput

---

## Requisitos

| Item | Descrição |
|------|-----------|
| Servidor | Ubuntu 20.04+ (recomendado: 22.04 ou 24.04) |
| RAM | Mínimo 512 MB (recomendado: 1 GB+) |
| Disco | 2 GB livres (para Go, binário e banco de dados) |
| Rede | IP público ou domínio acessível |
| Telegram | Conta ativa para criar bot e canal |

---

## Passo 1 — Preparar credenciais do Telegram

Antes de instalar, você precisa de três informações do Telegram:

### 1.1 — API_ID e API_HASH

1. Acesse [https://my.telegram.org](https://my.telegram.org) e faça login com seu número de telefone
2. Clique em **API development tools**
3. Crie um aplicativo (nome e plataforma não importam)
4. Anote o **App api_id** e o **App api_hash**

### 1.2 — BOT_TOKEN

1. No Telegram, abra o [@BotFather](https://t.me/BotFather)
2. Envie `/newbot`
3. Escolha um nome e um username para o bot
4. Anote o token no formato `123456789:AAExemplo...`

### 1.3 — Canal de armazenamento (LOG_CHANNEL)

1. Crie um canal privado no Telegram (pode ser via app)
2. Adicione seu bot como **administrador** com permissão de postar mensagens
3. Acesse [https://web.telegram.org/k](https://web.telegram.org/k), abra o canal e copie o número da URL: `#-XXXXXXXXXX`
4. O LOG_CHANNEL é `-100` + esse número. Exemplo: `-1001234567890`

### 1.4 — OWNER_ID (seu ID pessoal)

1. No Telegram, abra o [@userinfobot](https://t.me/userinfobot) e envie qualquer mensagem
2. Ele responde com seu **Id**. Anote.

---

## Passo 2 — Instalar o Syncgo

Conecte-se ao servidor via SSH e execute **um único comando**:

```bash
sudo bash install.sh
```

Se o arquivo não estiver no servidor ainda, copie primeiro:

```bash
# No seu computador Windows (PowerShell):
scp installer\install.sh root@IP_DO_SERVIDOR:/tmp/install.sh

# No servidor:
sudo bash /tmp/install.sh
```

Ou, se tiver o projeto em um repositório Git:

```bash
SYNCGO_REPO=https://github.com/usuario/syncgo sudo bash /tmp/install.sh
```

O instalador irá:

1. Instalar Go e dependências automaticamente
2. Compilar o binário
3. Fazer perguntas sobre sua configuração (veja abaixo)
4. Criar o serviço `syncgo` no systemd
5. Iniciar o bot automaticamente

---

## Passo 3 — Configuração interativa

Durante a instalação o script faz perguntas. Pressione **Enter** para usar o valor padrão.

```
── Telegram ──────────────────────────────────────
  API_ID []:               → cole seu api_id
  API_HASH []:             → cole seu api_hash
  BOT_TOKEN []:            → cole o token do @BotFather
  LOG_CHANNEL []:          → ID do canal, ex: -1001234567890
  OWNER_ID [0]:            → seu ID do Telegram
  ADMINS []:               → IDs extras separados por vírgula (opcional)

── HTTP ──────────────────────────────────────────
  PORT [8080]:             → porta do servidor HTTP (mantenha 8080)
  FQDN []:                 → IP ou domínio público do servidor
  HAS_SSL [false]:         → true se tiver HTTPS com certificado
  NO_PORT [false]:         → true para omitir a porta na URL

── XUI MySQL (opcional) ─────────────────────────
  XUI_HOST []:             → IP do servidor MySQL do XUI (vazio = desativar)
  XUI_PORT [3306]:         → porta do MySQL
  XUI_USER []:             → usuário MySQL
  XUI_PASSWORD []:         → senha MySQL
  XUI_DATABASE [xui]:      → nome do banco
  XUI_SERVER_ID [1]:       → ID do servidor no XUI

── TMDB ──────────────────────────────────────────
  TMDB_API_KEY []:         → chave gratuita em themoviedb.org/settings/api
  TMDB_LANGUAGE [pt-BR]:   → idioma dos metadados

── XUI reload via SSH (opcional) ────────────────
  XUI_SSH_USER []:         → usuário SSH do servidor XUI (vazio = desativar)
  XUI_SSH_PASSWORD []:     → senha SSH
  XUI_RELOAD_CMD [sudo service xuione reload]:
```

---

## Passo 4 — Verificar se está funcionando

Após a instalação, o terminal mostra o status. Para verificar depois:

```bash
# Ver status do serviço
systemctl status syncgo

# Acompanhar logs em tempo real
journalctl -u syncgo -f

# Testar se o servidor HTTP responde
curl http://localhost:8080/health
```

Se tudo estiver ok, abra o Telegram, envie `/start` para o seu bot e ele deve responder.

---

## Passo 5 — Configurar o bot pelo Telegram

Após iniciar, acesse o menu do bot no Telegram:

| Comando | O que faz |
|---------|-----------|
| `/start` | Abre o menu principal |
| `/help` | Lista todos os comandos |
| `/settings` | Configura XUI, TMDB e outros via interface |
| `/addtoken` | Adiciona token de bot extra para upload paralelo |

Para sincronizar conteúdo via Xtream Codes:

1. Envie `/start` → clique em **⚙️ Configurações** → **Xtream / M3U**
2. Adicione a URL do seu provedor Xtream
3. Use o botão de sincronização para importar filmes ou séries

---

## Atualizar o Syncgo

Para recompilar com código novo mantendo todos os dados e configurações:

```bash
sudo bash /tmp/install.sh update
```

---

## Reiniciar / Parar

```bash
sudo systemctl restart syncgo   # reiniciar
sudo systemctl stop syncgo      # parar
sudo systemctl start syncgo     # iniciar
```

---

## Editar configuração depois da instalação

```bash
sudo nano /opt/syncgo/.env
sudo systemctl restart syncgo
```

---

## Ver logs

```bash
# Logs em tempo real (via systemd)
journalctl -u syncgo -f

# Arquivo de log (logrotate gerencia, 7 dias)
tail -f /var/log/syncgo/syncgo.log
```

---

## Desinstalar

```bash
sudo bash /tmp/install.sh uninstall
```

Remove o serviço, binário, dados e usuário. O `.env` e banco de dados são apagados — **faça backup antes**.

---

## Estrutura de diretórios após instalação

```
/opt/syncgo/
├── syncgo          ← binário compilado
├── .env            ← configurações (protegido, apenas root/syncgo)
├── syncgo.db       ← banco de dados SQLite local
├── sessions/       ← sessões MTProto dos bots
└── src/            ← código-fonte (se clonado do git)

/var/log/syncgo/
└── syncgo.log      ← logs do serviço

/etc/systemd/system/
└── syncgo.service  ← definição do serviço
```

---

## Problemas comuns

### Bot não inicia — "API_ID is required"

O `.env` não foi preenchido corretamente. Edite:
```bash
sudo nano /opt/syncgo/.env
sudo systemctl restart syncgo
```

### Porta 8080 já em uso

Outro processo usa a porta. Mude o `PORT` no `.env` ou pare o processo conflitante:
```bash
sudo lsof -i :8080
```

### XUI MySQL — connection refused

- Confirme que o MySQL aceita conexões remotas (bind-address no `/etc/mysql/mysql.conf.d/mysqld.cnf`)
- Verifique se o usuário MySQL tem permissão de acesso remoto:
  ```sql
  GRANT ALL ON xui.* TO 'usuario'@'%' IDENTIFIED BY 'senha';
  FLUSH PRIVILEGES;
  ```

### Upload lento / timeout no Telegram

Adicione mais tokens de bot com `/addtoken` no bot. Cada token tem limite independente de upload.

### Serviço para sozinho

Veja o motivo nos logs:
```bash
journalctl -u syncgo -n 50 --no-pager
```

---

## Firewall

Libere a porta HTTP do Syncgo:

```bash
sudo ufw allow 8080/tcp
sudo ufw reload
```

Se usar HTTPS com nginx como proxy reverso, libere apenas 80 e 443 e configure o `FQDN` e `HAS_SSL=true` e `NO_PORT=true`.

---

## Suporte

Dúvidas ou problemas: abra uma issue no repositório do projeto.
