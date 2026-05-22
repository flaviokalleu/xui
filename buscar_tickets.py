#!/usr/bin/env python3
"""
buscar_tickets.py — Busca telefones dos tickets iniciados da Escorretora
"""

import sys

try:
    import paramiko
except ImportError:
    print("Dependencia ausente. Instale com: pip install paramiko")
    sys.exit(1)

HOST     = "163.172.220.149"
USER     = "kalleu"
PASSWORD = "H989PtVik3D6lnL"
DB_USER  = "evoticket"
DB_PASS  = "H989PtVik3D6lnL"
DB_NAME  = "evoticket"
OUTPUT   = "telefones_es_corretora.txt"

def ssh_run(client, cmd, timeout=60):
    _, stdout, stderr = client.exec_command(cmd, timeout=timeout)
    out = stdout.read().decode("utf-8", errors="replace")
    err = stderr.read().decode("utf-8", errors="replace")
    return out.strip(), err.strip()

def main():
    print(f"[*] Conectando em {USER}@{HOST} ...")
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASSWORD,
                   timeout=30, look_for_keys=False, allow_agent=False)
    print("[*] SSH OK")

    psql = f"PGPASSWORD='{DB_PASS}' psql -h 127.0.0.1 -U {DB_USER} -d {DB_NAME}"

    # Busca ticket_id, nome do contato e telefone
    query = """
SELECT
    t.id        AS ticket_id,
    t.status,
    c.name      AS contato,
    c.phone_number AS telefone
FROM tickets t
JOIN contacts c ON c.id = t.contact_id
WHERE t.company_id = 5
  AND t.deleted_at IS NULL
  AND t.status IN ('open','pending')
  AND c.phone_number IS NOT NULL
  AND c.phone_number <> ''
ORDER BY t.id;
"""
    print("[*] Consultando telefones...")
    out, err = ssh_run(client,
        f"{psql} -t -A -F'|' -c \"{query.strip()}\"", timeout=120)

    if err and 'ERROR' in err:
        print(f"[!] Erro: {err}")
        client.close()
        return

    lines = [l.strip() for l in out.splitlines() if l.strip()]
    print(f"[*] {len(lines)} registros encontrados")

    phones = []
    seen = set()
    for line in lines:
        parts = line.split('|')
        if len(parts) >= 4:
            ticket_id = parts[0].strip()
            status    = parts[1].strip()
            name      = parts[2].strip()
            phone     = parts[3].strip()
            phones.append((ticket_id, status, name, phone))
            seen.add(phone)

    with open(OUTPUT, "w", encoding="utf-8") as f:
        f.write(f"Empresa: Escorretora\n")
        f.write(f"Total de tickets: {len(phones)}\n")
        f.write(f"Telefones unicos: {len(seen)}\n")
        f.write("=" * 50 + "\n")
        f.write(f"{'Ticket':<10} {'Status':<10} {'Nome':<30} {'Telefone'}\n")
        f.write("-" * 50 + "\n")
        for ticket_id, status, name, phone in phones:
            f.write(f"{ticket_id:<10} {status:<10} {name:<30} {phone}\n")

    # Arquivo somente com telefones unicos
    with open("somente_telefones_escorretora.txt", "w", encoding="utf-8") as f:
        for phone in sorted(seen):
            f.write(phone + "\n")

    print(f"[OK] Salvo em '{OUTPUT}' ({len(phones)} linhas)")
    print(f"[OK] Somente telefones unicos em 'somente_telefones_escorretora.txt' ({len(seen)} numeros)")

    client.close()

if __name__ == "__main__":
    main()
