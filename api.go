package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// writeJSON écrit une réponse JSON sans échappement HTML : les noms de
// cartes contiennent « & » (les duos de pilotes) et doivent rester lisibles.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "")
	if err := enc.Encode(v); err != nil {
		// L'entête est déjà partie, il ne reste qu'à couper la connexion.
		panic(http.ErrAbortHandler)
	}
}

const maxBody = 1 << 16

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("corps de requête invalide: %w", err)
	}
	if dec.More() {
		return errors.New("corps de requête: une seule valeur JSON attendue")
	}
	return nil
}

// httpError écrit le statut avant le corps : l'ordre inverse fait ignorer
// le code par net/http.
func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
