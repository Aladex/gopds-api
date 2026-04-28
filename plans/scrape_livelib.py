#!/usr/bin/env python3
"""Скрейпер подборок livelib.ru через camoufox (обходит DDoS-Guard).

Запуск:
    .venv/bin/python plans/scrape_livelib.py [URL ...]

URL — формата https://www.livelib.ru/selection/<id>-<slug>. Несколько URL
можно передать через пробел. Скрипт обходит пагинацию (/~2, /~3, …) пока
не перестанут появляться новые книги, и сохраняет
plans/livelib/<slugified-h1>.csv с колонками title,author.
"""

from __future__ import annotations

import csv
import re
import sys
import time
from pathlib import Path

from bs4 import BeautifulSoup
from camoufox.sync_api import Camoufox

OUT_DIR = Path(__file__).parent / "livelib"
PAGE_GAP_SEC = 1.5
MAX_PAGES = 60
MIN_NEW_PER_PAGE = 1


def slugify_collection_name(raw: str) -> str:
    raw = raw.strip()
    raw = re.sub(r"[—–-]\s*\d+\s*книг.*$", "", raw, flags=re.IGNORECASE).strip()
    raw = re.sub(r'[\\/:*?"<>|]+', "", raw)
    raw = re.sub(r"\s+", "_", raw)
    return raw[:80] or "collection"


JUNK_RE = re.compile(r"^\s*\d[\d\s]*\s*прочитал", re.IGNORECASE)


def parse_books(html: str) -> tuple[str, list[tuple[str, str]]]:
    soup = BeautifulSoup(html, "lxml")
    h1 = soup.find("h1")
    title = h1.get_text(strip=True) if h1 else "collection"

    # На странице подборки livelib основной список книг лежит в `#booklist`
    # (внутри сидят .brow-карточки с .brow-data — название + автор). Всё, что
    # ниже (похожие подборки, рекомендации) — за пределами booklist и парсер
    # его игнорирует.
    booklist = soup.select_one("#booklist")
    if booklist is None:
        return title, []

    items: list[tuple[str, str]] = []
    seen: set[tuple[str, str]] = set()
    for card in booklist.select(".brow"):
        # The first /book/ link in a card wraps the cover image and has empty
        # text — use the first link whose text is a real title.
        text = ""
        for link in card.select('a[href*="/book/"]'):
            t = (link.get_text() or "").strip()
            if t and not re.match(r"^\d+$", t) and not JUNK_RE.match(t):
                text = t
                break
        if not text:
            continue
        author_link = card.select_one('a[href*="/author/"]')
        author = (author_link.get_text() or "").strip() if author_link else ""
        if not author:
            # livelib leaves the author empty for anthologies, conference
            # proceedings and multi-volume sets — without an author the
            # local matcher can't disambiguate, so drop the row early.
            continue
        key = (text.lower(), author.lower())
        if key in seen:
            continue
        seen.add(key)
        items.append((text, author))
    return title, items


def goto_with_challenge(page, url: str) -> None:
    """goto + wait through any DDoS-Guard JS challenge."""
    page.goto(url, timeout=90_000)
    # DDoS-Guard reloads itself after solving the challenge; wait for the real
    # content marker (the booklist container) up to ~60s.
    page.wait_for_selector('#booklist .brow', timeout=60_000)


def scrape_one(page, base_url: str) -> tuple[str, list[tuple[str, str]]]:
    print(f"--> {base_url}")
    goto_with_challenge(page, base_url)
    title, books = parse_books(page.content())
    print(f"    page 1: +{len(books)} (title='{title}')")

    seen_keys: set[tuple[str, str]] = {(t.lower(), a.lower()) for t, a in books}
    for page_idx in range(2, MAX_PAGES + 1):
        url = f"{base_url.rstrip('/')}/~{page_idx}"
        try:
            goto_with_challenge(page, url)
        except Exception as exc:
            print(f"    page {page_idx}: stop ({exc.__class__.__name__})")
            break
        _, page_books = parse_books(page.content())
        new_books = [b for b in page_books if (b[0].lower(), b[1].lower()) not in seen_keys]
        if len(new_books) < MIN_NEW_PER_PAGE:
            print(f"    page {page_idx}: no new books, stop")
            break
        for b in new_books:
            seen_keys.add((b[0].lower(), b[1].lower()))
            books.append(b)
        print(f"    page {page_idx}: +{len(new_books)} (total {len(books)})")
        time.sleep(PAGE_GAP_SEC)

    return title, books


def write_csv(name: str, books: list[tuple[str, str]]) -> Path:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    path = OUT_DIR / f"{slugify_collection_name(name)}.csv"
    with path.open("w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["title", "author"])
        for title, author in books:
            w.writerow([title, author])
    return path


def main(urls: list[str]) -> None:
    if not urls:
        print("usage: scrape_livelib.py URL [URL ...]", file=sys.stderr)
        sys.exit(2)
    profile_dir = Path.home() / ".cache" / "livelib-scraper-profile"
    profile_dir.mkdir(parents=True, exist_ok=True)
    with Camoufox(
        headless=True,
        persistent_context=True,
        user_data_dir=str(profile_dir),
    ) as ctx:
        # In persistent_context mode the context is yielded directly.
        page = ctx.pages[0] if ctx.pages else ctx.new_page()
        for url in urls:
            try:
                title, books = scrape_one(page, url)
                if not books:
                    print(f"!! empty result for {url}, skipping")
                    continue
                path = write_csv(title, books)
                print(f"== wrote {len(books)} books -> {path}")
            except Exception as exc:
                print(f"!! failed {url}: {exc.__class__.__name__}: {exc}")


if __name__ == "__main__":
    main(sys.argv[1:])
