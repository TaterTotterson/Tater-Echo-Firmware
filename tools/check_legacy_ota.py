#!/usr/bin/env python3
"""Run the actual pinned v4.2.3 updater against new-format OTA fixtures.

Uses only local Git objects, stdlib Go, loopback HTTP and a temporary directory.
Never connects to or flashes a satellite. Run before publishing this migration.
"""
from __future__ import annotations

import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from tools.package_echo_release import OTA_SCHEMA, ota_platform  # noqa: E402


BASELINE = 'd5b395a6e680a5b765fcd6c3339598f7d3807ff5'
MODULE = 'github.com/TaterTotterson/Tater-Echo-Firmware'
TEST = r'''package taternative
import (
 "archive/tar"; "bytes"; "compress/gzip"; "context"; "crypto/sha256";
 "encoding/hex"; "encoding/json"; "net/http"; "net/http/httptest";
 "os"; "path/filepath"; "sync/atomic"; "testing"
)
func payload(t *testing.T, target, kind string) []byte {
 files := map[string][]byte{"tater-echo": []byte("\x7fELFtest")}
 if target != "biscuit" && target != "radar" { files["tater-show"] = []byte("\x7fELFtest") }
 if kind == "system" { files = map[string][]byte{"rootfs.tar.gz": []byte("test rootfs")} }
 entries := map[string]linuxAppFile{}
 for name, data := range files { digest := sha256.Sum256(data); entries[name] = linuxAppFile{SHA256: hex.EncodeToString(digest[:]), Size:int64(len(data))} }
 manifest := map[string]any{"schema": SCHEMA, "platform": "echo-hardware-v1/"+target,
 "target":target, "base_os":"tater-linux", "version":"v9.0.0", "files":entries}
 if kind == "system" { manifest["kind"]="tater-linux-rootfs"; manifest["rootfs"]=entries["rootfs.tar.gz"] }
 raw, err := json.Marshal(manifest); if err != nil { t.Fatal(err) }; files["manifest.json"] = raw
 var output bytes.Buffer; gz := gzip.NewWriter(&output); archive := tar.NewWriter(gz)
 for name, data := range files { if err := archive.WriteHeader(&tar.Header{Name:name, Typeflag:tar.TypeReg, Mode:0755, Size:int64(len(data))}); err != nil { t.Fatal(err) }; if _,err:=archive.Write(data); err!=nil { t.Fatal(err) } }
 if err:=archive.Close();err!=nil {t.Fatal(err)}; if err:=gz.Close();err!=nil {t.Fatal(err)}; return output.Bytes()
}
func TestLegacyRejectsEveryNewOTAWithoutInstalling(t *testing.T) {
 for _, target := range []string{"biscuit","radar","checkers","cronos","crown","athena","rook"} {
  for _, kind := range []string{"app","bridge","system"} { t.Run(target+"/"+kind,func(t *testing.T) {
   data:=payload(t,target,kind); hash:=sha256.Sum256(data)
   server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Write(data)})); defer server.Close()
   state:=t.TempDir(); var installed, restarted atomic.Bool
   updater:=&OTAInstaller{Target:target, StateDir:state, HTTP:server.Client(), BaseOS:func()string{return "tater-linux"},
    InstallRootFS:func(context.Context,string)error{installed.Store(true);return nil}, Restart:func(){restarted.Store(true)}}
   request:=OTARequest{URL:server.URL,SHA256:hex.EncodeToString(hash[:]),SizeBytes:int64(len(data))}
   var err error; if kind=="system"{err=updater.InstallSystem(context.Background(),request,nil)}else{err=updater.Install(context.Background(),request,nil)}
   if err==nil||installed.Load()||restarted.Load(){t.Fatalf("unsafe legacy acceptance: %v",err)}
   if _,err:=os.Lstat(filepath.Join(state,"app","current")); !os.IsNotExist(err){t.Fatal("legacy application slot changed")}
  }) }
 }
}
'''


def main() -> None:
    if ota_platform('rook') != 'echo-hardware-v1/rook' or OTA_SCHEMA != 2:
        raise SystemExit('Update the legacy test fixtures for the new platform contract first')
    with tempfile.TemporaryDirectory(prefix='tater-v423-ota-') as temporary:
        root = Path(temporary)
        native = root / 'internal/taternative'
        platform = root / 'internal/platform'
        native.mkdir(parents=True)
        platform.mkdir(parents=True)
        (root / 'go.mod').write_text(f'module {MODULE}\n\ngo 1.24.0\n')
        # Only platform detection, the request DTO and progress clamping are stubbed.
        # The download, parsing and install-dispatch code is byte-for-byte old.
        (platform / 'platform.go').write_text('package platform\nconst TaterLinux="tater-linux"\nfunc Base() string { return TaterLinux }\n')
        (native / 'request.go').write_text(
            'package taternative\ntype OTARequest struct { URL string; SHA256 string; SizeBytes int64 }\n'
            'func clamp(v, lo, hi int) int { if v < lo { return lo }; if v > hi { return hi }; return v }\n')
        for name in ('ota.go', 'ota_system.go', 'ota_checkers_rootfs.go'):
            source = subprocess.check_output(['git', '-C', str(ROOT), 'show', f'{BASELINE}:device/internal/taternative/{name}'])
            (native / name).write_bytes(source)
        (native / 'legacy_test.go').write_text(TEST.replace('SCHEMA', str(OTA_SCHEMA)))
        subprocess.run(['go', 'test', '-race', '-v', './internal/taternative'], cwd=root,
                       env={**os.environ, 'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOWORK': 'off'}, check=True)


if __name__ == '__main__':
    main()
