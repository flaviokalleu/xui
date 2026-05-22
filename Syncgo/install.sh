#!/usr/bin/env bash
# =============================================================================
#  Syncgo -- Instalador para Ubuntu (compila o Go no servidor)
#  Uso: curl -fsSL https://seu-servidor/install.sh | sudo bash
#       ou:  sudo bash install.sh
# =============================================================================
set -euo pipefail

# -- Cores --------------------------------------------------------------------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[AVISO]${NC} $*"; }
error()   { echo -e "${RED}[ERRO]${NC}  $*" >&2; exit 1; }
header()  { echo -e "\n${BOLD}${BLUE}---  $*  ---${NC}\n"; }

# -- Configuracoes ------------------------------------------------------------
APP_NAME="syncgo"
APP_USER="syncgo"
INSTALL_DIR="/opt/syncgo"
SERVICE_FILE="/etc/systemd/system/syncgo.service"
LOG_DIR="/var/log/syncgo"
GO_MIN_MAJOR=1
GO_MIN_MINOR=22
REPO_URL="${SYNCGO_REPO:-}"

# -- Pre-requisitos -----------------------------------------------------------
check_root() {
    [[ $EUID -eq 0 ]] || error "Execute como root: sudo bash $0"
}

check_ubuntu() {
    if ! grep -qi ubuntu /etc/os-release 2>/dev/null; then
        warn "Este instalador foi testado no Ubuntu. Outros distros podem funcionar."
    fi
}

# -- Dependencias -------------------------------------------------------------
install_deps() {
    header "Dependencias"
    apt-get update -qq
    apt-get install -y -qq git curl wget build-essential ca-certificates openssh-client
    ok "Pacotes instalados"
}

# -- Go -----------------------------------------------------------------------
install_go() {
    header "Go"

    if command -v go &>/dev/null; then
        local ver; ver=$(go version | awk '{print $3}' | sed 's/go//')
        local major; major=$(echo "$ver" | cut -d. -f1)
        local minor; minor=$(echo "$ver" | cut -d. -f2)
        if [[ $major -ge $GO_MIN_MAJOR && $minor -ge $GO_MIN_MINOR ]]; then
            ok "Go $ver ja instalado"
            return
        fi
        warn "Go $ver encontrado, mas necessario >= $GO_MIN_MAJOR.$GO_MIN_MINOR. Atualizando..."
    fi

    info "Buscando ultima versao estavel do Go..."
    local latest
    latest=$(curl -fsSL "https://go.dev/VERSION?m=text" | head -1 | sed 's/go//')
    info "Instalando Go $latest..."

    local arch; arch=$(uname -m)
    case "$arch" in
        x86_64)  arch="amd64" ;;
        aarch64) arch="arm64" ;;
        armv7l)  arch="armv6l" ;;
        *) error "Arquitetura nao suportada: $arch" ;;
    esac

    local tarball="go${latest}.linux-${arch}.tar.gz"
    wget -q --show-progress -O "/tmp/${tarball}" "https://dl.google.com/go/${tarball}"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${tarball}"
    rm -f "/tmp/${tarball}"

    if ! grep -q '/usr/local/go/bin' /etc/environment 2>/dev/null; then
        echo 'PATH="/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"' \
            > /etc/environment
    fi
    export PATH="/usr/local/go/bin:$PATH"
    ok "Go $(go version | awk '{print $3}') instalado"
}

# -- Codigo-fonte -------------------------------------------------------------
get_source() {
    header "Codigo-fonte"

    if [[ -f "$(dirname "$0")/go.mod" ]] && grep -q "^module syncgo" "$(dirname "$0")/go.mod" 2>/dev/null; then
        SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
        info "Usando codigo do diretorio atual: $SRC_DIR"
        return
    fi

    if [[ -d "$INSTALL_DIR/src/.git" ]]; then
        SRC_DIR="$INSTALL_DIR/src"
        info "Atualizando repositorio existente..."
        git -C "$SRC_DIR" pull --ff-only
        return
    fi

    if [[ -z "$REPO_URL" ]]; then
        echo -e "${YELLOW}URL do repositorio Git do Syncgo (ex: https://github.com/user/syncgo):${NC}"
        read -r REPO_URL
        [[ -n "$REPO_URL" ]] || error "URL do repositorio nao informada."
    fi

    SRC_DIR="$INSTALL_DIR/src"
    mkdir -p "$INSTALL_DIR"
    info "Clonando $REPO_URL -> $SRC_DIR..."
    git clone "$REPO_URL" "$SRC_DIR"
    ok "Repositorio clonado"
}

