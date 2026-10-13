#!/usr/bin/env python3
"""Génère le catalogue seedé depuis le checklist officiel Topps.

Source primaire : le PDF officiel « F1_TURBO ATTAX 2026 » (export du
CHECKLIST.xlsx de Topps, colonnes Card # / Name / Team / Subset).
Source secondaire, optionnelle : une page HTML « Enregistrer la page »
d'un site de prix, utilisée uniquement pour associer un visuel à un
numéro de carte (seed/images.json). Aucune donnée de carte n'est lue
là-bas : les 403/anti-bot rendent la ressource non fiable, le PDF
officiel reste la seule autorité.

Usage :
    pdftotext -layout checklist.pdf checklist.txt
    python3 tools/parse_checklist.py checklist.txt seed/
    python3 tools/parse_checklist.py checklist.txt seed/ page-enregistree.html
"""

import html
import json
import pathlib
import re
import sys
import unicodedata

# Un code de carte est soit un numéro (1-353), soit un code d'exclusif
# (« GU 1 », « LE 18 », « MR-NOR », « RR-PIA »).
CODE_RE = re.compile(
    r"^\s*(?P<code>"
    r"\d{1,3}"
    r"|(?:GU|SUN|GG|LE|BG)\s?\d{1,2}"
    r"|(?:MR|RR)-[A-Z]{3}"
    r")\s{1,}(?P<rest>.*)$"
)

# Les sous-series sont composees de majuscules ; les noms d'equipes et de
# pilotes non. Sert a trancher les lignes a deux colonnes.
SUBSET_LIKE = re.compile(r"[A-Z0-9®™&/ -]+")

# Normalisation des symboles de marque, qui sont bruyants dans une UI.
def tidy(s: str) -> str:
    s = html.unescape(s)
    s = s.replace("®", "").replace("™", "").replace(" ", " ")
    return re.sub(r"\s+", " ", s).strip()


def parse_rows(text: str):
    """Retourne [(code, name, team, subset)] depuis le texte du PDF."""
    raw = []
    for line in text.splitlines():
        m = CODE_RE.match(line)
        if not m:
            continue
        code = re.sub(r"\s+", " ", m.group("code")).strip()
        # Les colonnes du PDF sont alignees par au moins deux espaces.
        cols = [c for c in re.split(r"\s{2,}", m.group("rest").strip()) if c]
        raw.append((code, cols))

    # 1re passe : les lignes a 3 colonnes donnent les vocabulaires de
    # reference. Une carte possedant un nom, une equipe et une
    # sous-serie est non ambigue et suffit a alimenter les deux.
    subsets = {tidy(cols[2]) for _, cols in raw if len(cols) == 3}
    teams = {tidy(cols[1]) for _, cols in raw if len(cols) == 3}
    # « BLACK GOLD LIMITED EDITION* only available via Topps.com » est le
    # seul sous-ensemble qui contient une phrase; on le reconnait a sa
    # longueur plutot que de le lister en dur.
    subsets |= {"BLACK GOLD LIMITED EDITION* only available via Topps.com"}

    rows = []
    nameless = []
    for code, cols in raw:
        if len(cols) == 3:
            name, team, subset = tidy(cols[0]), tidy(cols[1]), tidy(cols[2])
        elif len(cols) == 2:
            # La derniere colonne est toujours la sous-serie. Reste a
            # trancher si la premiere est une equipe (carte de logo, sans
            # nom de joueur) ou un nom (legendes, cartes sans equipe).
            head, subset = tidy(cols[0]), tidy(cols[1])
            if head in teams:
                name, team = "", head
                nameless.append((code, head, subset))
            else:
                name, team = head, ""
        elif len(cols) == 1:
            name, team, subset = "", "", tidy(cols[0])
        else:
            name = " ".join(tidy(c) for c in cols[:-1])
            team, subset = "", tidy(cols[-1])
        rows.append((code, name, team, tidy(subset)))

    return rows, nameless, teams


def build_catalogue(rows) -> dict:
    numbered = [r for r in rows if r[0].isdigit()]
    coded = [r for r in rows if not r[0].isdigit()]

    cards = []
    for i, (code, name, team, subset) in enumerate(numbered):
        cards.append(
            {
                "code": code,
                "num_sort": int(code),
                "name": name,
                "team": team,
                "subset": subset,
                "kind": "numbered",
                "in_goal": True,
            }
        )

    # Les exclusifs se rangent apres les 353, dans l'ordre du checklist.
    for i, (code, name, team, subset) in enumerate(coded):
        cards.append(
            {
                "code": code,
                "num_sort": 1000 + i,
                "name": name,
                "team": team,
                "subset": subset,
                "kind": "exclusive",
                "in_goal": False,
            }
        )

    return {
        "set": {
            "slug": "turbo-attax-f1-2026",
            "name": "Topps Turbo Attax Formula 1 2026",
            "publisher": "Topps",
            "season": "2026",
            "goal_total": len(numbered),
        },
        "cards": cards,
    }


