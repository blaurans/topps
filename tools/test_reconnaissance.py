#!/usr/bin/env python3
"""Tests de la reconnaissance des annonces eBay.

Sans ces tests, une erreur de motif range une image sous la mauvaise carte :
c'est le seul vraiment irréversible de tout l'outil. Ils tournent sans
réseau et sans navigateur.

    python3 tools/test_reconnaissance.py
"""

import importlib.util
import pathlib
import sys

spec = importlib.util.spec_from_file_location(
    "ebay_harvest", pathlib.Path(__file__).parent / "ebay_harvest.py")
eh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(eh)

CAT = eh.charger_catalogue()
CODES = eh.index_codes(CAT)

# (titre d'annonce, code attendu ou None)
CAS = [
    # Codes d'exclusifs écrits en clair, avec ou sans séparateur.
    ("2026 Topps Turbo Attax GG 5 Sir Jack Brabham Greats of the Grid", "GG 5"),
    ("2026 TOPPS TURBO ATTAX F1 LEWIS HAMILTON GG5 GREATS OF THE GRID", "GG 5"),
    ("GREATS OF THE GRID GG1 Lewis Hamilton Ferrari 2026 Turbo Attax", "GG 1"),
    ("2026 turbo attax greats of the grid GG 8 Michael Schumacher", "GG 8"),
    ("GG-6 Jackie Stewart Turbo Attax 2026", "GG 6"),
    ("2026 Turbo Attax MR-HAM Lewis Hamilton Gold Relic", "MR-HAM"),
    ("2026 turbo attax RR-LEC Charles Leclerc Raceday Relics", "RR-LEC"),
    ("2026 Turbo Attax Limited Edition LE 13 Fernando Alonso", "LE 13"),
    ("2026 turbo attax limited editions LE9 Sergio Perez Cadillac", "LE 9"),
    ("2026 Turbo Attax LE18 Valtteri Bottas Cadillac Limited Editions", "LE 18"),
    ("2026 turbo attax LE1 Oscar Piastri limited edition", "LE 1"),

    # Repli sur le nom du pilote et la sous-série.
    ("Jody Scheckter Greats of the Grid 2026 topps turbo attax", "GG 7"),
    ("2026 turbo attax Michael Schumacher manufactured relics gold", "MR-MSC"),

    # Numéro explicite.
    ("2026 Topps Turbo Attax #42 card", "42"),

    # Doivent être refusés : conteneurs, lots, éditions précédentes.
    ("2026 Topps Turbo Attax Base 353 Complete Sealed Box", None),
    ("Turbo Attax wholesale lot 150 cards", None),
    ("2025 Topps Turbo Attax F1 Hobby Box 24 packs", None),
    ("2026 Turbo Attax empty album binder display", None),

    # Pièges de frontières : ne doivent surtout pas se lire « LE 1 »
    # ou « MR-HAM » par hasard.
    ("2026 Turbo Attax Base Card LE130 of 353", None),
    ("2026 Topps Turbo Attax MR-HAMILTON refractor card", None),
    ("2026 Turbo Attax SUNSHINE1 parallels", None),
]


def main() -> int:
    echecs = 0
    for titre, attendu in CAS:
        obtenu = eh.trouver_numero(titre, CAT, CODES)
        if obtenu != attendu:
            echecs += 1
            print(f"ECHEC  {titre!r}\n       obtenu {obtenu!r}, attendu {attendu!r}")
    print(f"{len(CAS) - echecs}/{len(CAS)} reconnaissance(s) correcte(s)")
    return 1 if echecs else 0


if __name__ == "__main__":
    sys.exit(main())