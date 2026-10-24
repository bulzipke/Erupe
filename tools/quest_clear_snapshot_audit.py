"""Read five native clear-count slots from an explicitly declared ZZ full save.

Input must already be the decoded, nullcomp-decompressed full SAVEDATA blob.
This is an offline diagnostic, with no writes, DB, decompression or migration.
Layout evidence: outputs/quest-deployment-20261023/clear-counter-followup-20261024/.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import struct


LAYOUT = 'zz-stock-full-24d24'
FULL_SIZE = 0x24D24
TABLE_OFFSET = 0x249EC
TOKEN_OFFSET = 0x24A00
SLOT_COUNT = 5
VERSION_OFFSET = 0x1F669
NULLCOMP_MARKER = b'cmp 20110113   \x00'


def _file_signature(info):
    return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns,
            getattr(info, 'st_ctime_ns', None))


def read_stable_snapshot(path):
    """Reject observed handle/path drift while taking a bounded read.

    Metadata equality cannot prove an atomic snapshot: same-metadata writes,
    metadata restoration and changes after the last check can escape detection.
    This function neither locks a writer nor authenticates a save or character.
    """
    with path.open('rb') as handle:
        before = _file_signature(os.fstat(handle.fileno()))
        if _file_signature(path.stat()) != before:
            raise ValueError('input file identity or metadata changed before reading')
        # The extra byte preserves the existing exact-size rejection without
        # loading an unbounded input. Both fstat calls use this same handle.
        data = handle.read(FULL_SIZE + 1)
        after = _file_signature(os.fstat(handle.fileno()))
        if after != before or _file_signature(path.stat()) != before:
            raise ValueError('input file identity or metadata changed while reading')
    return data


def inspect_snapshot(data, layout):
    if layout != LAYOUT:
        raise ValueError(f'explicit supported layout required: {LAYOUT}')
    if data.startswith(NULLCOMP_MARKER):
        raise ValueError('input starts with the nullcomp marker; supply an already decompressed full save')
    if len(data) != FULL_SIZE:
        raise ValueError(f'decoded full save must be exactly {FULL_SIZE} bytes; got {len(data)}')
    slots = []
    anomalies = []
    seen = set()
    for index in range(SLOT_COUNT):
        quest_id, count = struct.unpack_from('<HH', data, TABLE_OFFSET + 4 * index)
        slots.append({'slot': index, 'quest_id': quest_id, 'count': count})
        if quest_id == 0 and count != 0:
            anomalies.append({'slot': index, 'kind': 'count_without_quest_ID'})
        if quest_id != 0:
            if quest_id in seen:
                anomalies.append({'slot': index, 'kind': 'duplicate_quest_ID'})
            seen.add(quest_id)
    token, = struct.unpack_from('<I', data, TOKEN_OFFSET)
    return {
        'schema': 'zz-clear-snapshot-audit-v1',
        'declared_layout': layout,
        'input_bytes': len(data),
        'input_sha256': hashlib.sha256(data).hexdigest(),
        'layout_auto_detected': False,
        'full_save_semantically_validated': False,
        'decoded_table_offset': TABLE_OFFSET,
        'decoded_token_offset': TOKEN_OFFSET,
        'decoded_version_offset': VERSION_OFFSET,
        'observed_version_byte': data[VERSION_OFFSET],
        'version_is_format_authentication': False,
        'native_slot_count': SLOT_COUNT,
        'slots': slots,
        'token': token,
        'occupied_slots': sum(slot['quest_id'] != 0 for slot in slots),
        'distinct_nonzero_IDs': len(seen),
        'no_empty_ID_slot': all(slot['quest_id'] != 0 for slot in slots),
        'anomalies': anomalies,
        'absent_ID_proves_zero_past_clears': False,
        'historical_sixth_or_later_counts_recovered': False,
        'counter_or_token_modified': False,
        'limits': [
            'Coordinates apply only to the declared stock ZZ decoded full-save layout, not raw/compressed packets or a diff payload.',
            'Length and declared layout do not identify an operating client or authenticate the save, its character or its history.',
            'Values are observed raw slots. No lifetime/reset policy, allowance, import, eligibility or reward is inferred.',
            'An ID absent from these five slots is not proof of zero historical clears, even when an empty slot is present.',
        ],
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--layout', required=True, choices=[LAYOUT])
    parser.add_argument('--input', required=True, type=Path, help='decoded, decompressed full SAVEDATA file')
    args = parser.parse_args(argv)
    try:
        data = read_stable_snapshot(args.input)
        report = inspect_snapshot(data, args.layout)
    except (OSError, ValueError) as error:
        parser.error(str(error))
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
