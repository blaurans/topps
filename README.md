# topps

Suivi de collection de cartes **Topps Turbo Attax Formula 1 2026**.

Un binaire Go, une base SQLite, un dossier de visuels facultatif. Aucun
service externe : l'authentification est faite par Google dans
l'application, et chacun ne voit que ses propres quantités.

![collection](https://img.shields.io/badge/catalogue-423%20cartes-4ea3ff)

## Le catalogue

Le seed est produit depuis le **checklist officiel Topps**
(`F1_Turbo_Attax_2026_-_CHECKLIST.xlsx.pdf`, export des colonnes
`Card # / Name / Team / Subset`) :

| | |
|---|---|
| cartes numérotées | **353** — l'objectif de complétion |
| exclusifs | 70 (`GU`, `SUN`, `GG`, `LE`, `BG`, `MR-`, `RR-`), suivis mais hors objectif |
| sous-séries | 35 |
| cartes à variantes | 25 (les `TURBO CHROME`) |

Les exclusifs sont ceux qui n'existent pas dans le numbering : tins,
éditions limitées, reliques. `MR-` et `RR-` sont les reliques
manufacturées et les reliques de course ; `BG 1` et `BG 2` n'existent que
via Topps.com.

Pour régénérer le seed :

```sh
pdftotext -layout checklist.pdf checklist.txt
python3 tools/parse_checklist.py checklist.txt seed/
```

Le script échoue bruyamment si la numérotation n'est plus continue de 1 à
353, et signale tout changement du nombre de cartes sans nom : les onze
logos d'écuries sont les seules à n'avoir ni pilote, et ce compte est donc
attendu à 11. C'est le filet de sécurité contre une refonte du PDF.

### Variantes de rareté

Les `TURBO CHROME` existent en tirages numérotés :

| Base | Green | Gold | Orange | Red | Superfractor |
|---|---|---|---|---|---|
| — | /99 | /50 | /25 | /5 | 1/1 |

Une quantité est stockée par couple (carte, rareté) : trois exemplaires du
#316 dont un Gold, c'est `base=3, gold=1`. Une carte « possédée » est celle
dont la quantité de base est non nulle.

> Le libellé Topps rattache les tirages numérotés aux cartes Turbo Chrome.
> Si Topps les étend à d'autres séries, seul `VARIANT_SUBSETS` dans
> `tools/parse_checklist.py` est à changer.

## Comptes et accès

Chaque personne arrive par **Google** et ne voit que ses propres
quantités : le catalogue est commun, les inventaires ne le sont pas. Deux
comptes ouverts côte à côte n'ont aucun recouvrement.

### Le flux

OAuth 2.0 « authorization code » avec PKCE, écrit sans dépendance externe
(`auth.go`) :

1. `/auth/google` tire un `state` aléatoire, un `nonce` et un `verifier`
   PKCE, et n'enregistre que l'empreinte du `state` en base.
2. Google renvoie vers `/auth/callback`. Le `state` est consommé à ce
   moment-là : rejouer l'URL de retour est donc sans effet.
3. Le code est échangé contre un `id_token`, **dont la signature est
   vérifiée localement** avec les clés publiques publiées par Google.
4. Le compte est créé ou mis à jour d'après le `sub` — jamais d'après
   l'adresse e-mail, qui peut changer ou être recyclée.

Sont contrôlés, dans cet ordre : l'algorithme (RS256 seulement), la
signature, l'issuer, l'audience, l'`authorized party` quand l'audience est
multiple, l'expiration, le nonce et `email_verified`. Le tout est couvert
par `auth_test.go`, avec une clé RSA fabriquée pour l'occasion.

### Sessions

Cookie `tp_s` : `HttpOnly`, `Secure`, `SameSite=Lax`, valable 30 jours. La
base n'en conserve que l'empreinte SHA-256 — une fuite de la base ne donne
donc pas de quoi se connecter. `auth_pending` suit les connexions en cours
et se purge au bout de 15 minutes.

### Restreindre les comptes

`TOPPS_ALLOWED_EMAILS` liste les adresses autorisées, séparées par des
virgules. **Vide signifie que tout compte Google peut créer une
collection** : c'est le réglage retenu ici, le site n'est pas restreint. Le
journal le signale à chaque démarrage, pour que le choix reste visible.

Renseigner la variable suffit à verrouiller l'accès, sans redéploiement.

### La reprise des données existantes

Avant l'authentification, les quantités n'appartenaient à personne. À la
migration, un compte technique les porte ; la première personne qui se
connecte les récupère, et le compte technique disparaît. Si elle a déjà la
même carte cochée, la quantité existante est conservée.

## Utiliser

Clic **+1**, clic droit **−1**. Les pastilles d'un carton cochent une
rareté (0 → 1 → 0). Le mode **Manquantes** ne montre que ce qu'il reste :
la carte cochée disparaît et la sélection saute sur la suivante.

| Touche | Effet |
|---|---|
| `/` | recherche |
| un chiffre puis `Entrée` | coche la carte de ce numéro et avance |
| `+` / `-` | quantité de la carte sélectionnée |
| `n` | prochaine carte manquante |
| `m` | basculer le mode Manquantes |
| `i` | basculer l'affichage des visuels |
| `Échap` | tout désélectionner |

Les filtres (équipe, série, tri, mode Manquantes) sont mémorisés dans le
navigateur.

## Developper

```sh
mise use                     # Go 1.27
go test ./...
go run . -db ./data/topps.db -thumbs ./data/thumbnails
```

Puis <http://127.0.0.1:8080>.

## Deployer sur ociub

```sh
ssh ociub
git clone https://github.com/blaurans/topps /app/topps   # la premiere fois
cd /app/topps
sudo chown -R 10001:10001 data && sudo chmod 700 data     # le conteneur est non-root
docker compose up -d --build
```

Ensuite :

```sh
git pull --ff-only && docker compose up -d --build
```

Aucun port n'est publié : le service n'est atteignable que par Caddy, qui
termine le TLS. L'accès est contrôlé par l'authentification Google de
l'application (voir plus bas). Le réseau `guacamole_internal` est externe,
partagé avec les autres services du serveur, et le conteneur s'y attache
sous le nom `topps`.

### Configurer l'authentification Google

Une fois le dépôt cloné, créer le fichier d'environnement à partir du
modèle, et y coller les deux valeurs délivrées par Google :

```sh
cp .env.example .env && chmod 600 .env && $EDITOR .env
```

Le fichier `.env` est dans `.gitignore` : le secret client ne doit jamais
atterrir dans le dépôt.

Si les trois variables manquent, le serveur démarre quand même et le dit
dans ses journaux ; la page de connexion explique alors ce qui manque au
lieu de renvoyer une erreur technique.

### Deux particularites d'ociub

**Le DNS ne marche pas dans les conteneurs en pont.** Ils héritent d'un
nameserver link-local (`169.254.169.254`) non routable depuis leur
namespace. Sans effet sur l'application, qui ne résout que des noms de
service via le DNS intégré de Docker, mais le build échoue sur
`proxy.golang.org`. D'où `build.network: host` dans le compose : le build
utilise le réseau de l'hôte, l'exécution reste sur `guacamole_internal`.

**Le miroir APK d'Alpine n'est pas fiable depuis le VPS.** D'où pas de
`apk add` dans l'image finale : ni `ca-certificates` (seul `wget` vers
localhost est utilisé, la sonde de santé) ni `tzdata` (les journaux sont
en UTC). `adduser` vient de busybox.

