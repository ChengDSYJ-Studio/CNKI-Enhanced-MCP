"""Developer release builder. End users run the contained native executable."""
from pathlib import Path
import hashlib,os,subprocess,zipfile,json,shutil,re,sys
ROOT=Path(__file__).resolve().parents[1]
VERSION=re.search(r'const Version = "([^"]+)"', (ROOT/'internal/cnki/model.go').read_text()).group(1)
local_go=ROOT/'.tools/go/bin/go'
GO=str(local_go) if local_go.is_file() else shutil.which('go')
if GO is None:
 raise SystemExit('Building releases requires Go; install the version specified in go.mod.')
DEST=ROOT.parent/('CNKI-Enhanced-MCP-'+VERSION)
DEST.mkdir(exist_ok=True)
platforms=[('macOS-arm64','darwin','arm64'),('macOS-x64','darwin','amd64'),('Windows-x64','windows','amd64'),('Linux-x64','linux','amd64'),('Linux-arm64','linux','arm64')]
common=['README.md','README.en.md','LICENSE','THIRD_PARTY_NOTICES.md','docs/banner.svg','docs/banner.en.svg','docs/search-flow.svg','docs/search-flow.en.svg','docs/architecture.svg','docs/architecture.en.svg','docs/VALIDATION.md','docs/TOOL_CONTRACT.md','docs/tool-schema.json']
manifest=[]
for label,system,arch in platforms:
 folder=ROOT/'dist'/label;folder.mkdir(parents=True,exist_ok=True)
 binary=folder/('cnki-mcp.exe' if system=='windows' else 'cnki-mcp')
 env=dict(os.environ,CGO_ENABLED='0',GOOS=system,GOARCH=arch)
 subprocess.run([GO,'build','-trimpath','-ldflags=-s -w','-o',str(binary),'./cmd/cnki-mcp'],cwd=ROOT,env=env,check=True)
 archive=DEST/f'CNKI-Enhanced-MCP-{VERSION}-{label}.zip'
 prefix=f'CNKI-Enhanced-MCP-{label}/'
 with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
  z.write(binary,prefix+binary.name)
  for name in common:z.write(ROOT/name,prefix+name)
  if system=='windows':
   z.writestr(prefix+'安装设置.cmd','@echo off\r\ncd /d "%~dp0"\r\ncnki-mcp.exe setup\r\npause\r\n')
  else:
   name='安装设置.command' if system=='darwin' else '安装设置.sh'
   info=zipfile.ZipInfo(prefix+name);info.external_attr=0o100755<<16
   z.writestr(info,'#!/bin/sh\ncd -- "$(dirname -- "$0")" || exit 1\n./cnki-mcp setup\n')
 manifest.append({'name':archive.name,'bytes':archive.stat().st_size,'sha256':hashlib.sha256(archive.read_bytes()).hexdigest()})
 print(label,'built',flush=True)
source=DEST/f'CNKI-Enhanced-MCP-{VERSION}-source.zip'
subprocess.run([sys.executable,str(ROOT/'scripts/package_source.py')],check=True)
shutil.copy2(ROOT.parent/f'CNKI-Enhanced-MCP-{VERSION}-source.zip',source)
manifest.append({'name':source.name,'bytes':source.stat().st_size,'sha256':hashlib.sha256(source.read_bytes()).hexdigest()})
(DEST/'SHA256SUMS.txt').write_text(''.join(f"{item['sha256']}  {item['name']}\n" for item in manifest))
(DEST/'manifest.json').write_text(json.dumps(manifest,indent=2))
print(DEST,flush=True)
