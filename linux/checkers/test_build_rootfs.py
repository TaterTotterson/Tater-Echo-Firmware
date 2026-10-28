import importlib.util
import os
from pathlib import Path
import shlex
import subprocess
import tarfile
import tempfile
import unittest


MODULE = Path(__file__).with_name("build_rootfs.py")
SPEC = importlib.util.spec_from_file_location("checkers_build_rootfs", MODULE)
build_rootfs = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build_rootfs)


class BootPatchTests(unittest.TestCase):
    def test_release_workflow_uses_unified_show_platform_subdirectory(self):
        workflow = (
            Path(__file__).resolve().parents[2] / ".github/workflows/release.yml"
        ).read_text()
        checkers_job = workflow.split("\n  checkers:\n", 1)[1].split(
            "\n  athena:\n", 1
        )[0]

        self.assertEqual(
            1,
            checkers_job.count(
                "--platform /tmp/tater-echo-linux-show/platforms/show"
            ),
        )

    def test_ssh_group_is_root_only_idempotent_and_collision_safe(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            group = root / "etc/group"
            group.parent.mkdir(parents=True)
            group.write_text("root:x:0:root\nusers:x:100:alice\n")
            build_rootfs.ensure_tater_ssh_group(root)
            build_rootfs.ensure_tater_ssh_group(root)
            self.assertEqual(1, group.read_text().count("tater-ssh:x:900:root"))

            group.write_text("root:x:0:root\noccupied:x:900:user\n")
            with self.assertRaisesRegex(SystemExit, "reserved tater-ssh gid"):
                build_rootfs.ensure_tater_ssh_group(root)

    def test_video_decoder_user_is_unprivileged_idempotent_and_network_only(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "etc").mkdir(parents=True)
            (root / "etc/passwd").write_text("root:x:0:0:root:/root:/bin/sh\n")
            (root / "etc/group").write_text("root:x:0:root\ninet:x:3003:avahi\n")
            build_rootfs.ensure_video_decoder_user(root)
            build_rootfs.ensure_video_decoder_user(root)
            self.assertEqual(1, (root / "etc/passwd").read_text().count("techo5-video:x:89:89:"))
            self.assertEqual(1, (root / "etc/group").read_text().count("techo5-video:x:89:"))
            self.assertIn("inet:x:3003:avahi,techo5-video", (root / "etc/group").read_text())

    def test_network_keeper_yields_to_active_usb_ethernet(self):
        source = """(
\tdown=0; lost=0
\twhile true; do
\t\tsleep 60
\t\t# Someone is choosing a network on the screen: not a fault, no reboot.
\t\t[ -e /run/techo5/wifi-setup ] && continue
\tdone
) &
"""
        patched = build_rootfs.patch_network_keeper(source)
        self.assertIn("grep -q '^active ' /run/tater-usb-ethernet.state", patched)
        self.assertIn("down=0; lost=0; continue", patched)
        self.assertLess(
            patched.index("/run/tater-usb-ethernet.state"),
            patched.index("/run/techo5/wifi-setup"),
        )

    def test_safe_extract_allows_rootfs_absolute_links_but_not_link_traversal(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive_path = root / "rootfs.tar"
            with tarfile.open(archive_path, "w") as archive:
                bin_dir = tarfile.TarInfo("./bin")
                bin_dir.type = tarfile.DIRTYPE
                bin_dir.mode = 0o755
                archive.addfile(bin_dir)
                link = tarfile.TarInfo("./bin/cp")
                link.type = tarfile.SYMTYPE
                link.linkname = "/bin/busybox"
                archive.addfile(link)
            destination = root / "safe"
            destination.mkdir()
            with tarfile.open(archive_path) as archive:
                build_rootfs.safe_extract(archive, destination)
            self.assertEqual("/bin/busybox", os.readlink(destination / "bin/cp"))

            with tarfile.open(archive_path, "w") as archive:
                link = tarfile.TarInfo("./escape")
                link.type = tarfile.SYMTYPE
                link.linkname = "/tmp"
                archive.addfile(link)
                archive.addfile(tarfile.TarInfo("./escape/owned"))
            with tarfile.open(archive_path) as archive:
                with self.assertRaisesRegex(SystemExit, "unsafe linked path"):
                    build_rootfs.safe_extract(archive, root / "blocked")

            with tarfile.open(archive_path, "w") as archive:
                link = tarfile.TarInfo("./escape")
                link.type = tarfile.SYMTYPE
                link.linkname = "../../outside"
                archive.addfile(link)
            with tarfile.open(archive_path) as archive:
                with self.assertRaisesRegex(SystemExit, "unsafe link target"):
                    build_rootfs.safe_extract(archive, root / "relative-escape")

            with tarfile.open(archive_path, "w") as archive:
                link = tarfile.TarInfo("./hardlink")
                link.type = tarfile.LNKTYPE
                link.linkname = "../../outside"
                archive.addfile(link)
            with tarfile.open(archive_path) as archive:
                with self.assertRaisesRegex(SystemExit, "unsafe link target"):
                    build_rootfs.safe_extract(archive, root / "hardlink-escape")

    def test_app_rollback_replaces_active_slot_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory) / "app"
            for slot in ("a", "b"):
                path = app / "slots" / slot
                path.mkdir(parents=True)
                for name in ("tater-echo", "tater-show"):
                    binary = path / name
                    binary.write_text("#!/bin/sh\n")
                    binary.chmod(0o755)
            (app / "current").symlink_to("slots/b")
            (app / "pending.env").write_text("previous=a\n")
            script = Path(directory) / "tater-app"
            script.write_text(build_rootfs.TATER_APP.replace(
                "APP=/data/tater-linux/app", f"APP={shlex.quote(str(app))}", 1))

            result = subprocess.run(["sh", str(script), "rollback"],
                                    capture_output=True, text=True)
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual("slots/a", os.readlink(app / "current"))
            self.assertFalse((app / "pending.env").exists())
            self.assertFalse((app / "slots/b/current.rollback").exists())

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

    def test_patch_athena_boot_preserves_platform_and_adds_tater_health(self):
        source = """#!/bin/sh
LOGDIR=/data/techo5-linux
mdev -s
t5_wifi_conf $LOGDIR/wpa_supplicant.conf
		pid=$(pidof techo5 | cut -d' ' -f1)
		if [ -n "$pid" ]; then
			started=healthy
		fi
		if [ $now -ge $TRIAL_TIMEOUT ]; then
			reboot
		fi
cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
log "boot script done"
"""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "etc" / "techo5" / "boot.sh"
            path.parent.mkdir(parents=True)
            path.write_text(source)
            build_rootfs.patch_boot(Path(directory), target="athena")
            patched = path.read_text()

        self.assertIn("LOGDIR=/data/tater-linux", patched)
        self.assertIn("pidof tater-echo", patched)
        self.assertIn("[ -s /run/tater-connected ]", patched)
        self.assertIn("pidof tater-show", patched)
        self.assertIn("pidof tater-camera", patched)
        self.assertIn("ip link set lo up", patched)
        self.assertIn("/data/techo5-linux/wpa_supplicant.conf", patched)
        self.assertIn("[ ! -s /data/local/etc/tater/device_token ]", patched)
        self.assertLess(
            patched.index("TRIAL_TIMEOUT=$((now+900))"),
            patched.index("if [ $now -ge $TRIAL_TIMEOUT ]; then"),
        )
        self.assertIn("Tater application A/B health", patched)
        self.assertIn("tater-app commit", patched)

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
