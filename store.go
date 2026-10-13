package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed seed/turbo-attax-f1-2026.json
var seedJSON []byte

// Store est la base SQLite. Mono-utilisateur : une écriture à la fois,
// verrouillage en WAL et pragmas posés à l'ouverture.
type Store struct{ db *sql.DB }

// Catalogue est la forme servie au client : tout le set en un document,
// 423 cartes tiennent dans ~200 Ko, le navigateur fait le reste.
type Catalogue struct {
	Set      SetInfo   `json:"set"`
	Rarities []Rarity  `json:"rarities"`
	Cards    []Card    `json:"cards"`
}

type SetInfo struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Publisher string `json:"publisher"`
	Season    string `json:"season"`
	GoalTotal int    `json:"goal_total"`
}

type Rarity struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Ord   int    `json:"ord"`
	Capped *int  `json:"capped"`
}

type Card struct {
	ID       int            `json:"id"`
	Code     string         `json:"code"`
	Name     string         `json:"name"`
	Team     string         `json:"team"`
	Subset   string         `json:"subset"`
	Kind     string         `json:"kind"`
	InGoal   bool           `json:"in_goal"`
	Thumb    bool           `json:"thumb"`
	Variants []string       `json:"variants"`
	Qty      map[string]int `json:"qty"`
}