# Les raretes numerotees n'existent que sur les cartes TURBO CHROME (le
# libelle Topps « Turbo Chrome ... numbered parallels »). Si la formulation
# evolue, seul ce fichier est a editer.
RARITIES = [
    {"code": "base", "label": "Base", "ord": 0, "capped": None},
    {"code": "green", "label": "Green Refractor", "ord": 1, "capped": 99},
    {"code": "gold", "label": "Gold Refractor", "ord": 2, "capped": 50},
    {"code": "orange", "label": "Orange Refractor", "ord": 3, "capped": 25},
    {"code": "red", "label": "Red Refractor", "ord": 4, "capped": 5},
    {"code": "1of1", "label": "Superfractor", "ord": 5, "capped": 1},
]
VARIANT_SUBSETS = {"TURBO CHROME"}


def attach_variants(catalogue: dict) -> int:
    n = 0
    for card in catalogue["cards"]:
        if card["subset"] in VARIANT_SUBSETS:
            card["variants"] = [r["code"] for r in RARITIES]
            n += 1
        else:
            card["variants"] = ["base"]
    return n


IMAGE_HREF_RE = re.compile(
    r'href="https://www\.sportscardspro\.com/game/[^"]*?-(\w+)"[^>]*>\s*'
    r'<img[^>]*?src="[^"]*?/(60_\d{3}__zlV\.jpg)"'
)


def parse_images(html_path: pathlib.Path) -> dict:
    """Associe numero de carte -> nom de fichier image.

    Le slug du lien se termine par le numero de modele ; l'attribut src
    voisin porte le fichier sauvegarde par le navigateur.
    """
    s = html_path.read_text(encoding="utf-8", errors="replace")
    out = {}
    for num, img in IMAGE_HREF_RE.findall(s):
        out[num] = img
    return dict(sorted(out.items(), key=lambda kv: kv[0].zfill(3)))


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    txt = pathlib.Path(sys.argv[1])
    outdir = pathlib.Path(sys.argv[2])
    outdir.mkdir(parents=True, exist_ok=True)

    rows, nameless, teams = parse_rows(txt.read_text(encoding="utf-8", errors="replace"))
    catalogue = build_catalogue(rows)
    n_var = attach_variants(catalogue)

    numbered = [c for c in catalogue["cards"] if c["in_goal"]]
    nums = [c["num_sort"] for c in numbered]
    assert nums == list(range(1, len(numbered) + 1)), "numerotation 1..N incomplete"
    assert len(set(c["code"] for c in catalogue["cards"])) == len(catalogue["cards"])

    catalogue["rarities"] = RARITIES
    (outdir / "turbo-attax-f1-2026.json").write_text(
        json.dumps(catalogue, ensure_ascii=False, indent=1) + "\n", encoding="utf-8"
    )

    print(f"cartes           : {len(catalogue['cards'])}")
    print(f"  dont numérotées: {len(numbered)} (objectif {catalogue['set']['goal_total']})")
    print(f"  dont exclusifs : {len(catalogue['cards']) - len(numbered)}")
    print(f"sous-séries      : {len({c['subset'] for c in catalogue['cards']})}")
    print(f"équipes          : {len(teams)}")
    print(f"cartes à variantes: {n_var}")
    # Les 11 cartes F1 TEAM LOGOS sont les seules sans nom de joueur.
    print(f"cartes sans nom  : {len(nameless)} ({', '.join(c for c, _, _ in nameless)})")
    if len(nameless) != 11:
        print("! inattendu: le PDF a changé de forme, vérifier le découpage")

    if len(sys.argv) > 3:
        imgs = parse_images(pathlib.Path(sys.argv[3]))
        (outdir / "images.json").write_text(
            json.dumps(imgs, ensure_ascii=False, indent=1) + "\n", encoding="utf-8"
        )
        got = {k for k in imgs if k.isdigit()}
        print(f"\nvisuels          : {len(imgs)} ({len(got)} numérotés sur {len(numbered)})")
        if imgs:
            print(f"couverture numérotées : {len(got) / len(numbered) * 100:.0f}%")


if __name__ == "__main__":
    main()
