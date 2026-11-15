"""Execute the real boot script against a fully synthetic filesystem/commands."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from tools import athena_codec_bundle as codec


class AthenaCodecBootTests(unittest.TestCase):
    def trial(self, *, state=None, fail=None, corrupt=False, wrong_kernel=False):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            def put(name, data, mode=0o644):
                path = root/name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data.encode() if isinstance(data, str) else data)
                path.chmod(mode)
                return path
            script = (codec.ROOT/'linux/athena/tater-athena-codecs').read_text()
            for name, expected in codec.load_lock()['modules'].items():
                data = name.encode()
                put('usr/lib/tater-athena-codecs/'+name, data)
                script = script.replace(expected, hashlib.sha256(data).hexdigest())
            if corrupt: put('usr/lib/tater-athena-codecs/mtk_scp.ko', 'changed')
            put('proc/device-tree/compatible', b'amzn,athena\0mediatek,mt8183\0')
            put('data/tater-athena-codec/runtime.json', '{}')
            put('data/tater-athena-codec/omx-lib/runtime.json', '{}')
            (root/'run').mkdir()
            if state is not None: put('sys/class/remoteproc/remoteproc0/state', state)
            # Only device-node type checks are relaxed in this host simulation.
            # No real module, device node, camera or hardware API is touched.
            script = script.replace('test -c /dev/', 'test -e /dev/')
            for prefix in ('/proc/', '/sys/', '/data/', '/run/', '/dev/', '/usr/lib/'):
                script = script.replace(prefix, str(root)+prefix)
            put('boot.sh', script, 0o755)
            put('bin/uname', '#!/bin/sh\nif [ "$1" = -r ]; then echo '+
                ('wrong-kernel' if wrong_kernel else '6.18.56-techo5-athena')+
                '; else echo "#1 SMP PREEMPT 2026-11-14 10:00:00 UTC"; fi\n', 0o755)
            put('bin/sha256sum', '#!/bin/sh\nexec shasum -a 256 "$@"\n', 0o755)
            put('bin/insmod', '''#!/bin/sh
module=${1##*/}; module=${module%.ko}
echo "load $module $2 $3" >> "$FAKE_ROOT/calls"
[ "$module" != "$FAIL_MODULE" ] || exit 1
mkdir -p "$FAKE_ROOT/sys/module/$module"
if [ "$module" = mtk_scp ]; then
    mkdir -p "$FAKE_ROOT/sys/module/mtk_scp/parameters" "$FAKE_ROOT/sys/class/remoteproc/remoteproc0"
    echo Y > "$FAKE_ROOT/sys/module/mtk_scp/parameters/athena_trial"
    echo offline > "$FAKE_ROOT/sys/class/remoteproc/remoteproc0/state"
fi
''', 0o755)
            put('bin/rmmod', '#!/bin/sh\necho "unload $1" >> "$FAKE_ROOT/calls"\n', 0o755)
            put('bin/mdev', '''#!/bin/sh
mkdir -p "$FAKE_ROOT/dev"
touch "$FAKE_ROOT/dev/tater-athena-ap-venc" "$FAKE_ROOT/dev/tater-athena-ap-vdec"
''', 0o755)
            environment = dict(os.environ, PATH=str(root/'bin')+':'+os.environ['PATH'],
                               FAKE_ROOT=str(root), FAIL_MODULE=fail or '')
            result = subprocess.run(['sh',str(root/'boot.sh')],env=environment,capture_output=True,text=True)
            calls = (root/'calls').read_text().splitlines() if (root/'calls').exists() else []
            ready = (root/'run/tater-athena-ap.ready').exists()
            return result.returncode, calls, ready

    def test_normal_boot_quiesces_scp_before_both_codecs(self):
        code,calls,ready=self.trial()
        self.assertEqual(code,0)
        self.assertTrue(ready)
        self.assertEqual([line.split()[1] for line in calls],
                         ['mtk_rpmsg','mtk_scp_ipi','mtk_scp','athena_ap','athena_ap_vdec'])
        self.assertIn('athena_trial=1',calls[2])

    def test_existing_offline_scp_does_not_get_reloaded(self):
        code,calls,ready=self.trial(state='offline')
        self.assertEqual(code,0)
        self.assertTrue(ready)
        self.assertEqual([line.split()[1] for line in calls],['athena_ap','athena_ap_vdec'])

    def test_running_scp_or_bad_identity_refuses_before_hardware_load(self):
        for options in ({'state':'running'},{'corrupt':True},{'wrong_kernel':True}):
            with self.subTest(options=options):
                code,calls,ready=self.trial(**options)
                self.assertNotEqual(code,0)
                self.assertFalse(ready)
                self.assertEqual(calls,[])

    def test_each_load_failure_unwinds_only_successfully_loaded_modules(self):
        modules=['mtk_rpmsg','mtk_scp_ipi','mtk_scp','athena_ap','athena_ap_vdec']
        for index,module in enumerate(modules):
            with self.subTest(module=module):
                code,calls,ready=self.trial(fail=module)
                self.assertNotEqual(code,0)
                self.assertFalse(ready)
                self.assertEqual([line.split()[1] for line in calls if line.startswith('unload ')],
                                 list(reversed(modules[:index])))


if __name__=='__main__': unittest.main()
