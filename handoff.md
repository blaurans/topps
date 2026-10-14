# Handoff — suivi de collection Topps Turbo Attax F1 2026

**Date** : 14 octobre 2026
**État** : en ligne et fonctionnel sur `https://topps.laurans.org`
**Dépôt** : `github.com/blaurans/topps` (public), branche `main`, commit `f7d18e6`

---

## 1. Décisions prises

| Sujet | Décision |
|---|---|
| Application | Suivi de collection **Topps Turbo Attax Formula 1 2026** (Topps, sorti le 23/07/2026) |
| Objectif de complétion | les **353 cartes numérotées** |
| Entrées hors objectif | 70 exclusifs (`GU`, `SUN`, `GG`, `LE`, `BG`, `MR-`, `RR-`) — suivis, pas comptés |
| Catalogue | seed depuis le **checklist officiel Topps** (`Card # / Name / Team / Subset`), pas depuis un site tiers |
| Visuels | **aucun** dans le dépôt ; lus depuis un dossier au démarrage, absents = carte en texte |
| Quantité | compteur par couple (carte, rareté) — les doublons comptent |
| Variantes | cases à cocher par rareté, uniquement sur les 25 `TURBO CHROME` (Green/99 → 1/1) |
| Périmètre | multi-sets dès le départ (table `sets`) |
| Historique | aucun — pas de dates, pas de prix, pas de sessions de booster |
| Stack | Go + SQLite (`modernc.org/sqlite`, Go pur, CGO désactivé), un binaire statique |
| Interface | grille texte + visuels, pas de framework JS |
| Exposition | `ociub.laurans.org`, vhost `topps.laurans.org` en **mTLS strict** |
| Déploiement | `ssh ociub` → `git pull` → `docker compose up -d --build` |
| Repo | **public** — le code et le catalogue sont visibles, la collection ne l'est jamais |
| Sauvegarde | `backup.sh` via `VACUUM INTO`, quotidienne à 4h17, rétention 30 jours |

---

## 2. Fichiers à rouvrir

### Dans le dépôt (`~/Work/topps/`)

| Fichier | Rôle |
|---|---|
| `main.go` | point d'entrée, routes, cycle de vie du serveur |
| `store.go` | SQLite : ouverture, migration, seed, requêtes, `SetQty`, `Backup` |
| `api.go` | helpers HTTP (`writeJSON`, `decodeJSON`, `httpError`) |
| `schema.sql` | 5 tables : `sets`, `cards`, `rarities`, `card_rarities`, `holdings` |
| `seed/turbo-attax-f1-2026.json` | le catalogue, 423 cartes, généré par `tools/parse_checklist.py` |
| `seed/images.json` | correspondance numéro → fichier image PriceCharting (144 entrées) |
| `web/index.html`, `web/app.js`, `web/style.css` | l'interface, sans étape de build |
| `tools/parse_checklist.py` | régénère le seed depuis le PDF officiel |
| `tools/fetch_images.py` | récupère les visuels PriceCharting depuis une page enregistrée |
| `tools/ebay_harvest.py` | récupère les visuels eBay en 1600 px, dans un navigateur réel |
| `backup.sh` | copie cohérente de la base à chaud |
| `Dockerfile`, `docker-compose.yml` | image et stack |
| `store_test.go` | 9 tests : intégrité du seed, idempotence, bornes, refus, sauvegarde |

### Hors dépôt

