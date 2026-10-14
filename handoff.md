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
| **Comptes** | **authentification Google**, multi-utilisateur |
| **Données** | **une collection par compte** — le catalogue est commun, les quantités sont personnelles |
| **Barrière d'accès** | **plus de mTLS** : c'est Google qui filtre, Caddy ne fait plus que le TLS |
| **Liste blanche** | **aucune, définitivement** : tout compte Google peut créer une collection |
| Bibliothèque OAuth | **aucune** : `auth.go` écrit le flux et la vérification de jeton à la main |
| Repo | **public** — le code et le catalogue sont visibles, la collection ne l'est jamais |
| Sauvegarde | `backup.sh` via `VACUUM INTO`, quotidienne à 4h17, rétention 30 jours |

---

## 2. Fichiers à rouvrir

### Dans le dépôt (`~/Work/topps/`)

| Fichier | Rôle |
|---|---|
| `main.go` | point d'entrée, routes, cycle de vie du serveur |
| `auth.go` | OAuth Google : PKCE, échange du code, **vérification du jeton**, cache des clés |
| `session.go` | page de connexion, callback, sessions, middleware d'accès |
| `users.go` | comptes, sessions en base, reprise des quantités existantes |
| `store.go` | SQLite : ouverture, migration, seed, requêtes, `SetQty`, `Backup` |
| `api.go` | helpers HTTP (`writeJSON`, `decodeJSON`, `httpError`) |
| `schema.sql` | tables `users`, `sessions`, `auth_pending`, `sets`, `cards`, `rarities`, `card_rarities`, `holdings` |
| `seed/turbo-attax-f1-2026.json` | le catalogue, 423 cartes, généré par `tools/parse_checklist.py` |
| `seed/images.json` | correspondance numéro → fichier image PriceCharting (144 entrées) |
| `web/index.html`, `web/app.js`, `web/style.css` | l'interface, sans étape de build |
| `tools/parse_checklist.py` | régénère le seed depuis le PDF officiel |
| `tools/fetch_images.py` | récupère les visuels PriceCharting depuis une page enregistrée |
| `tools/ebay_harvest.py` | récupère les visuels eBay en 1600 px, dans un navigateur réel |
| `tools/import_photos.py` | importe des photos personnelles : HEIC → JPEG, nom confronté au catalogue |
| `.env.example` | **modèle** des identifiants Google — le vrai `.env` est sur le serveur, jamais versionné |
| `backup.sh` | copie cohérente de la base à chaud |
| `Dockerfile`, `docker-compose.yml` | image et stack |
| `store_test.go`, `auth_test.go` | **29 tests** |

### Hors dépôt

| Chemin | Rôle |
|---|---|
| `~/Documents/turbo-attax/` | le PDF officiel Topps + les pages enregistrées + **les photos de cartes** |
| `~/.pki/bastion.laurans.org/` | PKI mTLS — **n'est plus nécessaire pour ce site**, garde pour les autres |
| `/app/topps/.env` sur ociub | **client ID + secret Google**, `chmod 600`, hors du dépôt |
| `/app/topps/` sur ociub | le déploiement : code, `data/`, `backups/` |
| `/app/guacamole/Caddyfile` sur ociub | le vhost `topps.laurans.org` |

---

## 3. Ce qui a été fait et vérifié

### Application
- Binaire Go statique (~17 Mo), un conteneur, aucun service externe.
- **29 tests Go verts** : intégrité du seed, idempotence, bornes, refus,
  sauvegarde, **plus** 20 tests d'authentification — dont la vérification de
  jeton contre une clé RSA fabriquée (signature falsifiée, `alg` en
  `HS256`/`none`, clé inconnue, autre application, audience multiple sans
  `azp`, issuer inconnu, jeton expiré, e-mail non vérifié, mauvais nonce),
  l'isolation entre deux comptes, la reprise des quantités, et le fait que
  le jeton de session n'est jamais stocké en clair.
- **Rendu réel en Chromium** piloté en CDP : page de connexion, redirection
  de la racine sans session, 423 cartes affichées, identité en en-tête,
  clics qui persistent après rechargement, déconnexion. **Zéro erreur
  console.**

### Déploiement
- Conteneur **healthy**, 335 visuels indexés.
- Identifiants Google en place dans `/app/topps/.env` (mode `600`, hors du
  dépôt — vérifié par `git check-ignore`).
- **mTLS retiré** du vhost `topps.laurans.org`. Sauvegarde du Caddyfile :
  `.bak.20261014-183924`. Les deux autres vhosts mTLS (`bastion`,
  `charge`) sont intacts — vérifié : `bastion` répond toujours
  `tlsv13 alert certificate required` sans certificat.
- Flux OAuth vérifié de l'extérieur : `/auth/google` renvoie vers
  `accounts.google.com` avec le bon `client_id`, l'URI de redirection, un
  `state`, un `nonce` et un `code_challenge` S256.

