#!/bin/bash
# Instala backup automático da VM do K8s Study Lab (rodar VIA az vm run-command).
#
# O estado dos usuários (contas, perfis, progresso, feedback) vive no volume
# docker lab-data, que mora no OS disk da VM — uma VM única, sem backup, era
# perda total em caso de corrupção. Este script instala um systemd timer que
# tira SNAPSHOT INCREMENTAL do OS disk (barato: só os deltas) no boot e a cada
# 24h enquanto a VM estiver de pé. Como o disco só muda com a VM ligada,
# snapshot-no-boot cobre o estado da sessão anterior mesmo com auto-stop.
# Retenção: 7 snapshots (prefixo k8slab-autobackup-, nome ordenável por data).
set -euo pipefail

cat > /opt/lab/backup-snapshot.sh <<'EOS'
#!/bin/bash
set -euo pipefail
# Config dir próprio: não disputa sessão az com run-lab.sh/autostop.
export AZURE_CONFIG_DIR=/opt/lab/.azure-backup
RG=k8slab-rg
VM=k8slab-vm
PREFIX=k8slab-autobackup
az login --identity >/dev/null
DISK_ID=$(az vm show -g "$RG" -n "$VM" --query 'storageProfile.osDisk.managedDisk.id' -o tsv)
NAME="$PREFIX-$(date +%Y%m%d-%H%M)"
az snapshot create -g "$RG" -n "$NAME" --source "$DISK_ID" --incremental true >/dev/null
echo "snapshot $NAME criado"
# Retenção: mantém os 7 mais novos (nome é ordenável por data)
az snapshot list -g "$RG" --query "[?starts_with(name, '$PREFIX')].name" -o tsv | sort | head -n -7 | while read -r old; do
  [ -n "$old" ] && az snapshot delete -g "$RG" -n "$old" >/dev/null && echo "removido $old"
done
EOS
chmod 755 /opt/lab/backup-snapshot.sh
bash -n /opt/lab/backup-snapshot.sh

cat > /etc/systemd/system/lab-backup.service <<'EOS'
[Unit]
Description=Snapshot incremental do disco (estado dos usuarios do K8s Study Lab)
After=network-online.target

[Service]
Type=oneshot
ExecStart=/opt/lab/backup-snapshot.sh
TimeoutStartSec=600
EOS

cat > /etc/systemd/system/lab-backup.timer <<'EOS'
[Unit]
Description=Backup diario + no boot (o disco so muda com a VM ligada)

[Timer]
OnBootSec=10min
OnUnitActiveSec=24h
Persistent=true

[Install]
WantedBy=timers.target
EOS

systemctl daemon-reload
systemctl enable --now lab-backup.timer
systemctl list-timers lab-backup.timer --no-pager
echo "BACKUP-INSTALADO"
