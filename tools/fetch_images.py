#!/usr/bin/env python3
"""Recupere les visuels de cartes depuis une page « Enregistrer la page ».

L'image d'origine n'est pas servie par le site lui-meme mais par le
bucket Google Cloud Storage de PriceCharting, dont le nom de fichier est
la largeur demandee : 60, 120 et 240 px sont les seules valeurs servies.
On demande donc 240, le maximum disponible.

La page Cloudflare du site est inaccessible hors navigateur : c'est le
navigateur de l'utilisateur qui a telecharge la page, et ce script lit le
HTML ainsi enregistre. Aucune donnee de carte n'est lue la : le
checklist officiel Topps reste la seule autorite, celui-ci n'apporte que
les visuels.

Usage :
    python3 tools/fetch_images.py <page-enregistree.html> <dossier-cible>
"""

import concurrent.futures
import pathlib
import re
import sys
import urllib.error
import urllib.request

BUCKET = "https://storage.googleapis.com/images.pricecharting.com/{ident}/240.jpg"

# <tr id="product-..."><a href=".../slug-<numero>"><img src="bucket/.../60.jpg">
PRODUCT_RE = re.compile(
    r'href="https://www\.sportscardspro\.com/game/[^"]*?-(\d+)"[^>]*>\s*'
    r'<img[^>]*?src="https://storage\.googleapis\.com/images\.pricecharting\.com/'
    r'([^/"]+)/\d+\.jpg"'
)


def parse(html_path: pathlib.Path) -> dict:
    """numero de carte -> identifiant PriceCharting."""
    s = html_path.read_text(encoding="utf-8", errors="replace")
    out = {}
    for num, ident in PRODUCT_RE.findall(s):
        out[num] = ident
    return dict(sorted(out.items(), key=lambda kv: int(kv[0])))


def fetch(ident: str, dest: pathlib.Path, timeout: int = 20) -> bool:
    req = urllib.request.Request(
        BUCKET.format(ident=ident), headers={"User-Agent": "Mozilla/5.0"}
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            data = r.read()
    except (urllib.error.URLError, OSError):
        return False
    if len(data) < 200 or data[:2] != b"\xff\xd8":  # trop petit, ou pas un JPEG
        return False
    dest.write_bytes(data)
    return True


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    html_path = pathlib.Path(sys.argv[1])
    dest = pathlib.Path(sys.argv[2])
    dest.mkdir(parents=True, exist_ok=True)

    mapping = parse(html_path)
    print(f"produits trouves dans la page : {len(mapping)}")

    ok = failed = 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        jobs = {
            pool.submit(fetch, ident, dest / f"{num}.jpg"): num
            for num, ident in mapping.items()
        }
        for job in concurrent.futures.as_completed(jobs):
            if job.result():
                ok += 1
            else:
                failed += 1

    total = 4096
    ko = 0
    for f in dest.glob("*.jpg"):
        if f.stat().st_size < total:
            f.unlink()
            ko += 1

    print(f"telecharges : {ok - ko}  ({ko} trop petits supprimes, {failed} echecs)")
    print(f"dossier     : {dest}")


if __name__ == "__main__":
    main()
