-- Suivi de collection de cartes Topps.
--
-- Un « set » est une édition (ici Turbo Attax F1 2026). Les cartes ont un
-- code : soit le numéro imprimé (1-353), soit un code d'exclusif
-- (« GU 1 », « MR-NOR ») pour ce qui n'existe pas dans le numbering.
--
-- Les variantes de rareté vivent à part : une carte peut avoir plusieurs
-- tirages (les Turbo Chrome numérotés Green/99 -> 1/1), et l'on possède
-- un compte par couple (carte, rareté).
--
-- Multi-utilisateur : le catalogue (sets, cards, rarities) est commun et
-- en lecture seule ; seules les quantités sont personnelles, rattachées
-- à un users.id obtenu via l'authentification Google.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY,
    -- Identifiant opaque et stable fourni par Google (OID). C'est lui qui
    -- fait foi : une adresse Gmail peut changer, le sub non.
    google_sub TEXT    NOT NULL UNIQUE,
    email      TEXT    NOT NULL DEFAULT '',
    name       TEXT    NOT NULL DEFAULT '',
    picture    TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    last_seen  TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Sessions. Seul le SHA-256 du cookie est stocké : une fuite de la base ne
-- donne donc pas directement de quoi se connecter.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_user ON sessions (user_id);

-- État d'une connexion Google en cours. La ligne est consommée au retour
-- du navigateur : le state du formulaire sert donc de jeton à usage unique,
-- ce qui évite d'avoir à signer un cookie avec un secret serveur.
CREATE TABLE IF NOT EXISTS auth_pending (
    state_hash TEXT PRIMARY KEY,
    nonce      TEXT NOT NULL,
    verifier   TEXT NOT NULL,            -- PKCE : le secret du client
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS sets (
    id         INTEGER PRIMARY KEY,
    slug       TEXT    NOT NULL UNIQUE,
    name       TEXT    NOT NULL,
    publisher  TEXT    NOT NULL,
    season     TEXT    NOT NULL,
    -- Nombre de cartes qui composent l'objectif de complétion.
    goal_total INTEGER NOT NULL,
    created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cards (
    id       INTEGER PRIMARY KEY,
    set_id   INTEGER NOT NULL REFERENCES sets(id) ON DELETE CASCADE,
    code     TEXT    NOT NULL,          -- '265', 'GU 1', 'MR-NOR'
    num_sort INTEGER NOT NULL,          -- position d'affichage
    name     TEXT    NOT NULL DEFAULT '',
    team     TEXT    NOT NULL DEFAULT '',
    subset   TEXT    NOT NULL,
    kind     TEXT    NOT NULL,          -- 'numbered' | 'exclusive'
    in_goal  INTEGER NOT NULL,          -- compte dans l'objectif ?
    UNIQUE (set_id, code)
);

CREATE INDEX IF NOT EXISTS cards_order ON cards (set_id, num_sort);

-- Échelle de rareté d'un set : la carte de base plus, pour les cartes qui
-- le permettent, les tirages numérotés du plus courant au plus rare.
CREATE TABLE IF NOT EXISTS rarities (
    id     INTEGER PRIMARY KEY,
    set_id INTEGER NOT NULL REFERENCES sets(id) ON DELETE CASCADE,
    code   TEXT    NOT NULL,            -- 'base', 'green', '1of1'
    label  TEXT    NOT NULL,
    ord    INTEGER NOT NULL,
    capped INTEGER,                     -- tirage numéroté sur N, NULL sinon
    UNIQUE (set_id, code)
);

-- Quelles raretés existent pour quelle carte. 328 cartes sur 353 n'ont que
-- « base » : ne pas leur allouer une ligne par rareté.
CREATE TABLE IF NOT EXISTS card_rarities (
    card_id   INTEGER NOT NULL REFERENCES cards(id)   ON DELETE CASCADE,
    rarity_id INTEGER NOT NULL REFERENCES rarities(id) ON DELETE CASCADE,
    PRIMARY KEY (card_id, rarity_id)
);

-- Ce que je possède réellement, pour un utilisateur donné.
--
-- L'index holdings_card n'est pas déclaré ici : sur une base antérieure à
-- l'authentification, cette table existe déjà sans la colonne user_id et la
-- création de l'index échouerait. C'est la migration qui le pose, une fois
-- la table remise en forme.
CREATE TABLE IF NOT EXISTS holdings (
    user_id   INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    card_id   INTEGER NOT NULL REFERENCES cards(id)   ON DELETE CASCADE,
    rarity_id INTEGER NOT NULL REFERENCES rarities(id) ON DELETE CASCADE,
    qty       INTEGER NOT NULL DEFAULT 0 CHECK (qty >= 0),
    PRIMARY KEY (user_id, card_id, rarity_id)
);