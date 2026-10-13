// Command topps sert le suivi d'une collection de cartes.
//
// Un seul binaire, une base SQLite, aucun service externe. Le catalogue
// est embarqué (seed/*.json) ; les visuels, eux, sont facultatifs et
// lus dans un dossier au démarrage.
//
//	topps -addr :8080 -db ./data/topps.db -thumbs ./data/thumbnails
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

// thumbs est l'index des visuels présents sur le disque, lu une fois au
// démarrage puis figé (aucune écriture ensuite).
type thumbs struct {
	dir   string
	index map[string]bool
}

func newThumbs(dir string) *thumbs {
	t := &thumbs{dir: dir, index: map[string]bool{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("pas de visuels (%v) : grille texte seule", err)
		return t
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".jpg") {
			t.index[strings.TrimSuffix(name, ".jpg")] = true
		}
	}
	log.Printf("visuels indexés : %d", len(t.index))
	return t
}

func (t *thumbs) has(code string) bool { return t.index[thumbKey(code)] }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", envOr("TOPPS_ADDR", ":8080"), "adresse d'écoute")
	dbPath := flag.String("db", envOr("TOPPS_DB", "/data/topps.db"), "chemin de la base SQLite")
	thumbsDir := flag.String("thumbs", envOr("TOPPS_THUMBS", "/data/thumbnails"), "dossier des visuels")
	slug := flag.String("set", envOr("TOPPS_SET", "turbo-attax-f1-2026"), "slug du set")
	backupCmd := flag.String("backup", "", "écrire une copie de la base dans ce fichier et quitter")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.LUTC)

	store, err := Open(*dbPath)
	if err != nil {
		log.Fatalf("base: %v", err)
	}
	defer store.Close()

	if *backupCmd != "" {
		if err := store.Backup(*backupCmd); err != nil {
			log.Fatalf("backup: %v", err)
		}
		log.Printf("backup écrit dans %s", *backupCmd)
		return
	}

	if err := store.Seed(); err != nil {
		log.Fatalf("seed: %v", err)
	}

	th := newThumbs(*thumbsDir)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("assets: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/catalogue", func(w http.ResponseWriter, r *http.Request) {
		cat, err := store.Catalogue(*slug, th.has)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, cat)
	})
	mux.HandleFunc("PUT /api/holding", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Card   string `json:"card"`
			Rarity string `json:"rarity"`
			Qty    int    `json:"qty"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if body.Card == "" || body.Rarity == "" {
			httpError(w, http.StatusBadRequest, errors.New("card et rarity sont obligatoires"))
			return
		}
		if err := store.SetQty(body.Card, body.Rarity, body.Qty); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"card": body.Card, "rarity": body.Rarity, "qty": max(body.Qty, 0)})
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(sub)))
	mux.HandleFunc("GET /thumb/{code}", func(w http.ResponseWriter, r *http.Request) {
		serveThumb(w, r, *thumbsDir)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, sub)
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		close(idle)
	}()

	log.Printf("écoute sur %s (base %s)", *addr, *dbPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serveur: %v", err)
	}
	<-idle
	log.Print("arrêt propre")
}

func serveIndex(w http.ResponseWriter, sub fs.FS) {
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "index manquant", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// serveThumb ne sert que des .jpg présents dans le dossier dédié : le
// nom reçu n'est jamais utilisé tel quel pour construire un chemin.
func serveThumb(w http.ResponseWriter, r *http.Request, dir string) {
	// Le client demande /thumb/2.jpg ; le fichier s'appelle 2.jpg.
	code := strings.TrimSuffix(r.PathValue("code"), ".jpg")
	name := thumbName(code)
	if name == ".jpg" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(dir, name))
}
