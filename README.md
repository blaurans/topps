# topps

Suivi de collection de cartes **Topps Turbo Attax Formula 1 2026**.

Un binaire Go, une base SQLite, un dossier de visuels facultatif. Aucun
service externe, aucun compte, aucune authentification dans l'application :
l'accès est filtré en amont par le reverse proxy, en mTLS.

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
sert le vhost `topps.laurans.org` **exigeant un certificat client**. Le
réseau `guacamole_internal` est externe, partagé avec les autres services
du serveur, et le conteneur s'y attache sous le nom `topps`.

### Deux particularites d'ociub

**Le DNS ne marche pas dans les conteneurs en pont.** Ils héritent d'un
nameserver link-local (`169.254.169.254`) non routable depuis leur
namespace. Sans effet sur l'application, qui ne résout que des noms de
service via le DNS intégré de Docker, mais le build échoue sur
`proxy.golang.org`. D'où `build.network: host` dans le compose : le build
utilise le réseau de l'hôte, l'exécution reste sur `guacamole_internal`.

**Le miroir APK d'Alpine n'est pas fiable depuis le VPS.** D'où pas de
`apk add` dans l'image finale : ni `ca-certificates` (l'application ne
sort pas en HTTPS) ni `tzdata` (les journaux sont en UTC). `adduser` vient
de busybox.

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

Deux sources automatisées, toutes deux sans OCR — l'identification se fait
par le titre ou le lien, jamais en lisant un pixel.

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
recouvrent. 315 visuels sont présents au total, dont 310 des 353 numérotées.

### Ajouter des images à la main

Déposer `<code>.jpg` dans le dossier et redémarrer le service.

### Pourquoi un panneau d'aperçu

Les visuels récupérés plafonnent autour de 240 px de haut, et ceux d'eBay
sont lourds (jusqu'à 517 Ko). À la taille d'une vignette de grille, ils
seraient illisibles ou lents. Le panneau affiche donc la carte sélectionnée
à sa taille réelle — c'est la confirmation du clic — pendant que le liseré
de la grille se pose déjà sur la manquante suivante.

## API

| | |
|---|---|
| `GET /api/catalogue` | le set entier : cartes, raretés, quantités |
| `PUT /api/holding` | `{"card":"316","rarity":"base","qty":2}` |

Les quantités sont **absolues** : le client calcule le delta, le serveur ne
fait pas d'arithmétique, un double-clic ne compte donc pas deux fois.
Une quantité négative est ramenée à zéro, pas rejetée.
