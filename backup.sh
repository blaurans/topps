#!/bin/sh
# Copie cohérente de la base, à chaud, sans arrêter le service.
#
#   sh backup.sh            une fois
#   0 4 * * * ... sh /app/topps/backup.sh   tous les jours
#
# VACUUM INTO produit un fichier compact et relisible par une autre
# instance ; un simple `cp` sur un SQLite en WAL donnerait une base
# incohérente.
set -eu

cd "$(dirname "$0")"
stamp=$(date +%F)
mkdir -p backups
chmod 777 backups 2>/dev/null || true

docker compose run --rm -T -v "$PWD/backups:/backup" topps \
	-backup "/backup/topps-$stamp.db"

find backups -name 'topps-*.db' -mtime +30 -delete
echo "$(date -Is) sauvegarde topps-$stamp.db"
