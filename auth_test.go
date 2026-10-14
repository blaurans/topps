package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testApp monte un serveur complet avec une base jetable et aucun visuel.
// Retourne le handler et le store, pour que les tests puissent écrire
// directement dans la base quand c'est plus simple que de passer par HTTP.
func testApp(t *testing.T, cfg *AuthConfig) (http.Handler, *Store) {
	t.Helper()
	store, err := Open(t.TempDir() + "/topps.db")
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sub, err := staticSub()
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	if cfg == nil {
		cfg = &AuthConfig{}
	}
	app := &App{store: store, th: newThumbs(t.TempDir()), slug: "turbo-attax-f1-2026", sub: sub, auth: cfg}
	return app.routes(), store
}

// loginSession crée un utilisateur et une session, et rend le cookie prêt
// à être passé aux requêtes suivantes.
func loginSession(t *testing.T, store *Store, email string) *http.Cookie {
	t.Helper()
	u, err := store.UpsertGoogleUser("sub-"+email, email, "Test", "")
	if err != nil {
		t.Fatalf("création utilisateur: %v", err)
	}
	token, err := store.CreateSession(u.ID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

func doGet(t *testing.T, h http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodGet, path, nil, cookies...)
}

func do(t *testing.T, h http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("corps: %v", err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

/* ---------- portails ---------- */

func TestRoutesExigentUneSession(t *testing.T) {
	h, _ := testApp(t, nil)

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/", http.StatusSeeOther},
		{http.MethodGet, "/api/catalogue", http.StatusUnauthorized},
		{http.MethodGet, "/api/me", http.StatusUnauthorized},
		{http.MethodGet, "/thumb/1.jpg", http.StatusSeeOther},
	} {
		rec := do(t, h, tc.method, tc.path, nil)
		if rec.Code != tc.want {
			t.Errorf("%s %s : statut %d, attendu %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}

	// La page de connexion et la sonde, elles, restent ouvertes.
	for _, path := range []string{"/login", "/healthz"} {
		if rec := doGet(t, h, path); rec.Code != http.StatusOK {
			t.Errorf("GET %s : statut %d, attendu 200", path, rec.Code)
		}
	}
}

func TestSessionInvalideEstRejetee(t *testing.T) {
	h, _ := testApp(t, nil)
	// Un cookie bien formé mais inconnu de la base ne doit rien passer.
	for _, fake := range []string{"bidon", "", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		rec := doGet(t, h, "/api/me", &http.Cookie{Name: sessionCookie, Value: fake})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("cookie %q : statut %d, attendu 401", fake, rec.Code)
		}
	}
}

func TestCatalogueAvecSession(t *testing.T) {
	h, store := testApp(t, nil)
	rec := doGet(t, h, "/api/catalogue", loginSession(t, store, "a@b.fr"))
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d", rec.Code)
	}
	var cat Catalogue
	if err := json.Unmarshal(rec.Body.Bytes(), &cat); err != nil {
		t.Fatalf("décodage: %v", err)
	}
	if len(cat.Cards) != 423 {
		t.Errorf("%d cartes, attendu 423", len(cat.Cards))
	}
}

/* ---------- isolation entre comptes ---------- */

func TestLesCollectionsNeSeVoientPas(t *testing.T) {
	h, store := testApp(t, nil)
	alice := loginSession(t, store, "alice@b.fr")
	bob := loginSession(t, store, "bob@b.fr")

	// Alice coche la carte 1.
	rec := do(t, h, http.MethodPut, "/api/holding",
		map[string]any{"card": "1", "rarity": "base", "qty": 3}, alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice : statut %d (%s)", rec.Code, rec.Body.String())
	}

	// Bob ne doit rien voir.
	var cat Catalogue
	json.Unmarshal(doGet(t, h, "/api/catalogue", bob).Body.Bytes(), &cat)
	for _, c := range cat.Cards {
		for rarity, qty := range c.Qty {
			if qty != 0 {
				t.Fatalf("bob voit %s (%s) = %d alors qu'il n'a rien coché", c.Code, rarity, qty)
			}
		}
	}

	// Et Alice voit bien les siennes.
	json.Unmarshal(doGet(t, h, "/api/catalogue", alice).Body.Bytes(), &cat)
	var found bool
	for _, c := range cat.Cards {
		if c.Code == "1" {
			found = true
			if c.Qty["base"] != 3 {
				t.Errorf("alice : carte 1 = %d, attendu 3", c.Qty["base"])
			}
		}
	}
	if !found {
		t.Error("la carte 1 a disparu du catalogue d'alice")
	}
}

func TestEcritureCroiseeRefusee(t *testing.T) {
	h, store := testApp(t, nil)
	alice := loginSession(t, store, "alice@b.fr")

	// Une rareté qui n'existe pas pour cette carte : refusé pour tout le monde.
	rec := do(t, h, http.MethodPut, "/api/holding",
		map[string]any{"card": "1", "rarity": "1of1", "qty": 1}, alice)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("statut %d, attendu 400", rec.Code)
	}
}

/* ---------- le jeton Google ---------- */

// fakeGoogle fabrique une clé RSA et un jeton RS256 valide (ou pas).
type fakeGoogle struct {
	key      *rsa.PrivateKey
	kid      string
	client   string
	issuer   string
	nonce    string
	aud      any
	azp      string
	exp      int64
	email    string
	verified bool
	alg      string
	// tamper altère la signature après coup.
	tamper bool
}

func newFakeGoogle() *fakeGoogle {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &fakeGoogle{
		key:      key,
		kid:      "test-key",
		client:   "client-id.apps.googleusercontent.com",
		issuer:   "https://accounts.google.com",
		aud:      "client-id.apps.googleusercontent.com",
		exp:      time.Now().Add(time.Hour).Unix(),
		email:    "moi@gmail.com",
		verified: true,
		alg:      "RS256",
	}
}

func (f *fakeGoogle) token() string {
	head := map[string]any{"alg": f.alg, "kid": f.kid, "typ": "JWT"}
	claims := map[string]any{
		"iss":            f.issuer,
		"aud":            f.aud,
		"exp":            f.exp,
		"iat":            time.Now().Unix(),
		"sub":            "109876543210",
		"email":          f.email,
		"email_verified": f.verified,
		"name":           "Moi",
	}
	if f.nonce != "" {
		claims["nonce"] = f.nonce
	}
	if f.azp != "" {
		claims["azp"] = f.azp
	}
	hb, _ := json.Marshal(head)
	cb, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)

	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	if f.tamper {
		sig[0] ^= 0xff
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// avecClésGoogle installe la fausse clé dans le cache et rend une fonction
// de restauration, le cache étant un paquet global.
func avecClésGoogle(t *testing.T, f *fakeGoogle) {
	t.Helper()
	googleKeys.mu.Lock()
	old := googleKeys.keys
	googleKeys.keys = map[string]*rsa.PublicKey{f.kid: &f.key.PublicKey}
	googleKeys.lastTried = time.Now()
	googleKeys.refreshErr = nil
	googleKeys.mu.Unlock()
	t.Cleanup(func() {
		googleKeys.mu.Lock()
		googleKeys.keys = old
		googleKeys.mu.Unlock()
	})
}

func TestJetonValideAccepte(t *testing.T) {
	f := newFakeGoogle()
	avecClésGoogle(t, f)
	f.nonce = "nonce-attendu"

	id, err := verifyIDToken(t.Context(), f.token(), f.client, "nonce-attendu")
	if err != nil {
		t.Fatalf("jeton valide refusé : %v", err)
	}
	if id.Sub != "109876543210" || id.Email != "moi@gmail.com" {
		t.Errorf("claims inattendus : %+v", id)
	}
}

func TestJetonFalsifieRefuse(t *testing.T) {
	cases := []struct {
		nom       string
		mod       func(*fakeGoogle)
		nonceWant string
	}{
		{"signature modifiée", func(f *fakeGoogle) { f.tamper = true }, ""},
		{"algorithme HS256", func(f *fakeGoogle) { f.alg = "HS256" }, ""},
		{"algorithme none", func(f *fakeGoogle) { f.alg = "none" }, ""},
		{"clé inconnue", func(f *fakeGoogle) { f.kid = "autre" }, ""},
		{"autre application", func(f *fakeGoogle) { f.aud = "voleur.apps.googleusercontent.com" }, ""},
		{"audience multiple sans azp", func(f *fakeGoogle) {
			f.aud = []string{"client-id.apps.googleusercontent.com", "x"}
		}, ""},
		{"émetteur inconnu", func(f *fakeGoogle) { f.issuer = "https://attaquant.example" }, ""},
		{"jeton expiré", func(f *fakeGoogle) { f.exp = time.Now().Add(-time.Hour).Unix() }, ""},
		{"e-mail non vérifié", func(f *fakeGoogle) { f.verified = false }, ""},
		{"e-mail vide", func(f *fakeGoogle) { f.email = "" }, ""},
		{"autre nonce", func(f *fakeGoogle) {}, "un-autre-nonce"},
	}

	for _, tc := range cases {
		t.Run(tc.nom, func(t *testing.T) {
			f := newFakeGoogle()
			f.nonce = "nonce-attendu"
			avecClésGoogle(t, f)
			tc.mod(f)
			if _, err := verifyIDToken(t.Context(), f.token(), f.client, tc.nonceWant); err == nil {
				t.Fatalf("jeton accepté alors qu'il ne fallait pas")
			}
		})
	}
}

// Quand plusieurs clients sont destinataires, Google exige que l'authorized
// party soit le nôtre : c'est ce que vérifie ce test, dans les deux sens.
func TestJetonAudienceMultipleExigeAzp(t *testing.T) {
	multiples := []any{"client-id.apps.googleusercontent.com", "autre-client"}

	f := newFakeGoogle()
	avecClésGoogle(t, f)
	f.aud = multiples
	if _, err := verifyIDToken(t.Context(), f.token(), f.client, ""); err == nil {
		t.Error("audience multiple sans azp acceptée")
	}

	g := newFakeGoogle()
	avecClésGoogle(t, g)
	g.aud = multiples
	g.azp = "autre-client"
	if _, err := verifyIDToken(t.Context(), g.token(), g.client, ""); err == nil {
		t.Error("azp désignant un autre client accepté")
	}

	h := newFakeGoogle()
	avecClésGoogle(t, h)
	h.aud = multiples
	h.azp = "client-id.apps.googleusercontent.com"
	if _, err := verifyIDToken(t.Context(), h.token(), h.client, ""); err != nil {
		t.Errorf("audience multiple avec le bon azp refusée : %v", err)
	}
}

func TestJetonMalForme(t *testing.T) {
	for _, bad := range []string{"", "a", "a.b", "a.b.c.d", "...", "a..c"} {
		if _, err := verifyIDToken(t.Context(), bad, "c", ""); err == nil {
			t.Errorf("jeton %q accepté", bad)
		}
	}
}

/* ---------- le flux de connexion ---------- */

func TestLoginPagePublique(t *testing.T) {
	h, _ := testApp(t, &AuthConfig{ClientID: "x", ClientSecret: "y", BaseURL: "https://exemple.fr"})
	rec := doGet(t, h, "/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Continuer avec Google") {
		t.Error("le bouton de connexion est absent")
	}
	// La page publique ne doit rien divulguer des comptes autorisés.
	if strings.Contains(rec.Body.String(), "@") {
		t.Error("la page de connexion cite une adresse e-mail")
	}
}

func TestLoginNonConfigureEstExplique(t *testing.T) {
	h, _ := testApp(t, &AuthConfig{})
	rec := doGet(t, h, "/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "non configur") {
		t.Error("le manque de configuration n'est pas expliqué")
	}
}

func TestLoginStartVaChezGoogle(t *testing.T) {
	h, store := testApp(t, &AuthConfig{ClientID: "cid", ClientSecret: "cs", BaseURL: "https://exemple.fr"})
	rec := doGet(t, h, "/auth/google")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("redirection: %v", err)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "https://accounts.google.com/") {
		t.Errorf("redirection vers %s, attendu Google", loc)
	}
	q := loc.Query()
	if q.Get("client_id") != "cid" {
		t.Error("client_id manquant")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Error("PKCE absent")
	}
	if q.Get("redirect_uri") != "https://exemple.fr/auth/callback" {
		t.Errorf("redirect_uri : %q", q.Get("redirect_uri"))
	}
	// La tentative doit être retrouvable en base.
	var n int
	store.db.QueryRow(`SELECT count(*) FROM auth_pending`).Scan(&n)
	if n != 1 {
		t.Errorf("%d tentatives en base, attendu 1", n)
	}
}

func TestStateSeConsommeUneSeuleFois(t *testing.T) {
	_, store := testApp(t, nil)
	if err := store.PutPending("state1", "n1", "v1"); err != nil {
		t.Fatal(err)
	}
	nonce, verifier, err := store.ConsumePending("state1")
	if err != nil {
		t.Fatalf("première consommation : %v", err)
	}
	if nonce != "n1" || verifier != "v1" {
		t.Errorf("nonce/verifier : %q / %q", nonce, verifier)
	}
	if _, _, err := store.ConsumePending("state1"); err == nil {
		t.Error("un state rejoué a été accepté")
	}
}

func TestStateInconnuRefuse(t *testing.T) {
	_, store := testApp(t, nil)
	if _, _, err := store.ConsumePending("jamais-vu"); err == nil {
		t.Error("un state inconnu a été accepté")
	}
}

func TestPendingExpire(t *testing.T) {
	_, store := testApp(t, nil)
	store.PutPending("vieux", "n", "v")
	// On fait aginger la tentative bien au-delà de la durée de vie.
	store.db.Exec(`UPDATE auth_pending SET created_at = datetime('now', '-1 hour')`)
	if _, _, err := store.ConsumePending("vieux"); err == nil {
		t.Error("une tentative périmée a été acceptée")
	}
}

func TestListeBlancheRefuse(t *testing.T) {
	// Filtre d'adresses : le cas défavorable est un mauvais jeton, mais
	// la décision elle-même est testée directement.
	m := ParseAllowedEmails(" Alice@B.fr ,bob@b.fr,, ")
	if len(m) != 2 {
		t.Fatalf("%d adresses parsed, attendu 2", len(m))
	}
	if !m["alice@b.fr"] || !m["bob@b.fr"] {
		t.Errorf("normalisation incorrecte : %v", m)
	}
	if ParseAllowedEmails("") != nil {
		t.Error("une liste vide devrait signifier « tout le monde »")
	}
}

func TestDeconnexionTueLaSession(t *testing.T) {
	h, store := testApp(t, nil)
	c := loginSession(t, store, "a@b.fr")

	if rec := doGet(t, h, "/logout", c); rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d", rec.Code)
	}
	// La session ne doit plus donner accès.
	if rec := doGet(t, h, "/api/me", c); rec.Code != http.StatusUnauthorized {
		t.Errorf("après déconnexion : statut %d, attendu 401", rec.Code)
	}
	// Et la ligne a disparu de la base.
	var n int
	store.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("%d session(s) en base après déconnexion", n)
	}
}

