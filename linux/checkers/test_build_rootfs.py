import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


MODULE = Path(__file__).with_name("build_rootfs.py")
SPEC = importlib.util.spec_from_file_location("checkers_build_rootfs", MODULE)
build_rootfs = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build_rootfs)


class BootPatchTests(unittest.TestCase):
    def test_patch_boot_adds_tater_health_and_hardware_contract(self):
        source = """#!/bin/sh
LOGDIR=/data/techo5-linux
mdev -s
t5_wifi_conf $LOGDIR/wpa_supplicant.conf
	t5_bt_up "$BT_MODULE" /var/log
			reboot
			true
			reboot
		pid=$(pidof techo5 | cut -d' ' -f1)
		if [ -n "$pid" ]; then
			started=healthy
			if [ -n "$started" ]; then
				log "slot $s: daemon up for $TRIAL_SETTLE s, committing"
				slotctl commit >> /run/boot.log 2>&1
				exit 0
			fi
		fi
		if [ $now -ge $TRIAL_TIMEOUT ]; then
			log "trial timeout"
		fi
cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
log "boot script done"
"""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "etc" / "techo5" / "boot.sh"
            path.parent.mkdir(parents=True)
            path.write_text(source)
            build_rootfs.patch_boot(Path(directory))
            patched = path.read_text()

        self.assertIn("LOGDIR=/data/tater-linux", patched)
        self.assertIn("ip link set lo up", patched)
        self.assertIn("echo 1 4 1 7 > /proc/sys/kernel/printk", patched)
        self.assertLess(
            patched.index("echo 1 4 1 7 > /proc/sys/kernel/printk"),
            patched.index("t5_wifi_conf $LOGDIR/wpa_supplicant.conf"),
        )
        self.assertIn("/data/techo5-linux/wpa_supplicant.conf", patched)
        self.assertIn("[ -s /run/tater-connected ]", patched)
        self.assertIn("pidof tater-show", patched)
        self.assertIn("pidof tater-camera", patched)
        self.assertIn("Tater application A/B health", patched)
        self.assertIn("tater-app commit", patched)
        self.assertIn("SLOTCTL_LEAVE_RW=1 slotctl commit", patched)
        self.assertIn("committed; restarting with read-only mounts", patched)
        self.assertIn("[ ! -s /data/local/etc/tater/device_token ]", patched)
        self.assertIn("TRIAL_TIMEOUT=$((now+900))", patched)
        self.assertLess(
            patched.index("TRIAL_TIMEOUT=$((now+900))"),
            patched.index("if [ $now -ge $TRIAL_TIMEOUT ]; then"),
        )
        self.assertNotIn('t5_bt_up "$BT_MODULE"', patched)
        self.assertEqual(4, patched.count("/usr/local/bin/tater-reboot-now"))

    def test_patch_slotctl_can_leave_store_writable_for_direct_restart(self):
        source = """#!/bin/sh
usage() {
  cat <<EOF
  switch <a|b>            boot this slot next; a bad slot goes back on trial
EOF
}
done_writing() {
\tsync
\tif [ -n "$WAS_RO" ]; then
\t\tmount -o remount,ro "$STORE" 2>/dev/null || echo "slotctl: note: $STORE left writable (busy)" >&2
\t\tWAS_RO=
\tfi
}
cmd_switch() {
\t:
}
case "$cmd" in
commit) cmd_commit;;
esac
"""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "usr" / "local" / "sbin" / "slotctl"
            path.parent.mkdir(parents=True)
            path.write_text(source)
            build_rootfs.patch_slotctl(Path(directory))
            patched = path.read_text()

        self.assertIn('[ "$SLOTCTL_LEAVE_RW" = 1 ]', patched)
        self.assertIn("left writable for immediate restart", patched)
        self.assertIn('rearm) cmd_rearm;;', patched)
        self.assertIn('set_state $s "trial $TRIES"', patched)
        self.assertEqual(1, patched.count('mount -o remount,ro "$STORE"'))

    def test_setup_rearm_refreshes_only_running_trial(self):
        source = """#!/bin/sh
STORE=${STORE:?}
TRIES=3
WAS_RO=
usage() {
  switch <a|b>            boot this slot next; a bad slot goes back on trial
}
die() { echo "$*" >&2; exit 1; }
writable() { test -e "$STORE/.techo5-store" || die "not a store"; }
done_writing() {
\tsync
\tif [ -n "$WAS_RO" ]; then
\t\tmount -o remount,ro "$STORE" 2>/dev/null || echo "slotctl: note: $STORE left writable (busy)" >&2
\t\tWAS_RO=
\tfi
}
state() { cat "$STORE/slots/$1.state"; }
set_state() { printf '%s\\n' "$2" > "$STORE/slots/$1.state"; }
booted() { echo a; }
cmd_switch() {
\t:
}
cmd=$1
case "$cmd" in
commit) cmd_commit;;
esac
"""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            script = root / "usr/local/sbin/slotctl"
            script.parent.mkdir(parents=True)
            script.write_text(source)
            build_rootfs.patch_slotctl(root)
            store = root / "store"
            (store / "slots").mkdir(parents=True)
            (store / ".techo5-store").touch()
            state = store / "slots/a.state"
            environment = dict(os.environ, STORE=str(store), SLOTCTL_LEAVE_RW="1")

            state.write_text("trial 0\n")
            result = subprocess.run(["sh", str(script), "rearm"], env=environment,
                                    capture_output=True, text=True)
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("trial 3\n", state.read_text())

            state.write_text("good\n")
            result = subprocess.run(["sh", str(script), "rearm"], env=environment,
                                    capture_output=True, text=True)
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("good\n", state.read_text())


if __name__ == "__main__":
    unittest.main()
