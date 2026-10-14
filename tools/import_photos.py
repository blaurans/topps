#!/usr/bin/env python3
"""Importe des photos de cartes dans le dossier des visuels.

On ne regarde pas les images : un fichier s'identifie par son NOM, et le
nom est confronté au catalogue avant toute copie. Une photo dont le nom
ne correspond à aucune carte est signalée, jamais devinée.

    python3 tools/import_photos.py ~/Documents/turbo-attax --stage /tmp/x

Le nommage attendu reprend celui du serveur, où le code de carte a ses
espaces remplacés par des tirets :

    301.jpg      -> 301.jpg        (carte numérotée)
    GU1.jpg      -> GU-1.jpg       (code « GU 1 »)
    SUN11.jpg    -> SUN-11.jpg     (code « SUN 11 »)
    MR-HAM.jpg   -> MR-HAM.jpg     (code « MR-HAM »)

Les photos d'iPhone arrivent en HEIC : elles sont converties au passage.
"""

import argparse
import json
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile

# Noms sans numéro : ce sont les reliques, identifiées par leur sigle.
RELIC = re.compile(r"^(MR|RR)-([A-Z]{3})$")
# Noms « SIGLENUMERE » : les goalies, soleils, legends, leasing…
PREFIXED = re.compile(r"^(GU|SUN|LE|GG|BG)[ _]?(\d+)$")
PLAIN = re.compile(r"^(\d+)$")


def load_codes(seed_path):
    """Ensemble des codes de carte du catalogue."""
    doc = json.loads(pathlib.Path(seed_path).read_text())
    return {c["code"] for c in doc["cards"]}


def thumb_key(code):
    """Le nom de fichier attendu par le serveur pour ce code."""
    return re.sub(r"[ /\\]", "-", code)


def infer_code(stem):
    """Déduit le code de carte d'un nom de fichier.

    Retourne (code, ambigu). ``code`` est None quand le nom ne désigne
    aucune carte précise : deviner serait pire que de refuser.
    """
    name = stem.strip()
    if PLAIN.match(name):
        return name, False

    m = PREFIXED.match(name)
    if m:
        return f"{m.group(1)} {int(m.group(2))}", False

    m = RELIC.match(name)
    if m:
        return name, False

    # Un sigle seul : la famille existe, mais pas la carte.
    if re.match(r"^(MR|RR|GU|SUN|LE|GG|BG)$", name):
        return None, True
    if re.match(r"^(MR|RR)-?$", name):
        return None, True
    return None, False


def is_heic(path):
    with open(path, "rb") as fh:
        return fh.read(12)[4:8] == b"ftyp"


def dimensions(path):
    """Largeur et hauteur, via ImageMagick."""
    try:
        out = subprocess.run(
            ["identify", "-format", "%w %h", str(path)],
            capture_output=True, text=True, timeout=60,
        )
        if out.returncode == 0:
            w, h = out.stdout.split()[:2]
            return int(w), int(h)
    except (subprocess.SubprocessError, ValueError, IndexError, FileNotFoundError):
        pass
    return None


def convert_heic(path, dest):
    """HEIC vers JPEG. ImageMagick d'abord, heif-convert en secours."""
    tmp = dest.with_suffix(".heic.tmp.jpg")
    if subprocess.run(["convert", str(path), str(tmp)], capture_output=True).returncode == 0:
        shutil.move(tmp, dest)
        return True
    tmp.unlink(missing_ok=True)
    try:
        res = subprocess.run(["heif-convert", str(path), str(dest)],
                             capture_output=True, timeout=300)
    except (FileNotFoundError, subprocess.SubprocessError):
        return False
    return res.returncode == 0 and dest.exists()


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("photos", type=pathlib.Path, help="dossier des photos")
    ap.add_argument("--seed", type=pathlib.Path,
                    default=pathlib.Path("seed/turbo-attax-f1-2026.json"))
    ap.add_argument("--stage", type=pathlib.Path,
                    help="dossier de sortie (sinon, rien n'est ecrit)")
    ap.add_argument("--ratio-tolerance", type=float, default=0.12,
                    help="écart toléré autour des deux formats de carte")
    args = ap.parse_args()

    if not args.photos.is_dir():
        sys.exit(f"dossier introuvable : {args.photos}")

    codes = load_codes(args.seed)
    stage = args.stage
    if stage:
        stage.mkdir(parents=True, exist_ok=True)

    # Les deux formats réels d'une carte Turbo Attax, dans les deux sens :
    # certaines séries sont horizontales.
    portrait, paysage = 63 / 88, 88 / 63

    deployes, convertis, ecartes, ambigus, inconnus = [], [], [], [], []

    with tempfile.TemporaryDirectory() as tmp:
        for src in sorted(args.photos.glob("*.jpg")) + sorted(args.photos.glob("*.jpeg")):
            code, ambiguous = infer_code(src.stem)
            if ambiguous:
                ambigus.append(src.name)
                continue
            if code is None:
                inconnus.append(src.name)
                continue
            if code not in codes:
                inconnus.append(src.name)
                continue

            dest_name = thumb_key(code) + ".jpg"
            dest = (stage / dest_name) if stage else pathlib.Path(tmp) / dest_name

            if is_heic(src):
                if not convert_heic(src, dest):
                    ecartes.append((src.name, "conversion HEIC impossible"))
                    continue
                convertis.append(src.name)
            else:
                shutil.copy2(src, dest)

            wh = dimensions(dest)
            if wh:
                w, h = wh
                r = w / h
                if (abs(r - portrait) > args.ratio_tolerance and
                        abs(r - paysage) > args.ratio_tolerance):
                    # Signalé, pas bloqué : certaines photos sont des
                    # scans de plusieurs cartes, et l'utilisateur voit
                    # ce que c'est.
                    ecartes.append((dest_name, f"format inhabituel {w}x{h}"))

            deployes.append(dest_name)

    print(f"{len(deployes)} visuel(s) prêt(s)"
          f"{' dans ' + str(stage) if stage else ' (aucun ecrit)'}")
    if convertis:
        print(f"  {len(convertis)} converti(s) depuis HEIC")
    if ambigus:
        print(f"  {len(ambigus)} nom(s) trop ambigu(s) : {', '.join(ambigus)}")
        print("    -> ces cartes existent en plusieurs exemplaires ; préciser lequel.")
    if inconnus:
        print(f"  {len(inconnus)} nom(s) hors catalogue, ignorés : {', '.join(inconnus)}")
    if ecartes:
        print("  à regarder :")
        for nom, why in ecartes:
            print(f"    {nom} : {why}")

    return 0


if __name__ == "__main__":
    sys.exit(main())