# -- Compilacao ---------------------------------------------------------------
build_binary() {
    header "Compilacao"
    info "Compilando syncgo..."
    export PATH="/usr/local/go/bin:$PATH"
    (
        cd "$SRC_DIR"
        go build -ldflags="-s -w" -o "$INSTALL_DIR/$APP_NAME" ./cmd/syncgo/
    )
    ok "Binario gerado em $INSTALL_DIR/$APP_NAME"
}

# -- Usuario e diretorios -----------------------------------------------------
setup_user_dirs() {
    header "Usuario e diretorios"

    if ! id "$APP_USER" &>/dev/null; then
        useradd -r -s /bin/false -d "$INSTALL_DIR" -M "$APP_USER"
        ok "Usuario $APP_USER criado"
    else
        ok "Usuario $APP_USER ja existe"
    fi

    mkdir -p "$INSTALL_DIR/sessions" "$LOG_DIR"
    chown -R "$APP_USER:$APP_USER" "$INSTALL_DIR" "$LOG_DIR"
    chmod 750 "$INSTALL_DIR" "$LOG_DIR"
    ok "Diretorios prontos"
}

# -- Helper de prompt ---------------------------------------------------------
# Escreve a pergunta em stderr para nao contaminar captura por $()
_ask() {
    local label="$1" default="${2:-}" secret="${3:-}" val
    if [[ -n "$default" ]]; then
        echo -e "${YELLOW}  $label${NC} [${CYAN}${default}${NC}]: " >&2
    else
        echo -e "${YELLOW}  $label${NC}: " >&2
    fi
    if [[ "$secret" == "secret" ]]; then
        read -rs val; echo >&2
    else
        read -r val
    fi
    printf '%s' "${val:-$default}"
}