### Reverse proxy

Le vhost est dans le Caddyfile de la stack Guacamole, qui possede deja les
ports 80 et 443 :

```sh
docker exec guacamole-caddy-1 caddy validate --config /etc/caddy/Caddyfile
docker exec guacamole-caddy-1 caddy reload  --config /etc/caddy/Caddyfile --force
```

Caddy resout `topps` au chargement de la configuration : apres un
`--force-recreate` qui changerait l'IP du conteneur, un `caddy reload`
peut etre necessaire. C'est le meme comportement que `driveco`.

### Sauvegarde

`backup.sh` utilise `VACUUM INTO` : une copie cohérente à chaud, sans
arrêter le service (un `cp` sur une base en WAL ne le serait pas). Il
tourne tous les jours a 4h17 via `/etc/cron.d/topps-backup` et garde 30
jours.

```sh
sh backup.sh                       # a la main
ls backups/
```

Restauration : arrêter le service, poser le fichier dans `data/topps.db`,
redémarrer.

## Visuels

Le serveur lit `--thumbs` (défaut `/data/thumbnails`) au démarrage et
n'expose un visuel que pour les fichiers présents ; sinon la carte reste
en texte. Les fichiers sont nommés d'après le code de la carte, avec
espaces et barres remplacés par des tirets : `GU 1` → `GU-1.jpg`,
`MR-NOR` → `MR-NOR.jpg`.

Trois sources, toutes deux sans OCR côté import automatisé — l'identification
se fait par le nom du fichier, le titre ou le lien, jamais en lisant un
pixel.

### Depuis ses propres photos

