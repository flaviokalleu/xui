#!/usr/bin/env bash
# =============================================================================
#  Syncgo — Instalador para Ubuntu
#  Coloque este script na mesma pasta do binário "syncgo" e execute:
#
#    sudo bash install.sh             # instalar
#    sudo bash install.sh update      # substituir binário e reiniciar
#    sudo bash install.sh uninstall   # remover tudo
# =============================================================================
set -euo pipefail

# ── Cores ─────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

info()   { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()     { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()   { echo -e "${YELLOW}[AVISO]${NC} $*"; }
error()  { echo -e "${RED}[ERRO]${NC}  $*" >&2; exit 1; }
header() { echo -e "\n${BOLD}${BLUE}━━━  $*  ━━━${NC}\n"; }

# ── Constantes ─────────────────────────────────────────────────────────────────
APP_NAME="syncgo"
APP_USER="syncgo"
INSTALL_DIR="/opt/syncgo"
SERVICE_FILE="/etc/systemd/system/syncgo.service"
LOG_DIR="/var/log/syncgo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREBUILT="$SCRIPT_DIR/syncgo"

# -- Helpers -------------------------------------------------------------------
check_root() {
    [[ $EUID -eq 0 ]] || error "Execute como root:  sudo bash $0"
}

install_deps() {
    info "Instalando dependencias minimas..."
    apt-get update -qq
    apt-get install -y -qq curl ca-certificates psmisc 2>/dev/null || true
    ok "Dependencias prontas"
}

# ask_val "label" ["default"] ["secret"]
ask_val() {
    local label="$1" default="${2:-}" mode="${3:-}"
    if [[ -n "$default" ]]; then
        echo -e "${YELLOW}  ${label} [${CYAN}${default}${NC}${YELLOW}]:${NC}" >&2
    else
        echo -e "${YELLOW}  ${label}:${NC}" >&2
    fi
    local val=""
    if [[ "$mode" == "secret" ]]; then
        read -rs val </dev/tty; echo >&2
    else
        read -r val </dev/tty
    fi
    printf '%s' "${val:-$default}"
}

# -- Binario -------------------------------------------------------------------
install_binary() {
    header "Binario"
    [[ -f "$PREBUILT" ]] || error "Binario '$PREBUILT' nao encontrado. Coloque o arquivo 'syncgo' (Linux x86_64) ao lado de install.sh"

    local arch; arch=$(uname -m)
    [[ "$arch" == "x86_64" ]] || warn "Binario compilado para x86_64 mas servidor e $arch -- pode nao funcionar."

    # Para o servico se ja estiver rodando para liberar o arquivo
    systemctl stop "$APP_NAME" 2>/dev/null || true

    mkdir -p "$INSTALL_DIR"
    # Copia para arquivo temporario e substitui atomicamente (evita "Text file busy")
    cp "$PREBUILT" "$INSTALL_DIR/${APP_NAME}.new"
    mv -f "$INSTALL_DIR/${APP_NAME}.new" "$INSTALL_DIR/$APP_NAME"
    chmod 755 "$INSTALL_DIR/$APP_NAME"
    ok "Binario copiado para $INSTALL_DIR/$APP_NAME"
}

# ── Usuário e diretórios ───────────────────────────────────────────────────────
setup_dirs() {
    header "Diretórios"

    if ! id "$APP_USER" &>/dev/null; then
        useradd -r -s /bin/false -d "$INSTALL_DIR" -M "$APP_USER"
        ok "Usuário do sistema '$APP_USER' criado"
    else
        ok "Usuário '$APP_USER' já existe"
    fi

    mkdir -p "$INSTALL_DIR/sessions" "$LOG_DIR"
    chown -R "$APP_USER:$APP_USER" "$INSTALL_DIR" "$LOG_DIR"
    chmod 750 "$INSTALL_DIR" "$LOG_DIR"
    chmod 755 "$INSTALL_DIR/$APP_NAME"
    ok "Diretórios prontos"
}

# ── Configuração (.env) ────────────────────────────────────────────────────────
configure_env() {
    header "Configuração"

    local env_file="$INSTALL_DIR/.env"

    if [[ -f "$env_file" ]]; then
        echo -e "${YELLOW}  .env já existe. Reconfigurar? [s/N]:${NC}" >&2
        local r=""; read -r r </dev/tty
        if [[ "${r,,}" != "s" ]]; then
            ok "Mantendo .env existente"
            return
        fi
    fi

    echo ""
    echo -e "${BOLD}  Preencha as informações abaixo.${NC}"
    echo -e "  Pressione ${CYAN}Enter${NC} para usar o padrão [entre colchetes]."
    echo -e "  ${BLUE}Dica: XUI, TMDB e SSH podem ser configurados depois pelo próprio bot.${NC}"
    echo ""

    # ── Telegram ───────────────────────────────────────────────────────────────
    echo -e "${BOLD}── Telegram ──────────────────────────────────────────────${NC}"
    echo -e "  ${BLUE}↳ Obtenha API_ID e API_HASH em https://my.telegram.org${NC}"
    local api_id;      api_id=$(ask_val      "API_ID" "")
    local api_hash;    api_hash=$(ask_val    "API_HASH" "")
    echo -e "  ${BLUE}↳ Token obtido via @BotFather → /newbot${NC}"
    local bot_token;   bot_token=$(ask_val   "BOT_TOKEN" "")
    echo -e "  ${BLUE}↳ ID do canal privado onde os arquivos ficam (ex: -1001234567890)${NC}"
    local log_channel; log_channel=$(ask_val "LOG_CHANNEL" "")
    echo -e "  ${BLUE}↳ Seu ID numérico — envie /start para @userinfobot${NC}"
    local owner_id;    owner_id=$(ask_val    "OWNER_ID" "")
    local admins;      admins=$(ask_val      "ADMINS (IDs extras separados por vírgula, opcional)" "")

    # ── HTTP ────────────────────────────────────────────────────────────────────
    echo -e "\n${BOLD}── HTTP ──────────────────────────────────────────────────${NC}"
    local port;    port=$(ask_val    "PORT" "8080")
    local fqdn_default; fqdn_default=$(curl -fsSL --connect-timeout 4 ifconfig.me 2>/dev/null || echo "localhost")
    local fqdn;    fqdn=$(ask_val    "FQDN  (IP público ou domínio)" "$fqdn_default")
    local has_ssl; has_ssl=$(ask_val "HAS_SSL  (true/false)" "false")
    local no_port; no_port=$(ask_val "NO_PORT  (true = omitir porta da URL)" "false")

    # ── XUI MySQL ───────────────────────────────────────────────────────────────
    echo -e "\n${BOLD}── XUI MySQL  (opcional — pode configurar depois pelo bot) ──${NC}"
    local xui_host;      xui_host=$(ask_val      "XUI_HOST" "")
    local xui_port;      xui_port=$(ask_val      "XUI_PORT" "3306")
    local xui_user;      xui_user=$(ask_val      "XUI_USER" "")
    local xui_password;  xui_password=$(ask_val  "XUI_PASSWORD" "" secret)
    local xui_database;  xui_database=$(ask_val  "XUI_DATABASE" "xui")
    local xui_server_id; xui_server_id=$(ask_val "XUI_SERVER_ID" "1")

    # ── TMDB ────────────────────────────────────────────────────────────────────
    echo -e "\n${BOLD}── TMDB  (opcional — pode configurar depois pelo bot) ────────${NC}"
    echo -e "  ${BLUE}↳ Chave gratuita em https://www.themoviedb.org/settings/api${NC}"
    local tmdb_api_key;  tmdb_api_key=$(ask_val  "TMDB_API_KEY" "")
    local tmdb_language; tmdb_language=$(ask_val "TMDB_LANGUAGE" "pt-BR")

    # ── XUI SSH reload ──────────────────────────────────────────────────────────
    echo -e "\n${BOLD}── XUI reload via SSH  (opcional) ────────────────────────────${NC}"
    local xui_ssh_host;     xui_ssh_host=$(ask_val     "XUI_SSH_HOST" "")
    local xui_ssh_port;     xui_ssh_port=$(ask_val     "XUI_SSH_PORT" "22")
    local xui_ssh_user;     xui_ssh_user=$(ask_val     "XUI_SSH_USER" "")
    local xui_ssh_password; xui_ssh_password=$(ask_val "XUI_SSH_PASSWORD" "" secret)
    local xui_reload_cmd;   xui_reload_cmd=$(ask_val   "XUI_RELOAD_CMD" "sudo service xuione reload")
    local xui_reload_debounce; xui_reload_debounce=$(ask_val "XUI_RELOAD_DEBOUNCE_SEC" "30")

    # ── Avançado ────────────────────────────────────────────────────────────────
    echo -e "\n${BOLD}── Avançado ──────────────────────────────────────────────────${NC}"
    local workers;    workers=$(ask_val    "WORKERS" "4")
    local rate_limit; rate_limit=$(ask_val "RATE_LIMIT_PER_MIN" "120")

    # ── Gravar arquivo ──────────────────────────────────────────────────────────
    cat > "$env_file" <<ENVEOF
# ===== TELEGRAM =====
API_ID=${api_id}
API_HASH=${api_hash}
BOT_TOKEN=${bot_token}
LOG_CHANNEL=${log_channel}
OWNER_ID=${owner_id}
ADMINS=${admins}

# ===== HTTP =====
PORT=${port}
BIND_ADDRESS=0.0.0.0
FQDN=${fqdn}
HAS_SSL=${has_ssl}
NO_PORT=${no_port}

# ===== XUI MySQL =====
XUI_HOST=${xui_host}
XUI_PORT=${xui_port}
XUI_USER=${xui_user}
XUI_PASSWORD=${xui_password}
XUI_DATABASE=${xui_database}
XUI_SERVER_ID=${xui_server_id}

# ===== TMDB =====
TMDB_API_KEY=${tmdb_api_key}
TMDB_LANGUAGE=${tmdb_language}

# ===== XUI SSH reload =====
XUI_SSH_HOST=${xui_ssh_host}
XUI_SSH_PORT=${xui_ssh_port}
XUI_SSH_USER=${xui_ssh_user}
XUI_SSH_PASSWORD=${xui_ssh_password}
XUI_RELOAD_CMD=${xui_reload_cmd}
XUI_RELOAD_DEBOUNCE_SEC=${xui_reload_debounce}

# ===== Avancado =====
WORKERS=${workers}
RATE_LIMIT_PER_MIN=${rate_limit}
DB_PATH=${INSTALL_DIR}/syncgo.db
SESSION_DIR=${INSTALL_DIR}/sessions
SHUTDOWN_TIMEOUT=30
METRICS_ENABLED=true
ENVEOF

    chmod 640 "$env_file"
    chown "$APP_USER:$APP_USER" "$env_file"
    ok ".env salvo em $env_file"
}

# ── Serviço systemd ────────────────────────────────────────────────────────────
install_service() {
    header "Serviço systemd"

    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Syncgo -- Telegram streaming bot + XUI integration
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=${APP_USER}
Group=${APP_USER}
WorkingDirectory=${INSTALL_DIR}
EnvironmentFile=${INSTALL_DIR}/.env
ExecStart=${INSTALL_DIR}/${APP_NAME}
Restart=on-failure
RestartSec=5s
StandardOutput=append:${LOG_DIR}/syncgo.log
StandardError=append:${LOG_DIR}/syncgo.log
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=full
ProtectHome=yes

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable "$APP_NAME"
    ok "Serviço habilitado no boot"
}

# ── Logrotate ──────────────────────────────────────────────────────────────────
setup_logrotate() {
    cat > "/etc/logrotate.d/syncgo" <<EOF
${LOG_DIR}/syncgo.log {
    daily
    rotate 7
    compress
    missingok
    notifempty
    copytruncate
}
EOF
    ok "logrotate: 7 dias, comprimido"
}

# -- Inicializacao -------------------------------------------------------------
start_service() {
    header "Iniciando"

    # Libera a porta caso algum processo antigo ainda esteja rodando
    local port; port=$(grep '^PORT=' "$INSTALL_DIR/.env" 2>/dev/null | cut -d= -f2 || echo "8080")
    if command -v fuser &>/dev/null; then
        fuser -k "${port}/tcp" 2>/dev/null || true
    else
        # fuser nao disponivel -- tenta via ss + kill
        local pid; pid=$(ss -tlnp "sport = :${port}" 2>/dev/null | awk 'NR>1{match($0,/pid=([0-9]+)/,a); if(a[1]) print a[1]}' | head -1)
        [[ -n "$pid" ]] && kill -9 "$pid" 2>/dev/null || true
    fi

    systemctl start "$APP_NAME"

    # Aguarda ate 15s pelo servico subir (bot precisa de ~9s para conectar ao Telegram)
    local i=0
    while [[ $i -lt 15 ]]; do
        sleep 1; i=$((i+1))
        if systemctl is-active --quiet "$APP_NAME"; then
            ok "Syncgo rodando! (${i}s)"
            return
        fi
    done

    warn "Servico nao iniciou. Log:"
    journalctl -u "$APP_NAME" --no-pager -n 20
    error "Verifique as configuracoes em $INSTALL_DIR/.env e rode: systemctl start syncgo"
}

# ── Resumo ─────────────────────────────────────────────────────────────────────
print_summary() {
    local scheme="http" port="8080" fqdn="localhost" no_port="false" has_ssl="false"
    if [[ -f "$INSTALL_DIR/.env" ]]; then
        while IFS='=' read -r k v; do
            [[ "$k" =~ ^# ]] && continue
            case "$k" in
                PORT)    port="$v" ;;
                FQDN)    fqdn="$v" ;;
                HAS_SSL) has_ssl="$v" ;;
                NO_PORT) no_port="$v" ;;
            esac
        done < "$INSTALL_DIR/.env"
    fi
    [[ "$has_ssl" == "true" ]] && scheme="https"
    local url_port=""; [[ "$no_port" != "true" ]] && url_port=":${port}"
    local url="${scheme}://${fqdn}${url_port}"

    echo ""
    echo -e "${BOLD}${GREEN}╔══════════════════════════════════════════════╗${NC}"
    echo -e "${BOLD}${GREEN}║        Syncgo instalado com sucesso!         ║${NC}"
    echo -e "${BOLD}${GREEN}╚══════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "  ${BOLD}URL do servidor:${NC}  $url"
    echo -e "  ${BOLD}Configuracao:${NC}     $INSTALL_DIR/.env"
    echo -e "  ${BOLD}Logs:${NC}             $LOG_DIR/syncgo.log"
    echo ""
    echo -e "  ${BOLD}Comandos:${NC}"
    echo -e "    ${CYAN}systemctl status syncgo${NC}      ver status"
    echo -e "    ${CYAN}systemctl restart syncgo${NC}     reiniciar"
    echo -e "    ${CYAN}journalctl -u syncgo -f${NC}      logs ao vivo"
    echo -e "    ${CYAN}nano $INSTALL_DIR/.env${NC}       editar configuracao"
    echo ""
}

