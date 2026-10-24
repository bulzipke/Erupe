"""Synthetic-only CLI/layout regressions and observable input-drift checks."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
TEMP_ROOT = ROOT / 'tmp/quest-deployment-20261023/festa-supply-followup-20261024'
TOOL = ROOT / 'tools/quest_clear_snapshot_audit.py'
spec = importlib.util.spec_from_file_location('clear_snapshot_audit_under_test', TOOL)
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


def sample(entries=(), token=60):
    data = bytearray(150820)
    for index, (quest_id, count) in enumerate(entries):
        struct.pack_into('<HH', data, 149996 + 4 * index, quest_id, count)
    struct.pack_into('<I', data, 150016, token)
    data[128617] = 0x34
    return bytes(data)


def changed(info, **values):
    fields = {key: getattr(info, key) for key in
              ['st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_ctime_ns']}
    fields.update(values)
    return SimpleNamespace(**fields)


class PathProbe:
    def __init__(self, path, stats):
        self.path = path
        self.stats = iter(stats)

    def open(self, mode):
        return self.path.open(mode)

    def stat(self):
        value = next(self.stats)
        if isinstance(value, Exception):
            raise value
        return value


class ClearSnapshotStableReadTests(unittest.TestCase):
    def setUp(self):
        TEMP_ROOT.mkdir(parents=True, exist_ok=True)
        self.directory = tempfile.TemporaryDirectory(dir=TEMP_ROOT)
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / 'synthetic-decoded-save.bin'
        self.original = sample([(56014, 7)])
        self.path.write_bytes(self.original)

    def test_stable_real_handle_and_path_keep_output_and_bytes(self):
        info = self.path.stat()
        real_fstat = audit.os.fstat
        handles = []

        def observed(fd):
            handles.append(fd)
            return real_fstat(fd)

        with patch.object(audit.os, 'fstat', side_effect=observed):
            data = audit.read_stable_snapshot(self.path)
        self.assertEqual(len(handles), 2)
        self.assertEqual(handles[0], handles[1])
        self.assertEqual(data, self.original)
        self.assertEqual(self.path.read_bytes(), self.original)
        self.assertEqual(audit.inspect_snapshot(data, audit.LAYOUT),
                         audit.inspect_snapshot(self.original, audit.LAYOUT))
        self.assertEqual(self.path.stat().st_mtime_ns, info.st_mtime_ns)

    def test_each_observable_handle_field_change_rejects_entire_read(self):
        info = self.path.stat()
        for field in ['st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_ctime_ns']:
            altered = changed(info, **{field: getattr(info, field) + 1})
            with self.subTest(field=field), patch.object(audit.os, 'fstat', side_effect=[info, altered]):
                with self.assertRaisesRegex(ValueError, 'changed while reading'):
                    audit.read_stable_snapshot(PathProbe(self.path, [info, altered]))

    def test_path_replacement_before_read_is_rejected(self):
        other = Path(self.directory.name) / 'replacement.bin'
        other.write_bytes(self.original)
        original_stat, other_stat = self.path.stat(), other.stat()
        self.assertNotEqual((original_stat.st_dev, original_stat.st_ino),
                            (other_stat.st_dev, other_stat.st_ino))
        with self.assertRaisesRegex(ValueError, 'changed before reading'):
            audit.read_stable_snapshot(PathProbe(self.path, [other_stat]))

    def test_path_replacement_after_read_is_rejected_even_when_handle_stays_stable(self):
        other = Path(self.directory.name) / 'replacement.bin'
        other.write_bytes(self.original)
        info = self.path.stat()
        with self.assertRaisesRegex(ValueError, 'changed while reading'):
            audit.read_stable_snapshot(PathProbe(self.path, [info, other.stat()]))

    def test_path_metadata_drift_and_disappearance_after_read_are_rejected(self):
        info = self.path.stat()
        with self.assertRaisesRegex(ValueError, 'changed while reading'):
            audit.read_stable_snapshot(PathProbe(self.path, [info, changed(info, st_mtime_ns=info.st_mtime_ns + 1)]))
        with self.assertRaises(FileNotFoundError):
            audit.read_stable_snapshot(PathProbe(self.path, [info, FileNotFoundError('synthetic disappearance')]))

    def test_optional_ctime_absence_is_not_format_authentication(self):
        info = self.path.stat()
        limited = SimpleNamespace(st_dev=info.st_dev, st_ino=info.st_ino,
                                  st_size=info.st_size, st_mtime_ns=info.st_mtime_ns)
        with patch.object(audit.os, 'fstat', side_effect=[limited, limited]):
            data = audit.read_stable_snapshot(PathProbe(self.path, [limited, limited]))
        self.assertEqual(data, self.original)
        self.assertFalse(audit.inspect_snapshot(data, audit.LAYOUT)['full_save_semantically_validated'])

    def test_cli_detected_drift_emits_no_partial_JSON(self):
        info = self.path.stat()
        stdout, stderr = io.StringIO(), io.StringIO()
        with patch.object(audit.os, 'fstat', side_effect=[info, changed(info, st_size=info.st_size + 1)]):
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                with self.assertRaises(SystemExit) as error:
                    audit.main(['--layout', audit.LAYOUT, '--input', str(self.path)])
        self.assertEqual(error.exception.code, 2)
        self.assertEqual(stdout.getvalue(), '')
        self.assertIn('changed while reading', stderr.getvalue())
        self.assertEqual(self.path.read_bytes(), self.original)

    def test_cli_stable_success_is_readonly_and_has_existing_raw_contract(self):
        result = subprocess.run([sys.executable, '-B', str(TOOL), '--layout', audit.LAYOUT,
                                 '--input', str(self.path)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), audit.inspect_snapshot(self.original, audit.LAYOUT))
        self.assertEqual(self.path.read_bytes(), self.original)

    def test_original_widths_and_five_slot_history_limits(self):
        result = audit.inspect_snapshot(sample([(53291, 10), (65535, 65535)], 0xffffffff), audit.LAYOUT)
        self.assertEqual(result['slots'][0], {'slot': 0, 'quest_id': 53291, 'count': 10})
        self.assertEqual(result['slots'][1]['count'], 65535)
        self.assertEqual(result['token'], 0xffffffff)
        self.assertFalse(result['version_is_format_authentication'])
        full = audit.inspect_snapshot(sample([(1, 1), (2, 0), (3, 7), (4, 1), (5, 10)]), audit.LAYOUT)
        self.assertTrue(full['no_empty_ID_slot'])
        self.assertEqual(full['distinct_nonzero_IDs'], 5)
        self.assertFalse(full['historical_sixth_or_later_counts_recovered'])
        self.assertFalse(result['absent_ID_proves_zero_past_clears'])

    def test_original_anomalies_preserve_raw_counts(self):
        result = audit.inspect_snapshot(sample([(1, 1), (1, 2), (0, 3)]), audit.LAYOUT)
        self.assertEqual([a['kind'] for a in result['anomalies']], ['duplicate_quest_ID', 'count_without_quest_ID'])
        self.assertEqual(result['slots'][1]['count'], 2)
        self.assertEqual(result['slots'][2]['count'], 3)

    def test_original_shape_layout_and_compression_rejections(self):
        for data in [b'', sample()[:-1], sample() + b'\0', b'cmp 20110113   \0' + sample()[16:]]:
            with self.subTest(size=len(data)), self.assertRaises(ValueError):
                audit.inspect_snapshot(data, audit.LAYOUT)
        for layout in [None, '', 'auto', 'raw-wire', 'zz']:
            with self.subTest(layout=layout), self.assertRaises(ValueError):
                audit.inspect_snapshot(sample(), layout)

    def test_cli_missing_layout_or_oversize_still_has_no_JSON(self):
        self.path.write_bytes(self.original + b'\0')
        for options in [[], ['--layout', audit.LAYOUT]]:
            result = subprocess.run([sys.executable, '-B', str(TOOL), *options,
                                     '--input', str(self.path)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stdout, '')


if __name__ == '__main__':
    unittest.main(verbosity=2)
