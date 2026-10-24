"""Read-only route-001 maintenance preflight; never installs or activates anything."""
import argparse
import hashlib
import json
from pathlib import Path


IDS = [27438, 27479, 27348, 27365]
SUFFIXES = ["d0", "d1", "d2", "n0", "n1", "n2"]


def sha_file(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def json_file(path):
    if Path(path).stat().st_size > 16 << 20:
        raise ValueError("JSON input exceeds 16 MiB")
    return json.loads(Path(path).read_text(encoding="utf-8-sig"))


def pick(mapping, name):
    return next((value for key, value in mapping.items() if key.lower() == name.lower()), None)


def inspect(bundle, client_dat, server_bin, config_path=None):
    bundle, client_dat, server_bin = map(lambda p: Path(p).resolve(), (bundle, client_dat, server_bin))
    if not client_dat.is_dir() or not server_bin.is_dir():
        raise ValueError("client dat and server BinPath must be existing directories")
    manifest_path = bundle / "manifest.json"
    m = json_file(manifest_path)
    route = m.get("route", {})
    key = m.get("native_route_key")
    if (m.get("kind") != "offline_route_001_client_candidate" or type(key) is not int or not 0 < key < 200
            or m.get("overall_ranking_key") != 0 or route.get("index") != key or route.get("number") != 1
            or route.get("star") != 0 or route.get("native_code") != 10001 or route.get("stage_quest_ids") != IDS
            or m.get("delivery_rows") != [{"star": 0, "bank_ordinal": key, "group_flag": 0}]):
        raise ValueError("incompatible route-001 client candidate")
    expected = {f"server/quests/{qid:05d}{suffix}.bin" for qid in IDS for suffix in SUFFIXES}
    expected.update(["client/dat/mhfdat.bin", "client/dat/mhfinf.bin"])
    hashes = m.get("files_sha256", {})
    if set(hashes) != expected:
        raise ValueError("candidate must contain exactly 24 stages and two client assets")
    for relative, digest in hashes.items():
        if sha_file(bundle / relative) != digest:
            raise ValueError(f"candidate hash mismatch: {relative}")
    profile = bundle / "server" / "pallone-client-profile.json"
    if profile.read_bytes() != manifest_path.read_bytes():
        raise ValueError("server publication profile differs from candidate manifest")

    problems, files = [], []
    for filename, source_key in [("mhfdat.bin", "source_mhfdat_sha256"), ("mhfinf.bin", "source_mhfinf_sha256")]:
        target = client_dat / filename
        before = sha_file(target) if target.is_file() else None
        after = hashes[f"client/dat/{filename}"]
        if before == m.get(source_key):
            state = "matching_source"
        elif before == after:
            state = "already_candidate_requires_original_backup"
            problems.append(f"{filename}: candidate already installed; locate its original backup")
        else:
            state = "rebuild_required"
            problems.append(f"{filename}: current client differs from this candidate's source")
        files.append({"role": "client", "target": str(target), "candidate": str(bundle / "client" / "dat" / filename),
                      "before_exists": before is not None, "before_sha256": before, "after_sha256": after,
                      "state": state, "rollback": "restore exact original backup"})

    for relative in sorted(p for p in expected if p.startswith("server/")) + ["server/pallone-client-profile.json"]:
        target = server_bin / Path(relative).relative_to("server")
        before = sha_file(target) if target.is_file() else None
        after = sha_file(bundle / relative)
        row = {"role": "server", "target": str(target), "candidate": str(bundle / relative),
               "before_exists": before is not None, "before_sha256": before, "after_sha256": after,
               "rollback": "restore exact original backup" if before else "remove only if current hash still equals after_sha256"}
        if target.suffix == ".bin":
            companion = target.with_suffix(".json")
            row["json_fallback_before"] = {"path": str(companion), "exists": companion.is_file(),
                                           "sha256": sha_file(companion) if companion.is_file() else None}
        files.append(row)

    config = None
    if config_path is not None:
        config_path = Path(config_path).resolve()
        c = json_file(config_path)
        tower, debug = pick(c, "TowerRotation") or {}, pick(c, "DebugOptions") or {}
        config = {"path": str(config_path), "sha256": sha_file(config_path),
                  "restore_exact_original_file": True, "selected_fields": {
                      "ClientMode": pick(c, "ClientMode"), "BinPath": pick(c, "BinPath"),
                      "EarthID": pick(c, "EarthID"), "EarthStatus": pick(c, "EarthStatus"),
                      "TowerRotation.Enabled": pick(tower, "Enabled"), "DebugOptions.QuestTools": pick(debug, "QuestTools")}}
    return {
        "kind": "pallone_maintenance_preflight", "scope": "only the explicitly supplied local paths",
        "schedule": "not_scheduled", "files_ready_for_manual_install": not problems,
        "production_configuration_checked": config is not None, "database_checked": False,
        "event_registered": False, "runtime_modified": False, "problems": problems,
        "candidate_manifest_sha256": sha_file(manifest_path), "files": files, "configuration_backup": config,
        "event_plan_template": {"kind": "pallone_maintenance_event_plan", "event_id": None,
                                "starts_at": None, "ends_at": None, "claim_until": None,
                                "profile_sha256": sha_file(profile)},
        "configuration_patch_template": {"EarthStatus": 11, "EarthID": None,
                                         "TowerRotation": {"Enabled": False}, "DebugOptions": {"QuestTools": True}},
        "partial_config_is_not_a_replacement_config": True,
        "rollback_database_action": "none; retain event/result history and restore original Earth/Tower configuration",
        "completion_receipts_and_rewards": "unarmed; observations never authorize points",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--client-dat", required=True)
    parser.add_argument("--server-bin", required=True)
    parser.add_argument("--config", help="optional read-only actual server configuration; secrets are never exported")
    parser.add_argument("--report", help="new report file outside candidate and runtime directories; default stdout")
    args = parser.parse_args()
    result = inspect(args.bundle, args.client_dat, args.server_bin, args.config)
    text = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.report:
        out = Path(args.report).resolve()
        for root in [args.bundle, args.client_dat, args.server_bin]:
            if out.is_relative_to(Path(root).resolve()):
                parser.error("report must be outside the candidate/client/server directories")
        if args.config and out == Path(args.config).resolve():
            parser.error("report cannot overwrite server configuration")
        with out.open("x", encoding="utf-8") as stream:
            stream.write(text)
    else:
        print(text, end="")
    raise SystemExit(0 if result["files_ready_for_manual_install"] else 1)


if __name__ == "__main__":
    main()
