#!/usr/bin/env python3
"""Recupere les visuels depuis une recherche eBay, dans un navigateur reel.

Necessite Chrome ouvert avec chromedriver sur le port 9518, en session non
headless : eBay refuse les requetes simples (403) et detecte le mode
headless. Un navigateur reel avec le profil du proprietaire passe.

L'identification ne se fait pas par OCR mais en rapprochant le titre de
l'annonce du catalogue officiel : un titre comme « Lewis Hamilton Gold
Manufactured Relic Insert MR-HAM » ou « LANDO NORRIS GOLD LIMITED EDITION
LE 5 » designe une carte precise, et l'identifiant y est le plus souvent
ecrit en clair. Laà oé l'OCR echoue.

Usage :
    python3 tools/ebay_harvest.py <sortie> <requete> [requete...]
    python3 tools/ebay_harvest.py data/thumbnails "2026 turbo attax f1 gold" ...
"""

import concurrent.futures
import json
import pathlib
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

# Format portrait d'une carte Topps : 63 x 88 points.
CARD_RATIO = 63 / 88
TOLERANCE = 0.075
EBAY_REF = "https://www.ebay.com/"
UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"

# Mots de bruit : annonces de conteneurs, boosters, lots, ou une edition
# precedente, qui ne donneraient pas une image de carte identifiable.
BRUIT = re.compile(r"\b(202[0-5]|tin\b|box\b|blister|booster|starter|album|"
                  r"display|lot\b|bundle|pack\b|sealed|pick your|check description)\b", re.I)

# Mots du vocabulaire produit, sans valeur pour identifier un pilote.
STOP = {"topps", "turbo", "attax", "f1", "f2", "formula", "gold", "golden",
        "glory", "limited", "edition", "card", "cards", "tcg", "rare",
        "relic", "insert", "chrome", "legacy", "mclaren", "mercedes",
        "ferrari", "red", "bull", "aston", "martin", "alpine", "williams",
        "haas", "audi", "cadillac", "sauber", "racing", "team", "die",
        "cut", "case", "hit", "foil", "ssp", "2026", "2025"}


def charger_catalogue() -> dict:
    """code -> (nom, equipe, sous-serie) depuis le seed officiel."""
    chemin = pathlib.Path(__file__).parent.parent / "seed" / "turbo-attax-f1-2026.json"
    doc = json.load(open(chemin, encoding="utf-8"))
    return {c["code"]: (c["name"], c["team"], c["subset"]) for c in doc["cards"]}


def index_codes(catalogue: dict) -> list[tuple[str, str]]:
    """(forme compacte sans tiret, code) tries par longueur decroissante.

    « MR-HAM » devient « MRHAM », « LE 5 » devient « LE5 » : en cherchant
    ces formes dans un titre normalise on lit l'identifiant sans OCR.
    """
    out = []
    for code in catalogue:
        compact = code.replace("-", "").upper()
        if compact.isdigit():
            continue
        out.append((compact, code))
    return sorted(out, key=lambda t: -len(t[0]))


def lire_nom(titre: str, catalogue: dict) -> str | None:
    """Nom d'une personne dans un titre eBay, ou None.

    Les titres alternent majuscules (« LANDO NORRIS GOLD LIMITED EDITION »)
    et minuscules (« Lando Norris " Gold turbo attax legacy " McLaren »),
    donc on ne compte pas sur la casse : on compare les mots du titre a
    ceux du nom du pilote.
    """
    mots = [m for m in re.findall(r"[a-zà-ÿ]{3,}", titre.lower()) if m not in STOP]
    if not mots:
        return None

    meilleur, score = None, 0.0
    for code, (nom, _, _) in catalogue.items():
        if not nom:
            continue
        tokens = [m for m in re.findall(r"[a-zà-ÿ]{3,}", nom.lower()) if m not in STOP]
        if not tokens:
            continue
        communs = sum(1 for tk in tokens if tk in mots)
        ratio = communs / len(tokens)
        if ratio > score:
            meilleur, score = nom, ratio
    return meilleur if score >= 1.0 else None