### Visuels
- **Objectif complet : les 353 cartes numérotées ont un visuel.** 393 cartes
  sur 423 en compte.
- Trois sources : PriceCharting (240 px), eBay (1600 px), et les photos
  personnelles importées par `tools/import_photos.py` — 108 photos, dont
  des HEIC convertis.
- 40 exclusifs avec visuel : les 11 `GU`, les 11 `SUN`, `GG 4` à `GG 7`,
  les 5 `LE` trouvés (`LE 5`, `LE 6`, `LE 7`, `LE 8`, `LE 12`), 7 `MR`
  et 2 `BG`.
- **30 exclusifs restent sans image** : les 13 `LE`, les 10 `RR`, les 4 `GG`
  (`GG 1` à `GG 3` et `GG 8` — les quatre pilotes sur la piste), `MR-ALO`
  et `MR-RAI`, et `BG 1`.
  Deux passes eBay ont récupéré 12 exclusifs ; les `RR` ne se trouvent
  quasiment pas en vente à l'unité sur eBay.
- `tools/etat_visuels.py` liste ce qui manque, famille par famille.
- `tools/test_reconnaissance.py` (21 cas) verrouille l'identification des
  annonces : un motif faux rangerait une image sous la mauvaise carte.

---

## 4. Problèmes encore ouverts

### Le site est public — décision assumée, pas un oubli
Le mTLS a été retiré et `TOPPS_ALLOWED_EMAILS` reste vide : **tout compte
Google du monde peut créer une collection** sur `topps.laurans.org`. C'est
un choix explicite du propriétaire, assumé en connaissance de cause.

Ce que cela protège quand même : les collections restent séparées, et
personne ne voit les quantités d'un autre compte. Ce que cela n'empêche
pas : la création de comptes à la chaîne par des tiers, et donc des lignes
`users` inutiles. Le catalogue et les visuels, eux, sont publics de toute
façon (dépôt public, bucket d'images public).

→ Si l'envie vient de resserrer, une seule ligne dans `/app/topps/.env`
et un `docker compose up -d` : aucun redéploiement de code.

### La migration n'a pas encore tourné en production
Le code est prêt, la reprise est testée, mais le conteneur en ligne
tourne encore sur l'ancienne version mono-utilisateur. Au premier
redémarrage, les quantités existantes seront portées par un compte
technique, puis récupérées par la première personne qui se connecte.

### 24 numérotées sans visuel — **résolu**
Les 24 manquantes (F1 Gold, Turbo Chrome, fin de série) sont arrivées en
photos personnelles le 14 octobre. Il n'en reste plus.

### `RR.jpg` toujours non déployé
Le nom est ambigu : il existe 10 cartes `RR-*`. Il faut savoir laquelle.
`tools/import_photos.py` le refuse volontairement plutôt que de deviner.

### Deux photos au format atypique — conservées
`347.jpg` (1354×839) et `352.jpg` (1229×809) sont plus larges que le
format paysage attendu. Déployées, et **gardées telles quelles** : la photo
est mal prise mais elle est la bonne carte, ce qui vaut mieux qu'un trou.

### Les images eBay sont lourdes
Jusqu'à 517 Ko pièce, ~95 Mo au total. Le panneau d'aperçu charge l'image
entière à chaque sélection.
→ Un redimensionnement à 480 px diviserait le poids par quatre sans perte
visible. **Pas fait.**

### Caddy résout le nom de service au chargement
Après un `--force-recreate` qui changerait l'IP du conteneur, un
`caddy reload` peut être nécessaire.
→ Documenté dans le README, **jamais rencontré** en pratique.

### Le DNS ne marche pas dans les conteneurs en pont d'ociub
Sans effet sur l'application, mais le build a besoin de
`build.network: host`.
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

1. **Se connecter une fois sur `https://topps.laurans.org`** — c'est ce qui
   transfère les **17 quantités** actuellement portées par le compte
   technique vers ton compte. Tant que ce n'est pas fait, elles restent
   invisibles de l'interface (elles ne sont pas perdues).
2. Vérifier l'alignement des visuels sur un échantillon (la seule chose que
   je ne peux pas faire).
3. Compléter les 24 trous avec `tools/ebay_harvest.py` et des requêtes ciblées.
4. Trancher le sort de `RR.jpg`, toujours non déployé.

À noter : l'écran de consentement Google affiche un avertissement
« application non vérifiée » tant que l'application est en mode Test. C'est
normal pour un usage privé ; passer l'application en mode Production
supprime l'avertissement.

---

## 7. Reprendre le projet

```sh
# local
cd ~/Work/topps
mise use && go test ./...
go run . -db ./data/topps.db -thumbs ./data/thumbnails

# serveur
ssh ociub
cd /app/topps && git pull && docker compose up -d --build

# sauvegarde manuelle
ssh ociub 'cd /app/topps && sh backup.sh'
```

Le README couvre le déploiement, l'authentification, les deux
particularités d'ociub, le reverse proxy et les deux outils de récupération
des visuels.