# -- Configuracao (.env) ------------------------------------------------------
configure_env() {
    header "Configuracao (.env)"

    ENV_FILE="$INSTALL_DIR/.env"

    if [[ -f "$ENV_FILE" ]]; then
        echo -e "${YELLOW}.env ja existe em $ENV_FILE. Deseja reconfigurar? [s/N]${NC}"
        read -r resp
        [[ "${resp,,}" == "s" ]] || { ok "Mantendo .env existente"; return; }
    fi

    echo ""
    echo -e "${BOLD}  Preencha as informacoes abaixo.${NC}"
    echo -e "  Pressione ${CYAN}Enter${NC} para usar o padrao [entre colchetes]."
    echo -e "  ${BLUE}Dica: XUI, TMDB e SSH podem ser configurados depois pelo proprio bot.${NC}\n"

    # -- Telegram -------------------------------------------------------------
    echo -e "\n${BOLD}-- Telegram --------------------------------------------------${NC}"
    echo -e "  ${BLUE}Acesse https://my.telegram.org para obter API_ID e API_HASH${NC}"
    API_ID=$(_ask "API_ID" "")
    API_HASH=$(_ask "API_HASH" "")
    echo -e "  ${BLUE}Token obtido via @BotFather -> /newbot${NC}"
    BOT_TOKEN=$(_ask "BOT_TOKEN" "")
    echo -e "  ${BLUE}ID do canal privado onde os arquivos ficam (ex: -1001234567890)${NC}"
    LOG_CHANNEL=$(_ask "LOG_CHANNEL" "")
    echo -e "  ${BLUE}Seu ID numerico -- envie /start para @userinfobot${NC}"
    OWNER_ID=$(_ask "OWNER_ID" "")
    ADMINS=$(_ask "ADMINS adicionais (IDs separados por virgula, opcional)" "")

    # -- HTTP -----------------------------------------------------------------
    echo -e "\n${BOLD}-- HTTP ------------------------------------------------------${NC}"
    PORT=$(_ask "PORT" "8080")
    AUTO_IP=$(curl -fsSL --connect-timeout 5 ifconfig.me 2>/dev/null || echo "")
    FQDN=$(_ask "FQDN (dominio ou IP publico do servidor)" "${AUTO_IP:-localhost}")
    HAS_SSL=$(_ask "HAS_SSL (true/false)" "false")
    NO_PORT=$(_ask "NO_PORT -- omitir porta na URL? (true/false)" "false")

    # -- XUI MySQL ------------------------------------------------------------
    echo -e "\n${BOLD}-- XUI MySQL  (opcional -- pode configurar depois pelo bot) -${NC}"
    XUI_HOST=$(_ask "XUI_HOST" "")
    XUI_PORT=$(_ask "XUI_PORT" "3306")
    XUI_USER=$(_ask "XUI_USER" "")
    XUI_PASSWORD=$(_ask "XUI_PASSWORD" "" secret)
    XUI_DATABASE=$(_ask "XUI_DATABASE" "xui")
    XUI_SERVER_ID=$(_ask "XUI_SERVER_ID" "1")

    # -- TMDB -----------------------------------------------------------------
    echo -e "\n${BOLD}-- TMDB  (opcional -- pode configurar depois pelo bot) -------${NC}"
    echo -e "  ${BLUE}Chave gratuita em https://www.themoviedb.org/settings/api${NC}"
    TMDB_API_KEY=$(_ask "TMDB_API_KEY" "")
    TMDB_LANGUAGE=$(_ask "TMDB_LANGUAGE" "pt-BR")

    # -- XUI SSH reload -------------------------------------------------------
    echo -e "\n${BOLD}-- XUI reload via SSH  (opcional) ----------------------------${NC}"
    XUI_SSH_HOST=$(_ask "XUI_SSH_HOST" "")
    XUI_SSH_PORT=$(_ask "XUI_SSH_PORT" "22")
    XUI_SSH_USER=$(_ask "XUI_SSH_USER" "")
    XUI_SSH_PASSWORD=$(_ask "XUI_SSH_PASSWORD" "" secret)
    XUI_RELOAD_CMD=$(_ask "XUI_RELOAD_CMD" "sudo service xuione reload")
    XUI_RELOAD_DEBOUNCE=$(_ask "XUI_RELOAD_DEBOUNCE_SEC" "30")

    # -- Avancado -------------------------------------------------------------
    echo -e "\n${BOLD}-- Avancado --------------------------------------------------${NC}"
    WORKERS=$(_ask "WORKERS" "4")
    RATE_LIMIT=$(_ask "RATE_LIMIT_PER_MIN" "120")

    # -- Grava o arquivo ------------------------------------------------------
    cat > "$ENV_FILE" <<ENVEOF
# ===== TELEGRAM =====
API_ID=${API_ID}
API_HASH=${API_HASH}
BOT_TOKEN=${BOT_TOKEN}
LOG_CHANNEL=${LOG_CHANNEL}
OWNER_ID=${OWNER_ID}
ADMINS=${ADMINS}

# ===== HTTP =====
PORT=${PORT}
BIND_ADDRESS=0.0.0.0
FQDN=${FQDN}
HAS_SSL=${HAS_SSL}
NO_PORT=${NO_PORT}

# ===== XUI MySQL =====
XUI_HOST=${XUI_HOST}
XUI_PORT=${XUI_PORT}
XUI_USER=${XUI_USER}
XUI_PASSWORD=${XUI_PASSWORD}
XUI_DATABASE=${XUI_DATABASE}
XUI_SERVER_ID=${XUI_SERVER_ID}

# ===== TMDB =====
TMDB_API_KEY=${TMDB_API_KEY}
TMDB_LANGUAGE=${TMDB_LANGUAGE}

# ===== XUI SSH reload =====
XUI_SSH_HOST=${XUI_SSH_HOST}
XUI_SSH_PORT=${XUI_SSH_PORT}
XUI_SSH_USER=${XUI_SSH_USER}
XUI_SSH_PASSWORD=${XUI_SSH_PASSWORD}
XUI_RELOAD_CMD=${XUI_RELOAD_CMD}
XUI_RELOAD_DEBOUNCE_SEC=${XUI_RELOAD_DEBOUNCE}

# ===== Avancado =====
WORKERS=${WORKERS}
RATE_LIMIT_PER_MIN=${RATE_LIMIT}
DB_PATH=${INSTALL_DIR}/syncgo.db
SESSION_DIR=${INSTALL_DIR}/sessions
SHUTDOWN_TIMEOUT=30
METRICS_ENABLED=true
ENVEOF

    chmod 640 "$ENV_FILE"
    chown "$APP_USER:$APP_USER" "$ENV_FILE"
    ok ".env salvo em $ENV_FILE"
}

# -- Servico systemd ----------------------------------------------------------
install_service() {
    header "Servico systemd"

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
    ok "Servico habilitado no boot"
}

# -- Logrotate ----------------------------------------------------------------
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

# -- Inicializacao ------------------------------------------------------------
start_service() {
    header "Iniciando"
    local port; port=$(grep '^PORT=' "$INSTALL_DIR/.env" 2>/dev/null | cut -d= -f2 || echo "8080")
    fuser -k "${port}/tcp" 2>/dev/null || true
    systemctl restart "$APP_NAME"
    sleep 3
    if systemctl is-active --quiet "$APP_NAME"; then
        ok "Syncgo esta rodando!"
    else
        warn "Servico nao iniciou. Ultimas linhas do log:"
        journalctl -u "$APP_NAME" --no-pager -n 30
        error "Verifique as configuracoes em $INSTALL_DIR/.env e rode: systemctl start syncgo"
    fi
}

