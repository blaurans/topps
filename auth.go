package main

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Auth Google, sans dépendance externe.
//
// Le flux est celui de l'OAuth 2.0 « authorization code » avec PKCE :
// Google renvoie un code, on l'échange contre un jeton, et l'on ne fait
// confiance à ce jeton qu'après avoir vérifié sa signature avec les clés
// publiques publiées par Google. On vérifie aussi l'audience, l'issuer et
// le nonce : sans ces quatre contrôles, un jeton fabriqué pour une autre
// application serait accepté.

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleJWKSURL  = "https://www.googleapis.com/oauth2/v3/certs"

	// audience et issuer acceptés. Google présente l'ancien format d'issuer
	// sur les jetons plus anciens : les deux sont valables.
	authScope = "openid email profile"

	jwksTTL      = time.Hour
	jwksRetryGap = time.Minute
)

// AuthConfig tient la configuration Google. Si ClientID est vide,
// l'authentification est considered non configurée : le serveur démarre
// quand même et le dit, plutôt que de refuser de booter.
type AuthConfig struct {
	ClientID     string
	ClientSecret string
	BaseURL      string

	// AllowedEmails, quand il n'est pas vide, restreint l'accès à ces
	// adresses. Vide = tout compte Google peut se connecter.
	AllowedEmails map[string]bool
}

func (c *AuthConfig) RedirectURL() string {
	return strings.TrimRight(c.BaseURL, "/") + "/auth/callback"
}

// Configured indique si l'authentification peut fonctionner.
func (c *AuthConfig) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.BaseURL != ""
}

// ParseAllowedEmails lit une liste d'adresses séparées par des virgules.
// Une liste vide signifie « tout le monde » : c'est le comportement par
// défaut, à verrouiller explicitement quand on veut.
func ParseAllowedEmails(list string) map[string]bool {
	out := map[string]bool{}
	for _, e := range strings.Split(list, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			out[e] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Authorize renvoie l'URL Google vers laquelle envoyer le navigateur,
// ainsi que le state et le verifier PKCE à conserver jusqu'au retour.
func (c *AuthConfig) Authorize() (redirectTo string, state, nonce, verifier string, err error) {
	state, err = randomToken(32)
	if err != nil {
		return "", "", "", "", err
	}
	nonce, err = randomToken(16)
	if err != nil {
		return "", "", "", "", err
	}
	verifier, err = randomToken(32)
	if err != nil {
		return "", "", "", "", err
	}

	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURL()},
		"response_type":         {"code"},
		"scope":                 {authScope},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"access_type":           {"online"},
		"prompt":                {"select_account"},
	}
	return googleAuthURL + "?" + q.Encode(), state, nonce, verifier, nil
}

// Exchange échange le code contre un id_token vérifié.
func (c *AuthConfig) Exchange(ctx context.Context, code, verifier, wantNonce string) (*IDToken, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"redirect_uri":  {c.RedirectURL()},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := sharedClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("échange du code: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		// La réponse d'erreur de Google contient le motif exact (« invalid_grant »…)
		// : sans elle, un échec de configuration serait indéchiffrable.
		var e struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			return nil, fmt.Errorf("Google a refusé le code : %s (%s)", e.Error, e.ErrorDescription)
		}
		return nil, fmt.Errorf("Google a refusé le code : HTTP %d", res.StatusCode)
	}

	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("réponse de Google illisible: %w", err)
	}
	if tok.IDToken == "" {
		return nil, errors.New("Google n'a renvoyé aucun jeton d'identité")
	}

	claims, err := verifyIDToken(ctx, tok.IDToken, c.ClientID, wantNonce)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// sharedClient évite d'ouvrir un nouveau pool TCP par requête et respecte
// un délai maximal : une Google lente ne doit pas immobiliser le serveur.
var sharedClient = &http.Client{Timeout: 10 * time.Second}

/* ---------- le jeton lui-même ---------- */

