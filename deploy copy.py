#!/usr/bin/env python3
"""
deploy.py — Atualiza os containers Docker nas 3 VPSs do Evoticket em paralelo.

Uso:
    pip install paramiko
    python deploy.py
"""

import threading
import sys
import time
from datetime import datetime

try:
    import paramiko
except ImportError:
    print("Dependência ausente. Instale com: pip install paramiko")
    sys.exit(1)

# ── Configuração dos servidores ───────────────────────────────────────────────

SERVERS = [
    {
        "name": "VPS-Pessoal (163.172.220.149)",
        "host": "163.172.220.149",
        "user": "kalleu",
        "password": "H989PtVik3D6lnL",
        "path": "/opt/evoticket",
        "color": "\033[36m",   # ciano
    },
  
]

# ── Constantes ────────────────────────────────────────────────────────────────

RESET = "\033[0m"
RED   = "\033[31m"
BOLD  = "\033[1m"
DIM   = "\033[2m"

DEPLOY_CMD = """\
set -e
cd {path}
echo '>>> Puxando imagens mais recentes...'
docker compose pull
echo '>>> Subindo containers...'
docker compose up -d --remove-orphans
echo '>>> Status dos containers:'
docker compose ps --format 'table {{{{.Name}}}}\t{{{{.Status}}}}\t{{{{.Ports}}}}'
"""

# ── Estado compartilhado ──────────────────────────────────────────────────────

results: dict[str, tuple[str, str | None]] = {}
print_lock = threading.Lock()


def log(name: str, color: str, message: str) -> None:
    with print_lock:
        ts = time.strftime("%H:%M:%S")
        prefix = f"{DIM}{ts}{RESET} {color}{BOLD}[{name}]{RESET}"
        print(f"{prefix} {message}", flush=True)


# ── Deploy de um servidor ─────────────────────────────────────────────────────

def deploy(server: dict) -> None:
    name     = server["name"]
    color    = server["color"]
    host     = server["host"]
    user     = server["user"]
    password = server["password"]
    path     = server["path"]

    start = time.monotonic()

    try:
        log(name, color, f"Conectando em {user}@{host} …")

        client = paramiko.SSHClient()
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        client.connect(
            host,
            username=user,
            password=password,
            timeout=30,
            look_for_keys=False,
            allow_agent=False,
        )

        log(name, color, "SSH OK — iniciando deploy …")

        cmd = DEPLOY_CMD.format(path=path)
        _, stdout, stderr = client.exec_command(cmd, get_pty=True, timeout=600)

        # Stream de saída em tempo real
        for raw_line in stdout:
            line = raw_line.rstrip("\r\n")
            if line:
                log(name, color, line)

        exit_code = stdout.channel.recv_exit_status()
        elapsed = time.monotonic() - start

        if exit_code == 0:
            log(name, color, f"✓ Deploy concluído em {elapsed:.0f}s")
            results[name] = ("ok", None)
        else:
            err = stderr.read().decode("utf-8", errors="replace").strip()
            msg = f"exit {exit_code}" + (f" — {err}" if err else "")
            log(name, RED, f"✗ Falhou: {msg}")
            results[name] = ("fail", msg)

        client.close()

    except Exception as exc:
        elapsed = time.monotonic() - start
        log(name, RED, f"✗ Erro após {elapsed:.0f}s: {exc}")
        results[name] = ("fail", str(exc))


# ── Ponto de entrada ──────────────────────────────────────────────────────────

def main() -> None:
    now = datetime.now().strftime("%d/%m/%Y %H:%M:%S")
    print(f"\n{BOLD}{'═' * 60}{RESET}")
    print(f"{BOLD}  Deploy Evoticket — {now}{RESET}")
    print(f"{BOLD}  Servidores: {len(SERVERS)}  │  Modo: paralelo{RESET}")
    print(f"{BOLD}{'═' * 60}{RESET}\n")

    threads = [threading.Thread(target=deploy, args=(s,), daemon=True) for s in SERVERS]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    # ── Resumo ────────────────────────────────────────────────────────────────
    print(f"\n{BOLD}{'─' * 60}{RESET}")
    print(f"{BOLD}  Resultado:{RESET}")
    print(f"{BOLD}{'─' * 60}{RESET}")

    all_ok = True
    for server in SERVERS:
        name   = server["name"]
        color  = server["color"]
        status, msg = results.get(name, ("fail", "não executado"))
        if status == "ok":
            print(f"  {color}{BOLD}✓  {name}{RESET}")
        else:
            all_ok = False
            print(f"  {RED}{BOLD}✗  {name}{RESET}  →  {msg}")

    print(f"{BOLD}{'═' * 60}{RESET}\n")

    sys.exit(0 if all_ok else 1)


if __name__ == "__main__":
    main()
