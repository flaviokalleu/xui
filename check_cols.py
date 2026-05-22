import paramiko
client = paramiko.SSHClient()
client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
client.connect('163.172.220.149', username='kalleu', password='H989PtVik3D6lnL', timeout=30, look_for_keys=False, allow_agent=False)
psql = "PGPASSWORD='H989PtVik3D6lnL' psql -h 127.0.0.1 -U evoticket -d evoticket"
_, stdout, _ = client.exec_command(f"{psql} -t -c \"SELECT column_name FROM information_schema.columns WHERE table_name='contacts' ORDER BY ordinal_position;\"")
print(stdout.read().decode())
client.close()