// IDToken est ce qu'on retient d'un jeton vérifié.
type IDToken struct {
	Sub           string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	// L'audience est une chaîne, sauf quand plusieurs clients sont
	// autorisés : Google envoie alors un tableau.
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

type idTokenClaims struct {
	Iss   string   `json:"iss"`
	Aud   audience `json:"aud"`
	Exp   int64    `json:"exp"`
	Iat   int64    `json:"iat"`
	Sub   string   `json:"sub"`
	Nonce string   `json:"nonce"`
	Azp   string   `json:"azp"`
	Email string   `json:"email"`
	// Google renvoie email_verified en booléen ; certains jetons anciens
	// l'omettent, ce qui est traité comme « non vérifié ».
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// verifyIDToken est le point de sécurité de tout le site. Elle n'accepte
// que RS256, exige une clé publiée par Google et belonging au client, et
// contrôle l'expiration.
func verifyIDToken(ctx context.Context, token, clientID, wantNonce string) (*IDToken, error) {
	first := strings.IndexByte(token, '.')
	last := strings.LastIndexByte(token, '.')
	if first <= 0 || last <= first || last == len(token)-1 {
		return nil, errors.New("jeton mal formé")
	}
	headerB, payloadB, sigB := token[:first], token[first+1:last], token[last+1:]

	var h jwtHeader
	if err := json.Unmarshal(mustBase64URL(headerB), &h); err != nil {
		return nil, fmt.Errorf("en-tête du jeton illisible: %w", err)
	}
	// Refuser tout autre algorithme est ce qui empêche de faire passer un
	// jeton HMAC signé avec la clé publique de Google, qui est publique.
	if h.Alg != "RS256" {
		return nil, fmt.Errorf("algorithme de signature refusé : %q", h.Alg)
	}
	if h.Kid == "" {
		return nil, errors.New("jeton sans identifiant de clé")
	}

	signature, err := base64.RawURLEncoding.DecodeString(sigB)
	if err != nil {
		return nil, errors.New("signature du jeton illisible")
	}

	key, err := googleKey(ctx, h.Kid)
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256([]byte(headerB + "." + payloadB))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return nil, errors.New("signature du jeton invalide")
	}

	var claims idTokenClaims
	if err := json.Unmarshal(mustBase64URL(payloadB), &claims); err != nil {
		return nil, fmt.Errorf("corps du jeton illisible: %w", err)
	}

	if claims.Iss != "accounts.google.com" && claims.Iss != "https://accounts.google.com" {
		return nil, fmt.Errorf("émetteur inattendu : %q", claims.Iss)
	}
	if !contains(claims.Aud, clientID) {
		return nil, errors.New("jeton émis pour une autre application")
	}
	// Quand plusieurs clients sont destinataires, Google exige en plus que
	// l.authorized party soit bien le nôtre.
	if len(claims.Aud) > 1 && claims.Azp != clientID {
		return nil, fmt.Errorf("partie autorisée inattendue : %q", claims.Azp)
	}
	now := time.Now().Unix()
	if claims.Exp <= now {
		return nil, errors.New("jeton expiré")
	}
	if claims.Iat > now+60 {
		return nil, errors.New("jeton émis dans le futur")
	}
	if wantNonce != "" && claims.Nonce != wantNonce {
		return nil, errors.New("jeton renvoyé pour une autre tentative de connexion")
	}
	if claims.Sub == "" {
		return nil, errors.New("jeton sans identifiant de compte")
	}
	if !claims.EmailVerified {
		return nil, errors.New("adresse e-mail non vérifiée par Google")
	}
	if claims.Email == "" {
		return nil, errors.New("jeton sans adresse e-mail")
	}

	return &IDToken{
		Sub:           claims.Sub,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
	}, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// mustBase64URL décode un segment de JWS. Le format n'accepte ni
// remplissage ni caractères standard, et un segment invalide donne un JSON
// vide : l'erreur remonte ensuite au décodage des claims.
func mustBase64URL(s string) []byte {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return []byte("{}")
	}
	return b
}

/* ---------- les clés publiques de Google ---------- */

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

// keyCache garde les clés Google en mémoire. Google les fait tourner sans
// préavis : un `kid` inconnu déclenche un rafraîchissement, mais pas à
// chaque requête, pour ne pas se laisser emballer par un attaquant qui
// enverrait des `kid` au hasard.
type keyCache struct {
	mu         sync.Mutex
	keys       map[string]*rsa.PublicKey
	fetchedAt  time.Time
	lastTried  time.Time
	refreshErr error
}

var googleKeys = &keyCache{keys: map[string]*rsa.PublicKey{}}

func googleKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	googleKeys.mu.Lock()
	defer googleKeys.mu.Unlock()

	if k, ok := googleKeys.keys[kid]; ok {
		return k, nil
	}
	// Clé inconnue : on retente au plus une fois par minute.
	if !googleKeys.lastTried.IsZero() && time.Since(googleKeys.lastTried) < jwksRetryGap {
		if googleKeys.refreshErr != nil {
			return nil, googleKeys.refreshErr
		}
		return nil, fmt.Errorf("clé de signature inconnue : %q", kid)
	}
	if err := googleKeys.refresh(ctx); err != nil {
		return nil, err
	}
	if k, ok := googleKeys.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("clé de signature inconnue : %q", kid)
}

func (c *keyCache) refresh(ctx context.Context) error {
	c.lastTried = time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleJWKSURL, nil)
	if err != nil {
		c.refreshErr = err
		return err
	}
	res, err := sharedClient.Do(req)
	if err != nil {
		c.refreshErr = fmt.Errorf("récupération des clés Google: %w", err)
		return c.refreshErr
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.refreshErr = fmt.Errorf("clés Google : HTTP %d", res.StatusCode)
		return c.refreshErr
	}
	var doc jwks
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil {
		c.refreshErr = fmt.Errorf("clés Google illisibles: %w", err)
		return c.refreshErr
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		// On n'accepte que les clés RSA de signature : une clé d'un autre
		// type dans le document ne doit pas élargir ce qu'on valide.
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		if k.Kid == "" || k.N == "" || k.E == "" {
			continue
		}
		nBytes, err1 := base64.RawURLEncoding.DecodeString(k.N)
		eBytes, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(eBytes) == 0 || len(eBytes) > 8 {
			continue
		}
		e := new(big.Int).SetBytes(eBytes)
		if !e.IsInt64() || e.Int64() < 3 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}
	}
	if len(keys) == 0 {
		c.refreshErr = errors.New("aucune clé de signature exploitable chez Google")
		return c.refreshErr
	}
	c.keys = keys
	c.fetchedAt = time.Now()
	c.refreshErr = nil
	return nil
}