# ── Update ─────────────────────────────────────────────────────────────────────
do_update() {
    check_root
    header "Atualizando Syncgo"

    [[ -f "$PREBUILT" ]] || error "Binário '$PREBUILT' não encontrado ao lado do install.sh."
    [[ -d "$INSTALL_DIR" ]] || error "Syncgo não está instalado em $INSTALL_DIR."

    systemctl stop "$APP_NAME" 2>/dev/null || true
    cp "$PREBUILT" "$INSTALL_DIR/$APP_NAME"
    chmod 755 "$INSTALL_DIR/$APP_NAME"
    chown "$APP_USER:$APP_USER" "$INSTALL_DIR/$APP_NAME"
    systemctl start "$APP_NAME"
    sleep 2

    if systemctl is-active --quiet "$APP_NAME"; then
        ok "Syncgo atualizado e rodando!"
    else
        journalctl -u "$APP_NAME" --no-pager -n 20
        error "Serviço não iniciou após atualização."
    fi
}

# ── Uninstall ──────────────────────────────────────────────────────────────────
do_uninstall() {
    check_root
    header "Desinstalando Syncgo"

    echo -e "${RED}  ATENCAO: todos os dados serao apagados. Confirma? [s/N]:${NC}" >&2
    local r=""; read -r r </dev/tty
    [[ "${r,,}" == "s" ]] || { info "Cancelado."; exit 0; }

    systemctl stop "$APP_NAME" 2>/dev/null || true
    systemctl disable "$APP_NAME" 2>/dev/null || true
    rm -f "$SERVICE_FILE"
    systemctl daemon-reload
    rm -rf "$INSTALL_DIR" "$LOG_DIR" /etc/logrotate.d/syncgo
    userdel "$APP_USER" 2>/dev/null || true

    ok "Syncgo removido completamente."
}

