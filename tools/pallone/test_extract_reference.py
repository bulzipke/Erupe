import struct
import unittest

from extract_reference import extract, item_rows
from bs4 import BeautifulSoup


class HistoricalReferenceTests(unittest.TestCase):
    def fixture(self, fee=250):
        prefix = bytearray(0xa0)
        struct.pack_into("<3I", prefix, 0x8c, 300, 220, 60)
        metadata = {"recovery": {"quests": [{"quest_id": 27438, "fee": 250, "time_frames_30hz": 54000, "zenny_main": 2800, "zenny_sub_a": 2400, "zenny_sub_b": 1000, "prefix_0xa0_hex": prefix.hex()}]}}
        html = f'''<table class=mei><tr><th id=l6B2E>level</th></tr>
          <tr><td>1</td><td>title</td><td>map</td><td>{fee}z 30min</td>
          <td>2800z 2400z 1000z</td><td>300 220 60</td><td>goals</td>
          <td>100% range2</td><td>large07 small07 variable</td>
          <td><a href="../mons/known.htm">known</a><a href="../mons/unknown.htm">unknown</a></td></tr></table>
          <table class=s><tr><th>supplies</th></tr><tr><td><a href="../sozai/sozai.htm?0291">map</a>x1</td></tr></table>
          <table class=t><tr><th colspan=2>guaranteed</th></tr><tr><td colspan=2>skinx1</td></tr>
          <tr><th colspan=2>normal</th></tr><tr><td><a href="../sozai/sozai.htm?0179">skin</a>x2</td><td>25%</td></tr></table>'''
        return metadata, html

    def test_preserves_evidence_and_does_not_guess_table_flags(self):
        metadata, html = self.fixture()
        report = extract(metadata, html, lambda link: 39 if "known.htm" in link and "unknown" not in link else None)
        q = report["quests"][0]
        self.assertTrue(q["core_agrees_with_g10"])
        self.assertEqual((q["large_stat_table"], q["size_range"]), (7, 2))
        self.assertEqual(q["known_large_monster_ids"], [39])
        self.assertEqual(q["unmapped_monster_links"], ["../mons/unknown.htm"])
        guaranteed, normal = q["source_item_sections"]["t"]
        self.assertEqual(guaranteed["items"][0], {"item_id": 377, "quantity": 1, "rate_percent": None})
        self.assertEqual(normal["items"][0]["rate_percent"], 25)
        self.assertNotIn("native_table_id", guaranteed)

    def test_version_difference_is_a_conflict(self):
        metadata, html = self.fixture(fee=400)
        report = extract(metadata, html, lambda _: None)
        self.assertFalse(report["quests"][0]["core_agrees_with_g10"])
        self.assertEqual(report["quests"][0]["conflicts"], ["fee_time"])

    def test_unresolved_item_is_preserved_without_invented_id(self):
        sections = item_rows(BeautifulSoup('<table><tr><th>reward</th></tr><tr><td>unknownx1</td></tr></table>', "html.parser").table)
        self.assertIsNone(sections[0]["items"][0]["item_id"])


if __name__ == "__main__":
    unittest.main()
