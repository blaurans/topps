package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "topps.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	return s
}

func testCatalogue(t *testing.T, s *Store) *Catalogue {
	t.Helper()
	cat, err := s.Catalogue("turbo-attax-f1-2026", nil)
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	return cat
}

// TestSeedIntegrity verrouille ce qui vient du checklist officiel Topps.
// Si la régénération du seed casse la numérotation, c'est ici que ça hurle.
func TestSeedIntegrity(t *testing.T) {
	cat := testCatalogue(t, testStore(t))

	if got, want := len(cat.Cards), 423; got != want {
		t.Errorf("cartes = %d, want %d", got, want)
	}
	if got, want := cat.Set.GoalTotal, 353; got != want {
		t.Errorf("goal_total = %d, want %d", got, want)
	}

	var numbered, withVariants int
	seen := map[string]bool{}
	for _, c := range cat.Cards {
		if seen[c.Code] {
			t.Errorf("code dupliqué : %q", c.Code)
		}
		seen[c.Code] = true

		if c.InGoal {
			numbered++
			n, err := strconv.Atoi(c.Code)
			if err != nil {
				t.Errorf("carte en objectif au code non numérique : %q", c.Code)
				continue
			}
			if n < 1 || n > 353 {
				t.Errorf("numéro hors bornes : %d", n)
			}
		}
		if len(c.Variants) > 1 {
			withVariants++
		}
		if c.Qty == nil || c.Qty["base"] != 0 {
			t.Errorf("carte %s : quantité initiale non nulle", c.Code)
		}
	}
	if numbered != 353 {
		t.Errorf("cartes en objectif = %d, want 353", numbered)
	}
	if withVariants != 25 {
		t.Errorf("cartes à variantes = %d, want 25 (TURBO CHROME)", withVariants)
	}

	// La numérotation 1..353 doit être continue.
	for i := 1; i <= 353; i++ {
		if !seen[strconv.Itoa(i)] {
			t.Errorf("numéro manquant : %d", i)
		}
	}
}

// TestSeedIdempotent : relancer le seed sur une base peuplée ne doit
// toucher à rien, sinon les quantités partent à zéro au redémarrage.
func TestSeedIdempotent(t *testing.T) {
	s := testStore(t)
	if err := s.SetQty("316", "base", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(); err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	cat := testCatalogue(t, s)
	if len(cat.Cards) != 423 {
		t.Errorf("cartes = %d après re-seed, want 423", len(cat.Cards))
	}
	for _, c := range cat.Cards {
		if c.Code == "316" {
			if c.Qty["base"] != 3 {
				t.Errorf("quantité écrasée par le re-seed : %d", c.Qty["base"])
			}
			return
		}
	}
	t.Error("carte 316 absente après re-seed")
}

func TestSetQty(t *testing.T) {
	s := testStore(t)

	if err := s.SetQty("316", "base", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetQty("316", "gold", 1); err != nil {
		t.Fatal(err)
	}
	cat := testCatalogue(t, s)
	var found bool
	for _, c := range cat.Cards {
		if c.Code != "316" {
			continue
		}
		found = true
		if c.Qty["base"] != 2 || c.Qty["gold"] != 1 {
			t.Errorf("quantités : base=%d gold=%d, want 2 et 1", c.Qty["base"], c.Qty["gold"])
		}
		if c.Qty["red"] != 0 {
			t.Errorf("red = %d, want 0", c.Qty["red"])
		}
	}
	if !found {
		t.Fatal("carte 316 absente")
	}

	// Une valeur négative est ramenée à zéro, pas rejetée : le client peut
	// envoyer n'importe quel delta.
	if err := s.SetQty("316", "base", -7); err != nil {
		t.Fatal(err)
	}
	cat = testCatalogue(t, s)
	for _, c := range cat.Cards {
		if c.Code == "316" && c.Qty["base"] != 0 {
			t.Errorf("base = %d après qty négative, want 0", c.Qty["base"])
		}
	}

	// La mise à jour est absolue, pas incrémentale.
	if err := s.SetQty("316", "base", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetQty("316", "base", 1); err != nil {
		t.Fatal(err)
	}
	cat = testCatalogue(t, s)
	for _, c := range cat.Cards {
		if c.Code == "316" && c.Qty["base"] != 1 {
			t.Errorf("base = %d, want 1 (écriture absolue)", c.Qty["base"])
		}
	}
}

func TestSetQtyRejectsImpossible(t *testing.T) {
	s := testStore(t)

	if err := s.SetQty("9999", "base", 1); err == nil {
		t.Error("carte inexistante acceptée")
	}
	// La carte 1 n'a que la rareté « base » : un Green Refractor y est
	// refusé même si la rareté existe pour d'autres cartes.
	if err := s.SetQty("1", "green", 1); err == nil {
		t.Error("rareté non allouée à la carte acceptée")
	}
	if err := s.SetQty("1", "nimporte", 1); err == nil {
		t.Error("rareté inconnue acceptée")
	}
}

func TestThumbs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"2.jpg", "316.jpg", "MR-NOR.jpg", "GU-1.jpg", "pasuneimage.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th := newThumbs(dir)
	if got := th.index; len(got) != 4 {
		t.Errorf("indexés %d fichiers, want 4 (.jpg uniquement)", len(got))
	}
	for code, want := range map[string]bool{
		"2": true, "316": true, "MR-NOR": true, "GU 1": true, "999": false,
	} {
		if got := th.has(code); got != want {
			t.Errorf("has(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestThumbKeyCannotEscape(t *testing.T) {
	for _, code := range []string{"../secret", "a/b", "..", "x\\y"} {
		if k := thumbKey(code); k != filepath.Base(k) || k == "." || k == ".." {
			t.Errorf("thumbKey(%q) = %q, susceptible de sortir du dossier", code, k)
		}
	}
}

func TestCatalogueUnknownSet(t *testing.T) {
	s := testStore(t)
	if _, err := s.Catalogue("inexistant", nil); err == nil {
		t.Error("set inconnu accepté")
	}
}

func TestBackup(t *testing.T) {
	s := testStore(t)
	if err := s.SetQty("1", "base", 1); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "sauvegarde", "topps.db")
	if err := s.Backup(dest); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if fi, err := os.Stat(dest); err != nil || fi.Size() == 0 {
		t.Fatalf("sauvegarde vide ou absente : %v", err)
	}

	b, err := Open(dest)
	if err != nil {
		t.Fatalf("ouverture de la sauvegarde : %v", err)
	}
	defer b.Close()
	cat := testCatalogue(t, b)
	for _, c := range cat.Cards {
		if c.Code == "1" && c.Qty["base"] != 1 {
			t.Errorf("sauvegarde : quantité de la carte 1 = %d, want 1", c.Qty["base"])
		}
	}
}
