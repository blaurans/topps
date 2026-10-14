package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"
)

// sessionTTL est la durée de vie d'une connexion. Renouveler à chaque
// visite ferait durer une session indéfiniment tant que la machine est
// ouverte ; une borne fixe évite qu'un cookie volé reste bon éternellement.
const sessionTTL = 30 * 24 * time.Hour

// pendingTTL : une connexion Google abandonnée ne doit pas encombrer la
// table, et le state doit cesser d'être valide au bout d'un moment.
const pendingTTL = 15 * time.Minute

// User est une personne identifiée par Google.
type User struct {
	ID      int64  `json:"id"`
	Sub     string `json:"-"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// UpsertGoogleUser crée le compte au premier passage, puis rafraîchit le
// nom et la photo à chaque connexion. On ne touche jamais à l'email comme
// clé d'identité : c'est le `sub` qui identifie, un Gmail peut être
// renommé ou recyclé.
func (s *Store) UpsertGoogleUser(sub, email, name, picture string) (*User, error) {
	_, err := s.db.Exec(`
		INSERT INTO users (google_sub, email, name, picture) VALUES (?,?,?,?)
		ON CONFLICT (google_sub) DO UPDATE SET
			email      = excluded.email,
			name       = excluded.name,
			picture    = excluded.picture,
			last_seen  = datetime('now')`, sub, email, name, picture)
	if err != nil {
		return nil, err
	}
	var u User
	err = s.db.QueryRow(
		`SELECT id, google_sub, email, name, picture FROM users WHERE google_sub = ?`, sub).
		Scan(&u.ID, &u.Sub, &u.Email, &u.Name, &u.Picture)
	return &u, err
}

// ClaimLegacy transfère à la personne qui se connecte les quantités
// créées avant l'authentification. Sans appel, le compte technique reste
// en base et personne ne voit rien.
func (s *Store) ClaimLegacy(userID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var legacyID int64
	err = tx.QueryRow(`SELECT id FROM users WHERE google_sub = ?`, legacySub).Scan(&legacyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // déjà réclamé par quelqu'un d'autre
	}
	if err != nil {
		return err
	}

	res, err := tx.Exec(`
		UPDATE holdings SET user_id = ?
		WHERE user_id = ?
		  AND NOT EXISTS (SELECT 1 FROM holdings h2 WHERE h2.user_id = ? AND h2.card_id = holdings.card_id AND h2.rarity_id = holdings.rarity_id)`,
		userID, legacyID, userID)
	if err != nil {
		return err
	}
	moved, _ := res.RowsAffected()

	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, legacyID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if moved > 0 {
		// Les lignes en doublon avec les vôtres ont sauté : la clé primaire
		// ne peut pas en garder deux. On le dit plutôt que de perdre en silence.
		log.Printf("reprise de la collection : %d quantité(s) reprise(s)", moved)
	}
	return nil
}

// PutPending enregistre une tentative de connexion. Le state n'est jamais
// écrit tel quel : seul son empreinte l'est, donc lire la base ne permet
// pas de forger un retour vers /auth/callback.
func (s *Store) PutPending(state, nonce, verifier string) error {
	_, err := s.db.Exec(
		`INSERT INTO auth_pending (state_hash, nonce, verifier, created_at)
		 VALUES (?,?,?, datetime('now'))`, hashToken(state), nonce, verifier)
	return err
}

// ConsumePending récupère puis supprime la tentative : un state ne sert
// qu'une fois, le rejeu d'une URL de callback est donc sans effet.
func (s *Store) ConsumePending(state string) (nonce, verifier string, err error) {
	h := hashToken(state)
	err = s.db.QueryRow(
		`SELECT nonce, verifier FROM auth_pending
		 WHERE state_hash = ? AND created_at > datetime('now', ?)`,
		h, fmt.Sprintf("-%d seconds", int(pendingTTL.Seconds()))).Scan(&nonce, &verifier)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errors.New("connexion expirée ou déjà utilisée")
	}
	if err != nil {
		return "", "", err
	}
	if _, err := s.db.Exec(`DELETE FROM auth_pending WHERE state_hash = ?`, h); err != nil {
		return "", "", err
	}
	return nonce, verifier, nil
}

// PurgePending nettoie les tentatives abandonnées.
func (s *Store) PurgePending() {
	_, _ = s.db.Exec(`DELETE FROM auth_pending WHERE created_at <= datetime('now', ?)`,
		fmt.Sprintf("-%d seconds", int(pendingTTL.Seconds())))
}

// CreateSession ouvre une session et renvoie le cookie à poser. Le jeton
// n'est renvoyé qu'ici : la base n'en conserve que l'empreinte.
func (s *Store) CreateSession(userID int64) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at)
		 VALUES (?,?, datetime('now', ?))`,
		hashToken(token), userID, fmt.Sprintf("+%d seconds", int(sessionTTL.Seconds())))
	if err != nil {
		return "", err
	}
	return token, nil
}

// UserBySession retrouve la personne derrière un cookie, et repère au passage
// les sessions expirées pour les nettoyer.
func (s *Store) UserBySession(token string) (*User, error) {
	if token == "" {
		return nil, sql.ErrNoRows
	}
	var u User
	err := s.db.QueryRow(`
		SELECT u.id, u.google_sub, u.email, u.name, u.picture
		FROM sessions se
		JOIN users u ON u.id = se.user_id
		WHERE se.token_hash = ? AND se.expires_at > datetime('now')`, hashToken(token)).
		Scan(&u.ID, &u.Sub, &u.Email, &u.Name, &u.Picture)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) DeleteSession(token string) {
	if token == "" {
		return
	}
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
}

// UserCount sert au premier démarrage à savoir s'il reste à créer le
// compte de reprise.
func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

/* ---------- cryptographie ---------- */

// randomToken renvoie une chaîne aléatoire URL-safe.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken est l'empreinte sous laquelle un secret est stocké. Une fuite
// de la base ne donne alors plus aucun accès.
func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
