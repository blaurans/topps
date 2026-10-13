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
git clone https://github.com/blaurans/topps /app/topps   # la première fois
cd /app/topps && git pull && docker compose up -d --build
```

Aucun port n'est publié : le service est joignable uniquement par Caddy,
qui sert le vhost `topps.laurans.org` **exigeant un certificat client**.
Le réseau `guacamole_internal` est externe, partagé avec les autres
services du serveur, et le conteneur s'y attache sous le nom `topps`.

### Sauvegarde

```sh
ssh ociub 'docker compose -f /app/topps/docker-compose.yml run --rm \
  -v /app/topps/backups:/backup topps -backup /backup/$(date +%F).db'
```

`VACUUM INTO` produit une copie cohérente à chaud, sans arrêter le service.

## Visuels

Le serveur lit `--thumbs` (défaut `/data/thumbnails`) au démarrage et
n'expose un visuel que pour les fichiers présents ; sinon la carte reste
en texte. Aucun visuel n'est versionné ici : la source disponible ne
proposait que des vignettes de 42×60 px, inutilisables.

Pour en ajouter, déposer `<code>.jpg` dans le dossier, avec le code
normalisé (`GU 1` → `GU-1.jpg`, `MR-NOR` → `MR-NOR.jpg`) et redémarrer.

## API

| | |
|---|---|
| `GET /api/catalogue` | le set entier : cartes, raretés, quantités |
| `PUT /api/holding` | `{"card":"316","rarity":"base","qty":2}` |

Les quantités sont **absolues** : le client calcule le delta, le serveur ne
fait pas d'arithmétique, un double-clic ne compte donc pas deux fois.
Une quantité négative est ramenée à zéro, pas rejetée.