// seedDoc est la forme du fichier seed/*.json, produit par
// tools/parse_checklist.py depuis le checklist officiel Topps.
type seedDoc struct {
	Set      struct {
		Slug      string `json:"slug"`
		Name      string `json:"name"`
		Publisher string `json:"publisher"`
		Season    string `json:"season"`
		GoalTotal int    `json:"goal_total"`
	} `json:"set"`
	Rarities []struct {
		Code   string `json:"code"`
		Label  string `json:"label"`
		Ord    int    `json:"ord"`
		Capped *int   `json:"capped"`
	} `json:"rarities"`
	Cards []struct {
		Code     string   `json:"code"`
		NumSort  int      `json:"num_sort"`
		Name     string   `json:"name"`
		Team     string   `json:"team"`
		Subset   string   `json:"subset"`
		Kind     string   `json:"kind"`
		InGoal   bool     `json:"in_goal"`
		Variants []string `json:"variants"`
	} `json:"cards"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("création du dossier de base: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Un seul utilisateur : une connexion évite les surprises de verrou.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("migration: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Seed insère le catalogue s'il est absent. Idempotent : une base déjà
// peuplée n'est jamais réécrite, les quantités restent intactes.
func (s *Store) Seed() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sets`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	var doc seedDoc
	if err := json.Unmarshal(seedJSON, &doc); err != nil {
		return fmt.Errorf("seed illisible: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO sets (slug, name, publisher, season, goal_total) VALUES (?,?,?,?,?)`,
		doc.Set.Slug, doc.Set.Name, doc.Set.Publisher, doc.Set.Season, doc.Set.GoalTotal)
	if err != nil {
		return err
	}
	setID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	rarityIDs := map[string]int64{}
	for _, r := range doc.Rarities {
		res, err := tx.Exec(
			`INSERT INTO rarities (set_id, code, label, ord, capped) VALUES (?,?,?,?,?)`,
			setID, r.Code, r.Label, r.Ord, r.Capped)
		if err != nil {
			return err
		}
		if rarityIDs[r.Code], err = res.LastInsertId(); err != nil {
			return err
		}
	}

	for _, c := range doc.Cards {
		res, err := tx.Exec(
			`INSERT INTO cards (set_id, code, num_sort, name, team, subset, kind, in_goal)
			 VALUES (?,?,?,?,?,?,?,?)`,
			setID, c.Code, c.NumSort, c.Name, c.Team, c.Subset, c.Kind, boolToInt(c.InGoal))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}

		variants := c.Variants
		if len(variants) == 0 {
			variants = []string{"base"}
		}
		for _, code := range variants {
			rid, ok := rarityIDs[code]
			if !ok {
				return fmt.Errorf("carte %s : rareté inconnue %q", c.Code, code)
			}
			if _, err := tx.Exec(
				`INSERT INTO card_rarities (card_id, rarity_id) VALUES (?,?)`, id, rid); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// Catalogue renvoie le set complet avec les quantités actuelles.
func (s *Store) Catalogue(slug string, hasThumb func(string) bool) (*Catalogue, error) {
	ctx := context.Background()

	var set SetInfo
	err := s.db.QueryRowContext(ctx,
		`SELECT slug, name, publisher, season, goal_total FROM sets WHERE slug = ?`, slug).
		Scan(&set.Slug, &set.Name, &set.Publisher, &set.Season, &set.GoalTotal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("set inconnu: %q", slug)
	}
	if err != nil {
		return nil, err
	}

	rarities := []Rarity{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT code, label, ord, capped FROM rarities WHERE set_id = (SELECT id FROM sets WHERE slug = ?)
		 ORDER BY ord`, slug)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r Rarity
		if err := rows.Scan(&r.Code, &r.Label, &r.Ord, &r.Capped); err != nil {
			rows.Close()
			return nil, err
		}
		rarities = append(rarities, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Une ligne par couple carte/rareté : les cartes sans variante
	// remontent simplement avec NULL.
	rows, err = s.db.QueryContext(ctx,
		`SELECT c.id, c.code, c.name, c.team, c.subset, c.kind, c.in_goal,
		        r.code, COALESCE(h.qty, 0)
		 FROM cards c
		 JOIN card_rarities cr ON cr.card_id = c.id
		 JOIN rarities r        ON r.id = cr.rarity_id
		 LEFT JOIN holdings h   ON h.card_id = c.id AND h.rarity_id = r.id
		 WHERE c.set_id = (SELECT id FROM sets WHERE slug = ?)
		 ORDER BY c.num_sort, r.ord`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cards := []Card{}
	var cur *Card
	for rows.Next() {
		var (
			c      Card
			inGoal int
			rarity string
			qty    int
		)
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.Team, &c.Subset, &c.Kind,
			&inGoal, &rarity, &qty); err != nil {
			return nil, err
		}
		c.InGoal = inGoal == 1

		// La requête groupe par carte (ORDER BY num_sort, ord) : les
		// lignes d'une même carte sont consécutives.
		if cur == nil || cur.ID != c.ID {
			c.Qty = map[string]int{}
			c.Variants = []string{}
			c.Thumb = hasThumb != nil && hasThumb(c.Code)
			cards = append(cards, c)
			cur = &cards[len(cards)-1]
		}
		cur.Variants = append(cur.Variants, rarity)
		cur.Qty[rarity] = qty
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &Catalogue{Set: set, Rarities: rarities, Cards: cards}, nil
}

// SetQty fixe la quantité d'un couple (carte, rareté). La valeur est
// absolue : le client calcule le delta, le serveur ne fait pas
// l'arithmétique, ce qui évite qu'un double-clic ne compte deux fois.
func (s *Store) SetQty(cardCode, rarityCode string, qty int) error {
	if qty < 0 {
		qty = 0
	}
	var ok int
	err := s.db.QueryRow(`
		SELECT count(*) FROM card_rarities cr
		JOIN cards c   ON c.id = cr.card_id
		JOIN rarities r ON r.id = cr.rarity_id
		WHERE c.code = ? AND r.code = ?`, cardCode, rarityCode).Scan(&ok)
	if err != nil {
		return err
	}
	if ok == 0 {
		return fmt.Errorf("la carte %s n'existe pas en rareté %s", cardCode, rarityCode)
	}
	_, err = s.db.Exec(`
		INSERT INTO holdings (card_id, rarity_id, qty)
		SELECT c.id, r.id, ?
		FROM cards c, rarities r
		WHERE c.code = ? AND r.code = ?
		ON CONFLICT (card_id, rarity_id) DO UPDATE SET qty = excluded.qty`,
		qty, cardCode, rarityCode)
	return err
}

// Backup écrit une copie cohérente de la base, utilisable par le cron.
func (s *Store) Backup(dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_, err := s.db.Exec(`VACUUM INTO ?`, dest)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// thumbKey transforme un code de carte en nom de fichier sans extension :
// « GU 1 » et « MR-NOR » ne sont pas des noms de fichiers commodes.
func thumbKey(code string) string {
	r := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", "..", "-")
	return r.Replace(code)
}

func thumbName(code string) string { return thumbKey(code) + ".jpg" }
