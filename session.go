package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
)

const sessionCookie = "tp_s"

// pageLogin est le seul écran accessible sans session : il ne contient
// rien du tout, juste une porte vers Google.
var pageLogin = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="fr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<meta name="robots" content="noindex, nofollow">
<title>Connexion — Topps Turbo Attax</title>
<link rel="stylesheet" href="/static/style.css">
</head>
<body class="login-page">

<main class="login-box">
  <h1>Topps Turbo Attax</h1>
  <p class="login-sub">Suivi de collection · édition 2026</p>

  {{if .Error}}<p class="login-error">{{.Error}}</p>{{end}}

  {{if .Ready}}
    <a class="google-btn" href="/auth/google">
      <svg viewBox="0 0 18 18" aria-hidden="true" width="18" height="18">
        <path fill="#4285F4" d="M17.64 9.2c0-.64-.06-1.25-.16-1.84H9v3.48h4.84a4.14 4.14 0 0 1-1.8 2.72v2.26h2.91c1.7-1.57 2.69-3.88 2.69-6.62z"/>
        <path fill="#34A853" d="M9 18c2.43 0 4.47-.8 5.96-2.18l-2.91-2.26c-.81.54-1.84.86-3.05.86-2.35 0-4.34-1.58-5.05-3.71H.96v2.33A9 9 0 0 0 9 18z"/>
        <path fill="#FBBC05" d="M3.95 10.71a5.4 5.4 0 0 1 0-3.42V4.96H.96a9 9 0 0 0 0 8.08l2.99-2.33z"/>
        <path fill="#EA4335" d="M9 3.58c1.32 0 2.5.45 3.44 1.35l2.58-2.58C13.46.89 11.43 0 9 0A9 9 0 0 0 .96 4.96l2.99 2.33C4.66 5.16 6.65 3.58 9 3.58z"/>
      </svg>
      Continuer avec Google
    </a>
    {{if .Restricted}}
      <p class="login-note">Accès restreint à {{.Allowed}} compte(s) autorisé(s).</p>
    {{end}}
  {{else}}
    <p class="login-error">
      Authentification non configurée : il manque le client ID ou le secret
      Google dans l'environnement du conteneur.
    </p>
  {{end}}
</main>

</body>
</html>`))

type ctxKey int

const userKey ctxKey = 0

// requireSession entoure les routes privées. Sans cookie valide : 401 pour
// l'API (le navigateur ajax suit la redirection), renvoi vers /login
// sinon.
func requireSession(store *Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			unauthenticated(w, r)
			return
		}
		user, err := store.UserBySession(cookie.Value)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("session: %v", err)
			}
			clearSessionCookie(w)
			unauthenticated(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func unauthenticated(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		httpError(w, http.StatusUnauthorized, errors.New("authentification requise"))
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// userFrom récupère la personne posée par requireSession.
func userFrom(r *http.Request) *User {
	u, _ := r.Context().Value(userKey).(*User)
	return u
}

/* ---------- connexion et déconnexion ---------- */

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// Déjà connecté : inutile de repasser par Google.
	if u := a.sessionUser(r); u != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	data := struct {
		Ready      bool
		Error      string
		Restricted bool
		Allowed    int
	}{Ready: a.auth.Configured()}

	switch r.URL.Query().Get("error") {
	case "access_denied":
		data.Error = "Connexion annulée."
	case "not_allowed":
		data.Error = "Ce compte n'est pas autorisé sur cette instance."
	case "state":
		data.Error = "Connexion expirée ou déjà utilisée, réessayez."
	case "exchange":
		data.Error = "Google a refusé la connexion, réessayez."
	case "unconfigured":
		data.Error = "Authentification non configurée sur le serveur."
	}

	if m := a.auth.AllowedEmails; len(m) > 0 {
		data.Restricted = true
		// On annonce un nombre, jamais les adresses : cette page est
		// publique et n'a pas à lister qui possède un accès.
		data.Allowed = len(m)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageLogin.Execute(w, data); err != nil {
		log.Printf("page de connexion: %v", err)
	}
}

func (a *App) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	if !a.auth.Configured() {
		http.Redirect(w, r, "/login?error=unconfigured", http.StatusSeeOther)
		return
	}
	if a.sessionUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	redirectTo, state, nonce, verifier, err := a.auth.Authorize()
	if err != nil {
		log.Printf("connexion: %v", err)
		http.Redirect(w, r, "/login?error=exchange", http.StatusSeeOther)
		return
	}
	a.store.PurgePending()
	if err := a.store.PutPending(state, nonce, verifier); err != nil {
		log.Printf("connexion: %v", err)
		http.Redirect(w, r, "/login?error=exchange", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}

func (a *App) handleCallback(w http.ResponseWriter, r *http.Request) {
	if !a.auth.Configured() {
		http.Redirect(w, r, "/login?error=unconfigured", http.StatusSeeOther)
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Redirect(w, r, "/login?error="+errParam, http.StatusSeeOther)
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Redirect(w, r, "/login?error=state", http.StatusSeeOther)
		return
	}

	// Le state est consommé ici : une URL de callback rejouée est sans effet.
	wantNonce, verifier, err := a.store.ConsumePending(state)
	if err != nil {
		log.Printf("callback: %v", err)
		http.Redirect(w, r, "/login?error=state", http.StatusSeeOther)
		return
	}

	claims, err := a.auth.Exchange(r.Context(), code, verifier, wantNonce)
	if err != nil {
		log.Printf("callback: échange refusé: %v", err)
		http.Redirect(w, r, "/login?error=exchange", http.StatusSeeOther)
		return
	}

	// Filtre éventuel des adresses autorisées.
	if m := a.auth.AllowedEmails; len(m) > 0 && !m[strings.ToLower(claims.Email)] {
		log.Printf("connexion refusée : %s n'est pas dans la liste", claims.Email)
		http.Redirect(w, r, "/login?error=not_allowed", http.StatusSeeOther)
		return
	}

	user, err := a.store.UpsertGoogleUser(claims.Sub, claims.Email, claims.Name, claims.Picture)
	if err != nil {
		log.Printf("callback: %v", err)
		http.Redirect(w, r, "/login?error=exchange", http.StatusSeeOther)
		return
	}
	// La première personne qui arrive récupère les quantités d'avant
	// l'authentification.
	if err := a.store.ClaimLegacy(user.ID); err != nil {
		log.Printf("callback: reprise: %v", err)
	}

	token, err := a.store.CreateSession(user.ID)
	if err != nil {
		log.Printf("callback: session: %v", err)
		http.Redirect(w, r, "/login?error=exchange", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	log.Printf("connexion : %s", user.Email)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		a.store.DeleteSession(cookie.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// sessionUser retourne la personne connectée, ou nil.
func (a *App) sessionUser(r *http.Request) *User {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	u, err := a.store.UserBySession(cookie.Value)
	if err != nil {
		return nil
	}
	return u
}

/* ---------- identité ---------- */

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		httpError(w, http.StatusUnauthorized, errNotAuthenticated)
		return
	}
	writeJSON(w, u)
}

/* ---------- santé ---------- */

// handleHealthz est public : la sonde Docker n'a pas de session.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintln(w, "ok")
}

var errNotAuthenticated = errors.New("authentification requise")
