#!/usr/bin/env python3
"""Recupere les visuels de cartes depuis une ou plusieurs pages HTML.

L'image d'origine n'est pas servie par le site lui-meme mais par le
bucket Google Cloud Storage de PriceCharting, dont le nom de fichier est
la largeur demandee : 60, 120 et 240 px sont les seules valeurs servies.
On demande donc 240, le maximum disponible.

La page du site est derriere Cloudflare : inaccessible en dehors d'un
navigateur, et meme un navigateur pilote se fait bloquer. La page se
charge au defilement (`loading="lazy"` sur chaque vignette) : une
sauvegarde « page complete » ne capture que les lignes deja rendues, soit
environ 150 sur 353. Il faut donc scroller jusqu'en bas *avant* de
sauvegarder, et passer plusieurs fichiers ici si la collection a ete
partee en plusieurs sauvegardes — les correspondances sont fusionnees.

Aucune donnee de carte n'est lue la : le checklist officiel Topps reste
la seule autorite, ceci n'apporte que les visuels.

Usage :
    python3 tools/fetch_images.py <dossier-cible> <page.html> [page2.html ...]
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


def parse(html_paths: list[pathlib.Path]) -> dict:
    """numero de carte -> identifiant PriceCharting, fusionne sur plusieurs pages.

    Une meme page peut contenir deux fois la meme carte si le site a
    charge le produit parLots : le premier identifiant vu gagne.
    """
    out = {}
    for path in html_paths:
        s = path.read_text(encoding="utf-8", errors="replace")
        found = {num: ident for num, ident in PRODUCT_RE.findall(s)}
        new = [n for n in found if n not in out]
        print(f"  {path.name} : {len(found)} produits, {len(new)} nouveaux")
        for num, ident in found.items():
            out.setdefault(num, ident)
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
    args = sys.argv[1:]
    if len(args) < 2:
        sys.exit(__doc__)
    dest = pathlib.Path(args[0])
    html_paths = [pathlib.Path(a) for a in args[1:]]
    dest.mkdir(parents=True, exist_ok=True)

    print("pages lues :")
    mapping = parse(html_paths)
    print(f"produits distincts : {len(mapping)}")

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