# ── Banner ─────────────────────────────────────────────────────────────────────
print_banner() {
    echo -e "\n${BOLD}${BLUE}"
    echo "  ███████╗██╗   ██╗███╗   ██╗ ██████╗ ██████╗  ██████╗ "
    echo "  ██╔════╝╚██╗ ██╔╝████╗  ██║██╔════╝██╔════╝ ██╔═══██╗"
    echo "  ███████╗ ╚████╔╝ ██╔██╗ ██║██║     ██║  ███╗██║   ██║"
    echo "  ╚════██║  ╚██╔╝  ██║╚██╗██║██║     ██║   ██║██║   ██║"
    echo "  ███████║   ██║   ██║ ╚████║╚██████╗╚██████╔╝╚██████╔╝"
    echo "  ╚══════╝   ╚═╝   ╚═╝  ╚═══╝ ╚═════╝ ╚═════╝  ╚═════╝ "
    echo -e "${NC}"
}

# ── Main ───────────────────────────────────────────────────────────────────────
main() {
    print_banner

    case "${1:-install}" in
        install)
            check_root
            install_deps
            install_binary
            setup_dirs
            configure_env
            install_service
            setup_logrotate
            start_service
            print_summary
            ;;
        update|upgrade)
            do_update
            ;;
        uninstall|remove)
            do_uninstall
            ;;
        *)
            echo "Uso: sudo bash install.sh [install|update|uninstall]"
            exit 1
            ;;
    esac
}

main "$@"