def trouver_numero(titre: str, catalogue: dict, codes: list[tuple[str, str]]) -> str | None:
    """Code de carte depuis un titre, ou None si ambigu.

    Quatre strategies, de la plus fiable a la plus faible :
    1. un code d'exclusif ecrit en clair (« MR-HAM », « LE4 ») ;
    2. un numero precede d'un # ;
    3. le nom du pilote et la sous-serie, trouves tous les deux ;
    4. rien, et on prefere ignorer plutot que se tromper.
    """
    if BRUIT.search(titre):
        return None

    # 1. codes d'exclusifs, formes compactes dans le titre epure.
    t_compact = re.sub(r"[^A-Z0-9]", "", titre.upper())
    for compact, code in codes:
        if compact in t_compact:
            return code

    # 2. numero explicite.
    m = re.search(r"#\s*(\d{1,3})\b", titre)
    if m:
        n = int(m.group(1))
        if 1 <= n <= 353 and str(n) in catalogue:
            return str(n)

    # 3. nom + sous-serie.
    nom = lire_nom(titre, catalogue)
    if not nom:
        return None
    tl = titre.lower()
    candidats = [c for c, (n, _, _) in catalogue.items() if n and nom.lower() in n.lower()]
    if len(candidats) == 1:
        return candidats[0]
    if len(candidats) > 1:
        for sous in sorted({s for _, _, s in catalogue.values()}):
            if sous.lower() in tl:
                exact = [c for c in candidats if catalogue[c][2].lower() == sous.lower()]
                if len(exact) == 1:
                    return exact[0]
    return None


