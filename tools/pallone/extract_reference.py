"""Extract historical reference evidence without changing any game text/assets.

Requires beautifulsoup4. The HTML, recovered G10 metadata and monster-name map
are read-only inputs. Output is review evidence, never a loadable quest.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import struct

from bs4 import BeautifulSoup


def numbers(text):
    return [int(value) for value in re.findall(r"\d+", text)]


def item_rows(table):
    """Keep source sections; do not guess the native reward-table ID/flags."""
    sections = []
    known_items = {}
    for link in table.find_all("a", href=True):
        match = re.search(r"sozai\.htm\?([0-9A-F]{4})", link["href"], re.I)
        if match:
            known_items[link.get_text(strip=True)] = int(match[1], 16)
    current = None
    for row in table.find_all("tr"):
        heading = row.find("th")
        if heading:
            current = {"source_heading": heading.get_text(" ", strip=True), "items": []}
            sections.append(current)
            continue
        cells = row.find_all("td", recursive=False)
        if not cells or current is None:
            continue
        text = cells[0].get_text("", strip=True)
        quantity = re.search(r"[x\u00d7](\d+)", text)
        if not quantity:
            current["items"].append({"unresolved_source_text": text})
            continue
        match = None
        link = cells[0].find("a", href=True)
        if link:
            match = re.search(r"sozai\.htm\?([0-9A-F]{4})", link["href"], re.I)
        item = int(match[1], 16) if match else known_items.get(text[:quantity.start()])
        rate = re.search(r"(\d+)%", cells[1].get_text(strip=True)) if len(cells) > 1 else None
        entry = {"item_id": item, "quantity": int(quantity[1]), "rate_percent": int(rate[1]) if rate else None}
        if item is None:
            entry["unresolved_source_text"] = text
        current["items"].append(entry)
    return sections


def extract(metadata, html, monster_id_for_link):
    source = BeautifulSoup(html, "html.parser")
    wanted = {q["quest_id"]: q for q in metadata["recovery"]["quests"]}
    quests = []
    seen = set()
    for table in source.select("table.mei"):
        anchor = table.find(id=re.compile(r"^l[0-9A-F]+$", re.I))
        if not anchor:
            continue
        qid = int(anchor["id"][1:], 16)
        if qid not in wanted:
            continue
        if qid in seen:
            raise ValueError(f"Duplicate historical reference ID {qid}")
        seen.add(qid)
        rows = table.find_all("tr")
        cells = rows[1].find_all("td", recursive=False)
        if len(cells) != 10:
            raise ValueError(f"Quest {qid}: expected ten source columns")
        q = wanted[qid]
        fee_time = numbers(cells[3].get_text(" ", strip=True))[:2]
        zenny = numbers(cells[4].get_text(" ", strip=True))[:3]
        hrp = numbers(cells[5].get_text(" ", strip=True))[:3]
        original_hrp = list(struct.unpack_from("<3I", bytes.fromhex(q["prefix_0xa0_hex"]), 0x8c))
        original = {"fee_time": [q["fee"], q["time_frames_30hz"] // 1800], "zenny": [q["zenny_main"], q["zenny_sub_a"], q["zenny_sub_b"]], "rank_points": original_hrp}
        actual = {"fee_time": fee_time, "zenny": zenny, "rank_points": hrp}
        conflicts = [key for key in original if original[key] != actual[key]]
        size = numbers(cells[7].get_text(" ", strip=True))
        stats = numbers(cells[8].get_text(" ", strip=True))
        if len(size) != 2 or len(stats) != 2:
            raise ValueError(f"Quest {qid}: unrecognized stat/size fields")
        links = [a["href"] for a in cells[9].find_all("a", href=True)]
        roster = []
        unknown = []
        for link in links:
            monster = monster_id_for_link(link)
            if monster is None:
                unknown.append(link)
            else:
                roster.append(monster)
        detail = {}
        for sibling in table.next_siblings:
            if getattr(sibling, "name", None) != "table":
                continue
            classes = sibling.get("class", [])
            if "mei" in classes:
                break
            for kind in ("s", "sa", "t"):
                if kind in classes:
                    detail.setdefault(kind, []).extend(item_rows(sibling))
        quests.append({
            "quest_id": qid, "anchor": anchor["id"],
            "core_agrees_with_g10": not conflicts, "conflicts": conflicts,
            "original_core": original, "historical_core": actual,
            "large_stat_table": stats[0], "small_stat_table": stats[1],
            "size_percent": size[0], "size_range": size[1],
            "known_large_monster_ids": sorted(set(roster)), "unmapped_monster_links": unknown,
            "source_stat_mode": cells[8].get_text(" ", strip=True),
            "source_monster_links": links, "source_item_sections": detail,
            "limits": ["Stat variation flags and conditional monster appearances are unverified.", "Source item headings are not native reward table IDs or flags."],
        })
    quests.sort(key=lambda q: q["quest_id"])
    return {"kind": "historical_reference_not_loadable_quest", "required_count": len(wanted), "covered_count": len(quests), "agreeing_core_count": sum(q["core_agrees_with_g10"] for q in quests), "missing_ids": sorted(set(wanted) - seen), "quests": quests}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--metadata", required=True, type=Path)
    parser.add_argument("--ferias-root", required=True, type=Path)
    parser.add_argument("--monster-names", required=True, type=Path)
    args = parser.parse_args()
    metadata = json.loads(args.metadata.read_text(encoding="utf-8-sig"))
    if metadata.get("recovery", {}).get("kind") != "source_metadata_not_loadable_quest":
        parser.error("expected pallone-audit G10 metadata recovery")
    page = args.ferias_root / "quest/pa.htm"
    name_map = {re.sub(r"\s+", "", name): int(mid) for mid, name in re.findall(r'^\s*(\d+):\s*"([^"]+)"', args.monster_names.read_text(encoding="utf-8-sig"), re.M)}
    cache = {}
    root = args.ferias_root.resolve()

    def monster_id_for_link(link):
        if link not in cache:
            path = (page.parent / link).resolve()
            if not path.is_relative_to(root) or not path.is_file():
                cache[link] = None
            else:
                node = BeautifulSoup(path.read_text(encoding="utf-8-sig"), "html.parser").find(id="monsname")
                cache[link] = name_map.get(re.sub(r"\s+", "", node.get_text())) if node else None
        return cache[link]

    report = extract(metadata, page.read_text(encoding="utf-8-sig"), monster_id_for_link)
    report["sources"] = [{"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()} for path in (args.metadata, page, args.monster_names)]
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
