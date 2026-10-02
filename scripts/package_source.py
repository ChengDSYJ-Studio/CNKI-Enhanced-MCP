"""Create an upload-ready GitHub repository archive from the current source."""
from pathlib import Path
import hashlib
import re
import zipfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = re.search(r'const Version = "([^"]+)"', (ROOT / 'internal/cnki/model.go').read_text()).group(1)
DEST = ROOT.parent / f'CNKI-MCP-{VERSION}-GitHub.zip'
FILES = ['README.md', 'README.en.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'CHANGELOG.md', 'go.mod', 'go.sum', 'dev', '.gitignore']
EXCLUDE = {'__pycache__', '.DS_Store', 'WORKLOG.md'}
paths = [ROOT / name for name in FILES]
for folder in ['internal', 'cmd', 'docs', 'scripts', 'skills']:
    paths.extend(p for p in (ROOT / folder).rglob('*') if p.is_file() and not any(x in EXCLUDE for x in p.parts))
checksums = []
with zipfile.ZipFile(DEST, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
    for path in sorted(paths):
        relative = path.relative_to(ROOT).as_posix()
        archive.write(path, 'cnki-mcp/' + relative)
        checksums.append(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {relative}\n')
    archive.writestr('cnki-mcp/SOURCE_MANIFEST.sha256', ''.join(checksums))
with zipfile.ZipFile(DEST) as archive:
    assert archive.testzip() is None
    for path in paths:
        assert archive.read('cnki-mcp/' + path.relative_to(ROOT).as_posix()) == path.read_bytes()
sha = hashlib.sha256(DEST.read_bytes()).hexdigest()
DEST.with_suffix('.zip.sha256').write_text(f'{sha}  {DEST.name}\n')
print(f'{DEST}\n{len(paths)} source files, {DEST.stat().st_size} bytes\nSHA256 {sha}')