class Navigateur:
    """Session Chrome pilotee par chromedriver, sur le bureau reel."""

    def __init__(self):
        subprocess.run(["pkill", "-x", "chromedriver"], capture_output=True)
        time.sleep(1)
        prof = pathlib.Path("/tmp/chromeprof-ebay")
        subprocess.run(["rm", "-rf", str(prof)], check=False)
        src = pathlib.Path.home() / ".config" / "chromium"
        subprocess.run(["cp", "-r", str(src), str(prof)], check=False)
        subprocess.run(["rm", "-rf", *(str(p) for p in sorted(prof.glob("Singleton*")))], check=False)

        self.proc = subprocess.Popen(
            ["chromedriver", "--port=9518"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            env={"DISPLAY": ":0", "PATH": "/usr/bin:/bin:/usr/local/bin"})
        for _ in range(40):
            time.sleep(0.5)
            try:
                urllib.request.urlopen("http://127.0.0.1:9518/status", timeout=2)
                break
            except OSError:
                continue

        body = json.dumps({"capabilities": {"alwaysMatch": {
            "browserName": "chrome",
            "goog:chromeOptions": {"args": [
                "--no-sandbox", "--disable-gpu", f"--user-data-dir={prof}",
                "--window-position=-2400,-2400", "--window-size=1400,900",
            ]},
        }}}).encode()
        req = urllib.request.Request("http://127.0.0.1:9518/session", data=body,
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=30) as r:
            self.sid = json.load(r)["value"]["sessionId"]

    def aller(self, url: str):
        b = json.dumps({"url": url}).encode()
        rq = urllib.request.Request(f"http://127.0.0.1:9518/session/{self.sid}/url", data=b,
                                   headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(rq, timeout=180) as r:
            json.load(r)
        time.sleep(8)

    def executer(self, js: str):
        b = json.dumps({"script": js, "args": []}).encode()
        rq = urllib.request.Request(f"http://127.0.0.1:9518/session/{self.sid}/execute/sync", data=b,
                                   headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(rq, timeout=300) as r:
            return json.load(r)["value"]

    def annonces(self):
        return self.executer("""
        return Array.from(document.querySelectorAll('.s-card')).map(c => {
            const t = c.querySelector('.s-card__title, .s-card__header__text, h3');
            const a = c.querySelector('a[href*=hash]');
            const m = a ? (a.getAttribute('href')||'').match(/g:([A-Za-z0-9~\\-]+)/) : null;
            return { titre: t ? t.textContent.trim() : '', id: m ? m[1] : '' };
        }).filter(x => x.titre && x.id);
        """)

    def scroller(self, etapes: int = 10):
        """Defile le resultat jusqu'en bas pour charger toutes les annonces."""
        for _ in range(etapes):
            self.executer("window.scrollTo(0, document.body.scrollHeight)")
            time.sleep(1.0)
        self.executer("window.scrollTo(0, 0)")

    def fermer(self):
        try:
            urllib.request.urlopen(f"http://127.0.0.1:9518/session/{self.sid}", timeout=5).close()
        except OSError:
            pass
        self.proc.terminate()


def telecharger(ident: str, timeout: int = 30) -> bytes | None:
    url = f"https://i.ebayimg.com/images/g/{ident}/s-l1600.jpg"
    try:
        req = urllib.request.Request(url, headers={"User-Agent": UA, "Referer": EBAY_REF})
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.read()
    except (urllib.error.URLError, OSError):
        return None


def ratio(data: bytes) -> float | None:
    try:
        i, n = 2, len(data)
        while i < n - 9:
            if data[i] != 0xFF:
                i += 1
                continue
            m = data[i + 1]
            if m in (0xC0, 0xC1, 0xC2):
                h, w = int.from_bytes(data[i + 5:i + 7], "big"), int.from_bytes(data[i + 7:i + 9], "big")
                return w / h if h else None
            if m in (0xD8, 0xD9) or 0xD0 <= m <= 0xD7:
                i += 2
                continue
            i += 2 + int.from_bytes(data[i + 2:i + 4], "big")
    except Exception:
        pass
    return None


def main():
    args = sys.argv[1:]
    if len(args) < 2:
        sys.exit(__doc__)
    sortie = pathlib.Path(args[0])
    requetes = [re.sub(r"\s+", "+", q) for q in args[1:]]
    sortie.mkdir(parents=True, exist_ok=True)

    catalogue = charger_catalogue()
    codes = index_codes(catalogue)
    print(f"catalogue : {len(catalogue)} cartes, {len(codes)} codes d'exclusifs")

    nav = Navigateur()
    toutes: dict[str, str] = {}
    try:
        for q in requetes:
            print(f"\nrecherche : {q}")
            annonces = []
            for tentative in range(3):
                nav.aller(f"https://www.ebay.co.uk/sch/i.html?_nkw={q}")
                nav.scroller()
                annonces = nav.annonces()
                if annonces:
                    break
                print(f"  resultat vide, nouvelle tentative ({tentative + 1}/3)…")
                time.sleep(15)
            print(f"  {len(annonces)} annonces")
            connues = ignores = 0
            for a in annonces:
                n = trouver_numero(a["titre"], catalogue, codes)
                if not n:
                    ignores += 1
                    continue
                connues += 1
                if n not in toutes:
                    toutes[n] = a["id"]
            print(f"  identifiees {connues}, non identifiees {ignores} -> {len(toutes)} numeros au total")
    finally:
        nav.fermer()

    with open(sortie.parent / "identifies.json", "w", encoding="utf-8") as f:
        json.dump(toutes, f, ensure_ascii=False, indent=1, sort_keys=True)

    print(f"\n{len(toutes)} identifiants, telechargement…")
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        jobs = {pool.submit(telecharger, ident): code for code, ident in sorted(toutes.items())}
        ok = ignorees = 0
        for job in concurrent.futures.as_completed(jobs):
            code = jobs[job]
            data = job.result()
            if not data:
                print(f"  {code}: echec reseau")
                continue
            r = ratio(data)
            if not (r and abs(r - CARD_RATIO) < TOLERANCE):
                ignorees += 1
                continue
            dest = sortie / f"{code}.jpg"
            dest.write_bytes(data)
            ok += 1
    print(f"\n{ok} images gardees, {ignorees} hors format portrait ignorees")
    print(f"sortie : {sortie}")


if __name__ == "__main__":
    main()
