#!/usr/bin/env python3
import json, sys
from pathlib import Path
p=Path(__file__).with_name('inventory.json')
d=json.loads(p.read_text())
errors=[]
for section in ('files','packages'):
    for item in d[section]:
        if not item.get('destination') or 'UNMAPPED' in item['destination']:
            errors.append(f"{section}: unmapped {item.get('path', item.get('importPath'))}")
listed={x['path'] for x in d['files']}
actual=set()
for base in ('cmd','internal','scripts'):
    root=Path(base)
    if root.exists(): actual.update(str(x) for x in root.rglob('*') if x.is_file())
for path in sorted(actual-listed): errors.append(f'files: missing from inventory {path}')
for path in sorted(listed-actual): errors.append(f'files: stale inventory entry {path}')
if errors:
    print('\n'.join(errors)); sys.exit(1)
print(f"inventory ok: {len(listed)} files, {len(d['packages'])} packages, {len(d['exports'])} exports")