Le source le plus simple, et le meilleur rendu. Les photos sont nommées
d'après le numéro, l'import les confronte au catalogue et n'accepte que
ce qui existe vraiment :

```sh
python3 tools/import_photos.py ~/Documents/turbo-attax --stage /tmp/photos
```

| Fichier | Carte | Nom attendu |
|---|---|---|
| `301.jpg` | `301` | `301.jpg` |
| `GU1.jpg` | `GU 1` | `GU-1.jpg` |
| `SUN11.jpg` | `SUN 11` | `SUN-11.jpg` |
| `MR-HAM.jpg` | `MR-HAM` | `MR-HAM.jpg` |

Les HEIC d'iPhone sont convertis au passage. Deux formats sont acceptés,
car **certaines séries sont horizontales** : `63×88` et son inverse.

Trois refus, volontairement stricts — deviner serait pire que de ne rien
faire :

- un nom hors catalogue (`3470.jpg`) est ignoré ;
- un nom trop vague (`RR.jpg`) est signalé sans être deviné, car il existe
  dix cartes `RR-*` ;
- un format atypique est signalé sans bloquer, pour que le cas soit
  visible.

### Depuis le checklist en ligne (PriceCharting)

Les visuels ne sont pas servis par le site de checklist lui-même mais par
le bucket GCS de PriceCharting, où le nom de fichier est la largeur
demandée : seules 60, 120 et **240 px** existent. La page est derrière
Cloudflare et charge ses produits au défilement, donc il faut
**scroller jusqu'en bas avant d'enregistrer** la page.

```sh
python3 tools/fetch_images.py data/thumbnails <page.html> [page2.html ...]
```

Plusieurs fichiers peuvent être passés : les correspondances sont
fusionnées, ce qui permet de rattraper une collection sauvée en deux fois.

### Depuis eBay, en plein format

eBay refuse les requêtes simples et détecte le mode headless. Ce qui passe,
c'est un navigateur réel avec le profil du propriétaire : le script l'ouvre
sur le bureau, fait défiler les résultats, extrait les annonces et
télécharge chaque visuel en `s-l1600` — quatre fois la résolution des
visuels PriceCharting.

```sh
python3 tools/ebay_harvest.py data/thumbnails \
  "2026 turbo attax f1 gold" "2026 turbo attax turbo chrome" \
  "2026 turbo attax diamond infinity" "2026 turbo attax black edge"
```

L'identification se fait par le titre de l'annonce, dans l'ordre de
fiabilité : un code d'exclusif écrit en clair (`MR-HAM`, `LE4`), un numéro
précédé de `#`, sinon le nom du pilote croisé avec la sous-série. Les
annonces qui ne correspondent à rien sont ignorées plutôt que de risquer
de ranger une image sous le mauvais numéro.

Les images récupérées remplacent les basse résolution quand elles se
recouvrent. **Les 353 cartes numérotées ont un visuel**, et 381 cartes sur
423 en compte. Restent 42 exclusifs : les 17 `LE`, les 10 `RR`, les 8 `GG`,
les 5 `MR` et les 2 `BG`.

Pour savoir ce qui manque, à tout moment :

```sh
python3 tools/etat_visuels.py /app/topps/data/thumbnails
python3 tools/etat_visuels.py <dossier> --famille GG   # une famille à la fois
```

### Ajouter des images à la main

Déposer `<code>.jpg` dans le dossier et redémarrer le service.

### Pourquoi un panneau d'aperçu

Les visuels récupérés plafonnent autour de 240 px de haut, et ceux d'eBay
sont lourds (jusqu'à 517 Ko). À la taille d'une vignette de grille, ils
seraient illisibles ou lents. Le panneau affiche donc la carte sélectionnée
à sa taille réelle — c'est la confirmation du clic — pendant que le liseré
de la grille se pose déjà sur la manquante suivante.

## API

Toutes les routes `/api/` exigent une session ; sans cookie valide elles
renvoient `401`.

| | |
|---|---|
| `GET /api/me` | la personne connectée (`id`, `email`, `name`, `picture`) |
| `GET /api/catalogue` | le set entier : cartes, raretés, quantités **du demandeur** |
| `PUT /api/holding` | `{"card":"316","rarity":"base","qty":2}` |

Hors API, et publics : `/healthz` (sonde Docker), `/login`,
`/auth/google`, `/auth/callback`, `/logout` et `/static/`. Tout le reste
exige une session et redirige vers `/login`.

Les quantités sont **absolues** : le client calcule le delta, le serveur ne
fait pas d'arithmétique, un double-clic ne compte donc pas deux fois.
Une quantité négative est ramenée à zéro, pas rejetée.