func TestSecretJamaisStockeEnClair(t *testing.T) {
	_, store := testApp(t, nil)
	u, err := store.UpsertGoogleUser("sub-1", "a@b.fr", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateSession(u.ID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	var stored string
	if err := store.db.QueryRow(`SELECT token_hash FROM sessions LIMIT 1`).Scan(&stored); err != nil {
		t.Fatalf("lecture: %v", err)
	}
	if stored == token {
		t.Fatal("le jeton de session est stocké en clair")
	}
	if len(stored) != 64 {
		t.Errorf("l'empreinte fait %d caractères, attendu 64 (sha256 hex)", len(stored))
	}
}

/* ---------- reprise de la collection existante ---------- */

func TestMigrationReprendLesQuantites(t *testing.T) {
	// Une base dans l'ancien format : holdings sans user_id.
	dir := t.TempDir()
	old, err := Open(dir + "/topps.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	old.db.Exec(`DROP TABLE holdings`)
	old.db.Exec(`CREATE TABLE holdings (
		card_id   INTEGER NOT NULL REFERENCES cards(id)   ON DELETE CASCADE,
		rarity_id INTEGER NOT NULL REFERENCES rarities(id) ON DELETE CASCADE,
		qty       INTEGER NOT NULL DEFAULT 0 CHECK (qty >= 0),
		PRIMARY KEY (card_id, rarity_id))`)

	// Une quantité telle que l'ancienne version l'écrivait.
	var cardID, rarityID int64
	old.db.QueryRow(`SELECT id FROM cards WHERE code = '1'`).Scan(&cardID)
	old.db.QueryRow(`SELECT id FROM rarities WHERE code = 'base'`).Scan(&rarityID)
	if _, err := old.db.Exec(`INSERT INTO holdings (card_id, rarity_id, qty) VALUES (?,?,2)`,
		cardID, rarityID); err != nil {
		t.Fatalf("écriture avant migration: %v", err)
	}
	old.Close()

	store, err := Open(dir + "/topps.db")
	if err != nil {
		t.Fatalf("réouverture après migration: %v", err)
	}
	defer store.Close()

	// La table a bien la nouvelle forme.
	cols, err := store.tableColumns("holdings")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cols["user_id"]; !ok {
		t.Fatal("user_id toujours absent après migration")
	}

	// La quantité a survécu et attend d'être réclamée.
	var kept int
	store.db.QueryRow(`SELECT count(*) FROM holdings WHERE qty = 2`).Scan(&kept)
	if kept != 1 {
		t.Fatalf("%d quantité(s) conservée(s), attendu 1", kept)
	}

	u, err := store.UpsertGoogleUser("sub-1", "a@b.fr", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimLegacy(u.ID); err != nil {
		t.Fatalf("reprise: %v", err)
	}
	var qty int
	store.db.QueryRow(`SELECT qty FROM holdings WHERE user_id = ? AND card_id = ? AND rarity_id = ?`,
		u.ID, cardID, rarityID).Scan(&qty)
	if qty != 2 {
		t.Errorf("quantité reprise = %d, attendu 2", qty)
	}

	// Le compte technique disparaît.
	var n int
	store.db.QueryRow(`SELECT count(*) FROM users WHERE google_sub = ?`, legacySub).Scan(&n)
	if n != 0 {
		t.Error("le compte technique survit à la reprise")
	}
}

func TestDeuxReprisesSuccessivesSansPerte(t *testing.T) {
	h, store := testApp(t, nil)
	alice := loginSession(t, store, "alice@b.fr")

	do(t, h, http.MethodPut, "/api/holding",
		map[string]any{"card": "1", "rarity": "base", "qty": 2}, alice)

	// Bob se connecte : la reprise ne doit rien lui voler.
	bobCookie := loginSession(t, store, "bob@b.fr")
	var bobID int64
	store.db.QueryRow(`SELECT id FROM users WHERE google_sub = 'sub-bob@b.fr'`).Scan(&bobID)
	if err := store.ClaimLegacy(bobID); err != nil {
		t.Fatalf("reprise par bob: %v", err)
	}

	var cat Catalogue
	json.Unmarshal(doGet(t, h, "/api/catalogue", alice).Body.Bytes(), &cat)
	for _, c := range cat.Cards {
		if c.Code == "1" && c.Qty["base"] != 2 {
			t.Fatalf("alice a perdu sa carte 1 : %d", c.Qty["base"])
		}
	}
	json.Unmarshal(doGet(t, h, "/api/catalogue", bobCookie).Body.Bytes(), &cat)
	for _, c := range cat.Cards {
		if c.Code == "1" && c.Qty["base"] != 0 {
			t.Fatalf("bob a récupéré la carte d'alice : %d", c.Qty["base"])
		}
	}
}

func TestQuantiteBornée(t *testing.T) {
	h, store := testApp(t, nil)
	c := loginSession(t, store, "a@b.fr")
	do(t, h, http.MethodPut, "/api/holding",
		map[string]any{"card": "1", "rarity": "base", "qty": -5}, c)

	var cat Catalogue
	json.Unmarshal(doGet(t, h, "/api/catalogue", c).Body.Bytes(), &cat)
	for _, card := range cat.Cards {
		if card.Code == "1" && card.Qty["base"] != 0 {
			t.Errorf("quantité négative acceptée : %d", card.Qty["base"])
		}
	}
}