| Chemin | Rôle |
|---|---|
| `~/Documents/turbo-attax/` | le PDF officiel Topps + les pages enregistrées (sources du seed et des visuels) |
| `~/.pki/bastion.laurans.org/` | PKI mTLS — le certificat client `msa1` (valide jusqu'en 2028) permet d'ouvrir le site |
| `/app/topps/` sur ociub | le déploiement : code, `data/`, `backups/` |
| `/app/guacamole/Caddyfile` sur ociub | le vhost `topps.laurans.org` (sauvegarde `.bak.20261013-171815`) |

---

## 3. Ce qui a été fait et vérifié

### Application
- Binaire Go statique (~17 Mo), un conteneur, aucun service externe.
- **9 tests Go verts** : numérotation continue 1→353, seed idempotent (un redémarrage n'écrase pas les quantités), quantités bornées à 0, refus d'une rareté non allouée à une carte, sauvegarde relisible, `thumbKey` incapable de sortir du dossier.
- **Rendu réel en Chromium headless** : 423 cartes, compteurs exacts, zéro erreur console.
- **Parcours complet piloté par Chrome** : filtres, mode Manquantes, bascules, `n`, `Échap`, panneau d'aperçu.

### Déploiement
- Conteneur **healthy** depuis 11 h, 315 visuels indexés.
- Vhost Caddy validé puis rechargé ; certificat Let's Encrypt émis (valide jusqu'au 11 janvier 2027).
- **mTLS vérifié de l'extérieur** : sans certificat client, TLS répond `certificate required`.
- Sauvegarde quotidienne installée et une première copie effectuée.

### Visuels
- **315 cartes avec visuel** sur 423, dont **310 des 353 numérotées** et 5 exclusifs (`MR-VER`, `MR-HAM`, `MR-STE`, `MR-HAK`, `MR-MSC`).
- Deux sources : PriceCharting (240 px max) et eBay (1600 px, jusqu'à 517 Ko).

---

## 4. Problèmes encore ouverts

### 43 numérotées sans visuel
Presque toutes dans les séries rares, que les vendeurs photographient le moins :
les **F1 Gold (343-353)** et une partie des **Turbo Chrome (316-340)**.
→ `tools/ebay_harvest.py` avec d'autres requêtes est le moyen le plus direct de les compléter.

### La collection contient 1 carte possédée
Probablement un reste de mes tests. À remettre à zéro si ce n'est pas tienne.

### Les images eBay sont lourdes
Jusqu'à 517 Ko pièce, ~95 Mo au total. Le panneau d'aperçu charge l'image entière à chaque sélection.
→ Un redimensionnement à 480 px diviserait le poids par quatre sans perte visible. **Pas fait.**

### Caddy résout le nom de service au chargement
Après un `--force-recreate` qui changerait l'IP du conteneur, un `caddy reload` peut être nécessaire.
→ Documenté dans le README, **jamais rencontré** en pratique (l'IP est restée `172.18.0.7`).

### Le DNS ne marche pas dans les conteneurs en pont d'ociub
Sans effet sur l'application, mais le build a besoin de `build.network: host`.
→ Contourné dans le compose, **à ne pas retirer**.

### Le miroir APK d'Alpine est injoignable depuis le VPS
D'où l'absence de `apk add` dans l'image finale.
→ **À ne pas réintroduire** sans vérifier la connectivité.

---

## 5. Ce que je ne peux pas retrouver

**Je ne lis pas les images.** Chaque tentative d'ouverture d'un fichier image renvoie une erreur. Conséquences :

- **Je n'ai jamais vérifié visuellement** qu'une image correspond à son numéro. L'identification repose sur le titre de l'annonce ou le lien — fiable, mais pas infaillible.
- Les descriptions de cartes que j'ai pu donner en cours de route (« Andrea Stella sur la carte 2 », « Fernando Alonso n° 14 sur la 58 ») **étaient des inventions**. À ne pas tenir pour acquises.
- L'OCR ne tient pas sur ces vignettes (il lit « SAMLTON S MANEN FERRAN » pour « Hamilton Scuderia Ferrari ») — ce n'est pas un outil de vérification à ce format.

**À faire vérifier par un œil humain** : ouvrir quelques cartes au hasard et confirmer que le visuel correspond au numéro affiché.

---

## 6. Prochaine action

1. **Vérifier l'alignement des visuels** sur un échantillon au hasard (la seule chose que je ne peux pas faire).
2. Si des images sont mal rangées : corriger le nom du fichier dans `data/thumbnails/` et redémarrer.
3. Compléter les 43 trous avec `tools/ebay_harvest.py` et des requêtes ciblées.
4. Si le poids des images gêne : les redimensionner à 480 px.

---

## 7. Reprendre le projet

```sh
# local
cd ~/Work/topps
mise use && go test ./... && go run . -db ./data/topps.db -thumbs ./data/thumbnails

# serveur
ssh ociub
cd /app/topps && git pull && docker compose up -d --build

# sauvegarde manuelle
ssh ociub 'cd /app/topps && sh backup.sh'
```

Le README couvre le déploiement, les deux particularités d'ociub, le reverse proxy et les deux outils de récupération des visuels.
