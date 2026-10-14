#!/usr/bin/env python3
"""État de la couverture visuelle : ce qui a une image, ce qui n'en a pas.

Le serveur indexe les visuels au démarrage, mais le contenu du dossier se
déduit du catalogue sans rien lancer : c'est ce que fait ce script.

    python3 tools/etat_visuels.py /app/topps/data/thumbnails
    python3 tools/etat_visuels.py ~/Documents/turbo-attax   # avant import

Sur le serveur, sans accès au dossier :

    ssh ociub 'ls -1 /app/topps/data/thumbnails' | ...
"""

import argparse
import json
import pathlib
import re
import sys

key = lambda code: re.sub(r"[ /\\]", "-", code)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("dossier", type=pathlib.Path, nargs="?", default=pathlib.Path("/app/topps/data/thumbnails"))
    ap.add_argument("--seed", type=pathlib.Path,
                    default=pathlib.Path("seed/turbo-attax-f1-2026.json"))
    ap.add_argument("--famille", action="append",
                    help="ne détailler que cette famille d'exclusifs (GU, SUN, LE, GG, BG, MR, RR)")
    args = ap.parse_args()

    if not args.dossier.is_dir():
        sys.exit(f"dossier introuvable : {args.dossier}")

    presents = {p.stem for p in args.dossier.glob("*.jpg")}
    cards = json.loads(args.seed.read_text())["cards"]

    numeroes = [c for c in cards if c["code"].isdigit()]
    exclusifs = [c for c in cards if not c["code"].isdigit()]

    couverture = sum(1 for c in numeroes if key(c["code"]) in presents)
    manquants = [c for c in numeroes if key(c["code"]) not in presents]

    print(f"dossier : {args.dossier}  ({len(presents)} fichiers)")
    print(f"numérotées avec visuel : {couverture} / {len(numeroes)}")
    if manquants:
        codes = [c["code"] for c in manquants]
        for i in range(0, len(codes), 24):
            print("  sans visuel :", " ".join(codes[i:i + 24]))
    else:
        print("  objectif visuel atteint")

    familles = {}
    for c in exclusifs:
        if key(c["code"]) in presents:
            continue
        code = c["code"]
        famille = code.split()[0] if " " in code else code.split("-")[0]
        familles.setdefault(famille, []).append(c)

    if not familles:
        print("\ntous les exclusifs ont un visuel")
        return 0

    total = sum(len(v) for v in familles.values())
    if args.famille:
        print(f"\nexclusifs sans visuel : {total} au total, détail des familles demandées")
    else:
        print(f"\nexclusifs sans visuel : {total}")

    for famille in sorted(familles):
        if args.famille and famille not in args.famille:
            continue
        cartes = sorted(familles[famille], key=lambda c: c["code"])
        sous_serie = cartes[0].get("subset", "")
        print(f"\n  {famille} — {len(cartes)} manquante(s)" + (f" · {sous_serie}" if sous_serie else ""))
        for c in cartes:
            sujet = c.get("name") or "—"
            print(f"    {c['code']:<8} {sujet}")
    return 0


if __name__ == "__main__":
    sys.exit(main())