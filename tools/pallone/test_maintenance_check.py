import importlib.util
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("maintenance_check", Path(__file__).with_name("maintenance_check.py"))
maintenance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(maintenance)


class MaintenanceChecks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bundle, self.client, self.server = [self.root / name for name in ["bundle", "client", "server"]]
        for path in [self.bundle, self.client, self.server]: path.mkdir()
        m = {"kind": "offline_route_001_client_candidate", "native_route_key": 7, "overall_ranking_key": 0,
             "route": {"index": 7, "number": 1, "star": 0, "native_code": 10001, "stage_quest_ids": maintenance.IDS},
             "delivery_rows": [{"star": 0, "bank_ordinal": 7, "group_flag": 0}], "files_sha256": {}}
        names = [f"server/quests/{qid}{suffix}.bin" for qid in maintenance.IDS for suffix in maintenance.SUFFIXES]
        names += ["client/dat/mhfdat.bin", "client/dat/mhfinf.bin"]
        for name in names:
            p = self.bundle / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(name.encode())
            m["files_sha256"][name] = maintenance.sha_file(p)
        for name, field in [("mhfdat.bin", "source_mhfdat_sha256"), ("mhfinf.bin", "source_mhfinf_sha256")]:
            p = self.client / name
            p.write_bytes(b"original " + name.encode())
            m[field] = maintenance.sha_file(p)
        self.m = m
        self.write_manifest()

    def write_manifest(self):
        text = json.dumps(self.m)
        (self.bundle / "manifest.json").write_text(text)
        (self.bundle / "server/pallone-client-profile.json").write_text(text)

    def inspect(self, config=None):
        return maintenance.inspect(self.bundle, self.client, self.server, config)

    def test_read_only_backup_plan_and_secret_redaction(self):
        config = self.root / "config.json"
        config.write_text(json.dumps({"EarthID": 12, "EarthStatus": 2, "Database": {"Password": "do-not-export"},
                                      "TowerRotation": {"Enabled": True}, "PalloneFestival": {"Enabled": False, "EventID": 17},
                                      "DebugOptions": {"QuestTools": False}}))
        before = {str(p): p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        result = self.inspect(config)
        self.assertTrue(result["files_ready_for_manual_install"])
        self.assertEqual(len(result["files"]), 27)
        self.assertFalse(result["runtime_modified"])
        self.assertIsNone(result["event_plan_template"]["starts_at"])
        self.assertNotIn("do-not-export", json.dumps(result))
        self.assertTrue(result["configuration_backup"]["selected_fields"]["TowerRotation.Enabled"])
        self.assertEqual(result["configuration_backup"]["selected_fields"]["PalloneFestival.Enabled"], False)
        self.assertEqual(result["configuration_backup"]["selected_fields"]["PalloneFestival.EventID"], 17)
        patch = result["configuration_patch_template"]
        self.assertEqual(patch["PalloneFestival"], {"Enabled": True, "StartAt": "", "ActiveDays": 21, "ClaimDays": 21})
        self.assertNotIn("EventID", patch["PalloneFestival"])
        self.assertTrue(set(patch).isdisjoint({"EarthID", "EarthStatus", "TowerRotation"}))
        self.assertEqual(before, {str(p): p.read_bytes() for p in self.root.rglob("*") if p.is_file()})

    def test_rejects_stale_client_and_corrupt_candidate(self):
        (self.client / "mhfdat.bin").write_bytes(b"new localization")
        self.assertFalse(self.inspect()["files_ready_for_manual_install"])
        (self.bundle / "server/quests/27438n2.bin").write_bytes(b"changed")
        with self.assertRaises(ValueError): self.inspect()

    def test_already_installed_client_requires_previous_backup(self):
        for name in ["mhfdat.bin", "mhfinf.bin"]:
            (self.client / name).write_bytes((self.bundle / "client/dat" / name).read_bytes())
        result = self.inspect()
        self.assertFalse(result["files_ready_for_manual_install"])
        self.assertEqual(result["files"][0]["state"], "already_candidate_requires_original_backup")

    def test_preserves_existing_server_files_and_json_fallback(self):
        q = self.server / "quests"
        q.mkdir()
        (q / "27438d0.bin").write_bytes(b"old binary")
        (q / "27479d0.json").write_bytes(b"old JSON")
        result = self.inspect()
        by_name = {Path(row["target"]).name: row for row in result["files"]}
        self.assertTrue(by_name["27438d0.bin"]["before_exists"])
        self.assertTrue(by_name["27479d0.bin"]["json_fallback_before"]["exists"])
        self.assertEqual((q / "27438d0.bin").read_bytes(), b"old binary")

    def test_report_cannot_be_written_inside_runtime_or_candidate(self):
        for directory in [self.bundle, self.client, self.server]:
            destination = directory / "report.json"
            args = ["maintenance_check", "--bundle", str(self.bundle), "--client-dat", str(self.client),
                    "--server-bin", str(self.server), "--report", str(destination)]
            with mock.patch("sys.argv", args), contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as error: maintenance.main()
            self.assertEqual(error.exception.code, 2)
            self.assertFalse(destination.exists())


if __name__ == "__main__": unittest.main()