# -- Resumo final -------------------------------------------------------------
print_summary() {
    local scheme="http"
    [[ "${HAS_SSL:-false}" == "true" ]] && scheme="https"
    local url_port=""; [[ "${NO_PORT:-false}" != "true" ]] && url_port=":${PORT:-8080}"
    local url="${scheme}://${FQDN:-localhost}${url_port}"

    echo ""
    echo -e "${BOLD}${GREEN}+----------------------------------------------+${NC}"
    echo -e "${BOLD}${GREEN}|      Syncgo instalado com sucesso!           |${NC}"
    echo -e "${BOLD}${GREEN}+----------------------------------------------+${NC}"
    echo ""
    echo -e "  ${BOLD}URL do servidor:${NC}  $url"
    echo -e "  ${BOLD}Instalacao:${NC}       $INSTALL_DIR"
    echo -e "  ${BOLD}Configuracao:${NC}     $INSTALL_DIR/.env"
    echo -e "  ${BOLD}Logs:${NC}             $LOG_DIR/syncgo.log"
    echo ""
    echo -e "  ${BOLD}Comandos uteis:${NC}"
    echo -e "    ${CYAN}systemctl status syncgo${NC}       -- ver status"
    echo -e "    ${CYAN}systemctl restart syncgo${NC}      -- reiniciar"
    echo -e "    ${CYAN}journalctl -u syncgo -f${NC}       -- logs ao vivo"
    echo -e "    ${CYAN}nano $INSTALL_DIR/.env${NC}  -- editar configuracao"
    echo ""
}

# -- Atualizacao --------------------------------------------------------------
do_update() {
    check_root
    header "Atualizando Syncgo"

    export PATH="/usr/local/go/bin:$PATH"
    command -v go &>/dev/null || error "Go nao encontrado. Execute a instalacao completa primeiro."

    if [[ -d "$INSTALL_DIR/src/.git" ]]; then
        info "Atualizando repositorio..."
        git -C "$INSTALL_DIR/src" pull --ff-only
        SRC_DIR="$INSTALL_DIR/src"
    elif [[ -f "$(pwd)/go.mod" ]] && grep -q "^module syncgo" "$(pwd)/go.mod"; then
        SRC_DIR="$(pwd)"
    else
        error "Codigo-fonte nao encontrado. Forneca SYNCGO_REPO=<url> ou rode do diretorio do projeto."
    fi

    info "Compilando..."
    systemctl stop "$APP_NAME" 2>/dev/null || true
    (cd "$SRC_DIR" && go build -ldflags="-s -w" -o "$INSTALL_DIR/$APP_NAME" ./cmd/syncgo/)
    chown "$APP_USER:$APP_USER" "$INSTALL_DIR/$APP_NAME"
    systemctl start "$APP_NAME"

    ok "Syncgo atualizado e reiniciado!"
    systemctl status "$APP_NAME" --no-pager -l
}

# -- Desinstalacao ------------------------------------------------------------
do_uninstall() {
    check_root
    header "Desinstalando Syncgo"

    echo -e "${YELLOW}Tem certeza que deseja remover o Syncgo? TODOS os dados serao apagados! [s/N]${NC}"
    read -r resp
    [[ "${resp,,}" == "s" ]] || { info "Cancelado."; exit 0; }

    systemctl stop "$APP_NAME" 2>/dev/null || true
    systemctl disable "$APP_NAME" 2>/dev/null || true
    rm -f "$SERVICE_FILE"
    systemctl daemon-reload
    rm -rf "$INSTALL_DIR" "$LOG_DIR" /etc/logrotate.d/syncgo
    userdel "$APP_USER" 2>/dev/null || true

    ok "Syncgo removido completamente."
}

# -- Entrypoint ---------------------------------------------------------------
main() {
    echo -e "\n${BOLD}${BLUE}"
    echo "  ######  ##    ## ##    ##  ######   ######   ###### "
    echo "  ##       ##  ##  ###   ## ##    ## ##    ## ##    ##"
    echo "  ######    ####   ## ## ## ##       ##       ##    ##"
    echo "       ##    ##    ##  #### ##    ## ##    ## ##    ##"
    echo "  ######     ##    ##   ###  ######   ######   ###### "
    echo -e "${NC}"

    case "${1:-install}" in
        install)
            check_root
            check_ubuntu
            install_deps
            install_go
            get_source
            build_binary
            setup_user_dirs
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
            echo "  install    -- instalacao completa (padrao)"
            echo "  update     -- recompila e reinicia (mantem .env e dados)"
            echo "  uninstall  -- remove tudo"
            exit 1
            ;;
    esac
}

main "$@"
