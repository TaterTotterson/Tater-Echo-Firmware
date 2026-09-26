from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parent
MAGISK = ROOT / "magisk"


def privacy_packages() -> set[str]:
    return {
        line.strip()
        for line in (MAGISK / "privacy-packages.txt").read_text().splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    }


def privacy_components() -> set[str]:
    return {
        line.strip()
        for line in (MAGISK / "privacy-components.txt").read_text().splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    }


class CheckersPrivacyTests(unittest.TestCase):
    def test_high_risk_amazon_surfaces_are_quarantined(self):
        packages = privacy_packages()
        expected = {
            "amazon.speech.sim",
            "amazon.speech.davs.davcservice",
            "com.amazon.afe.app",
            "com.amazon.alexa.awaservice",
            "com.amazon.application.compatibility.enforcer",
            "com.amazon.bluestone.keyboard",
            "com.amazon.bluestone.proxy",
            "com.amazon.device.messaging",
            "com.amazon.device.metrics",
            "com.amazon.device.software.ota",
            "com.amazon.device.software.ota.override",
            "com.amazon.imp",
            "com.amazon.kindleautomatictimezone",
            "com.amazon.neo.minerva",
            "com.amazon.providers.contentsupport",
            "com.amazon.tcomm",
            "com.amazon.whisperjoin.middleware.v2.np",
            "com.amazon.wifi.sync",
            "com.fireos.usagestats.proxy",
        }
        self.assertTrue(expected <= packages, expected - packages)

    def test_hardware_and_framework_packages_are_not_disabled(self):
        packages = privacy_packages()
        retained = {
            "amazon.fireos",
            "com.amazon.android.service.wifiprofilemanager",
            "com.amazon.device.echoaudioservice",
            "com.amazon.device.settings",
            "com.amazon.webview",
            "com.amazon.webview.chromium",
        }
        self.assertFalse(retained & packages, retained & packages)

    def test_alexa_indoor_location_service_is_reversibly_stopped(self):
        script = (MAGISK / "privacy.sh").read_text()
        self.assertIn("apply_native_services", script)
        self.assertIn("restore_native_services", script)
        self.assertIn("inlocservice-was-running", script)
        self.assertIn("trackerd", script)
        self.assertIn("halo-hub-broker", script)
        self.assertIn("halo-bssid-scan", script)
        self.assertIn('stop "$service"', script)
        self.assertIn('start "$service"', script)
        self.assertLess(script.index("apply_native_services"), script.index("snapshot_package_uids"))

    def test_policy_is_reversible_and_makes_shared_framework_uid_lan_only(self):
        script = (MAGISK / "privacy.sh").read_text()
        self.assertIn("pm disable-user --user 0", script)
        self.assertIn('pm disable --user 0 "$component"', script)
        self.assertIn('component_is_disabled "$component"', script)
        self.assertIn("pm enable --user 0", script)
        self.assertIn("pm default-state --user 0", script)
        self.assertIn("snapshot_package_state", script)
        self.assertIn("snapshot_package_uids", script)
        self.assertIn("pm list packages </dev/null", script)
        self.assertIn('dumpsys package "$pkg" </dev/null', script)
        self.assertIn("firewall-policy.md5", script)
        self.assertIn('done < "$FIREWALL_UIDS"', script)
        self.assertIn("disabled-by-tater.txt", script)
        self.assertIn('[ "$uid" -gt 1000 ] || continue', script)
        self.assertIn("FRAMEWORK_UID=1000", script)
        self.assertIn('echo "$FRAMEWORK_UID" >> "$FIREWALL_UIDS.tmp"', script)
        self.assertIn("192.168.0.0/16", script)
        self.assertIn("fc00::/7", script)
        self.assertLess(script.index("192.168.0.0/16"), script.index('done < "$FIREWALL_UIDS"'))
        self.assertIn("clear_firewall4", script)
        self.assertIn("clear_firewall6", script)
        self.assertIn('iptables -A OUTPUT -j "$CHAIN4"', script)
        self.assertIn('ip6tables -A OUTPUT -j "$CHAIN6"', script)
        self.assertEqual(1, script.count('iptables -A OUTPUT -j "$CHAIN4"'))
        self.assertEqual(1, script.count('ip6tables -A OUTPUT -j "$CHAIN6"'))
        self.assertIn("append_owner_reject4", script)
        self.assertIn("append_owner_reject6", script)
        self.assertEqual(2, script.count("while [ $attempt -lt 3 ]"))
        self.assertIn('privacy_log "ipv4-uid-failed $uid attempts=$attempt"', script)
        self.assertIn('privacy_log "ipv6-uid-failed $uid attempts=$attempt"', script)
        self.assertNotIn('iptables -A "$CHAIN4" -j REJECT', script)
        self.assertNotIn('ip6tables -A "$CHAIN6" -j REJECT', script)
        self.assertIn("com.amazon.webview.chromium", script)
        self.assertIn("com.amazon.device.messaging.sdk.library", script)

    def test_shared_framework_components_remain_registered(self):
        expected = set()
        self.assertEqual(privacy_components(), expected)
        self.assertNotIn(
            "amazon.fireos/com.amazon.arcus.ArcusJobScheduler",
            expected,
        )
        self.assertFalse(any(component.startswith("amazon.fireos/") for component in expected))
        self.assertNotIn(
            "amazon.fireos/com.amazon.android.service.perfrecoverydhelper.UserActivityDetectionScheduler",
            expected,
        )
        self.assertNotIn(
            "amazon.fireos/com.amazon.android.settings.amazondropbox.LogRetentionJobService",
            expected,
        )
        script = (MAGISK / "privacy.sh").read_text()
        self.assertIn("apply_components", script)
        self.assertIn("restore_components", script)
        self.assertIn("disabled-components-by-tater.txt", script)

    def test_package_list_has_no_duplicates(self):
        lines = [
            line.strip()
            for line in (MAGISK / "privacy-packages.txt").read_text().splitlines()
            if line.strip() and not line.lstrip().startswith("#")
        ]
        self.assertEqual(len(lines), len(set(lines)))
        self.assertEqual(lines, sorted(lines))

    def test_boot_supervisor_lock_survives_mksh_command_substitutions(self):
        script = (MAGISK / "service.sh").read_text()
        self.assertIn("checkers-service.pid", script)
        self.assertIn("kill -0", script)
        self.assertIn("/proc/$old_supervisor/cmdline", script)
        self.assertIn("ignored stale supervisor", script)
        self.assertIn("cleanup_supervisor; exit 0", script)
        self.assertNotIn("trap cleanup_supervisor EXIT", script)

    def test_screen_watchdog_recovers_framework_and_display_failures(self):
        script = (MAGISK / "service.sh").read_text()
        self.assertIn("screen_watchdog", script)
        self.assertIn('pidof "$PACKAGE"', script)
        self.assertIn('am start --user 0 -W -n "$ACTIVITY"', script)
        self.assertIn("service.bootanim.exit", script)
        self.assertIn("ctl.stop bootanim", script)
        self.assertIn("screen-recovery-count", script)
        self.assertIn("SCREEN_REBOOT_FAILURES=12", script)
        self.assertIn('kill "$SCREEN_WATCHDOG_PID"', script)
        self.assertIn("trap 'exit 0' TERM INT", script)

    def test_tater_screen_and_server_are_not_blocked_by_first_privacy_pass(self):
        script = (MAGISK / "service.sh").read_text()
        activity_start = script.index('am start --user 0 -n "$ACTIVITY"')
        privacy_start = script.index('privacy.sh apply')
        server_loop = script.index("fast_exits=0")
        self.assertLess(activity_start, privacy_start)
        self.assertLess(privacy_start, server_loop)
        self.assertIn("PRIVACY_PID=$!", script)
        self.assertIn('kill "$PRIVACY_PID"', script)

    def test_privacy_firewall_precedes_slow_package_cleanup(self):
        script = (MAGISK / "privacy.sh").read_text()
        apply_case = script[script.index('case "${1:-apply}" in'):]
        self.assertLess(apply_case.index("refresh_firewall"), apply_case.index("apply_packages"))
        refresh = script[script.index("refresh_firewall() {"):script.index('case "${1:-apply}" in')]
        self.assertLess(refresh.index("snapshot_package_uids"), refresh.index("apply_cached_firewall"))
        snapshot = script[script.index("snapshot_package_uids() {"):script.index("firewall_packages() {")]
        self.assertLess(snapshot.index('mkdir -p "$STATE_DIR"'), snapshot.index(': > "$UID_SNAPSHOT"'))
        self.assertIn("MIN_FIREWALL_UIDS=100", script)
        self.assertIn("firewall_uids_valid", snapshot)
        self.assertIn("firewall_is_live", script)
        self.assertIn('apply_cached_firewall force', script)
        self.assertIn('privacy_log "firewall already active', script)

    def test_early_boot_firewall_uses_installer_validated_cache(self):
        early = (MAGISK / "post-fs-data.sh").read_text()
        privacy = (MAGISK / "privacy.sh").read_text()
        self.assertIn('"$PRIVACY" firewall-cached', early)
        self.assertNotIn("dumpsys", early)
        self.assertIn("firewall-cached)", privacy)
        self.assertIn("apply_cached_firewall", privacy)

    def test_persistent_speech_manager_is_hidden_before_android_starts(self):
        marker = MAGISK / "speech-interaction-manager.replace"
        self.assertTrue(marker.is_file())
        self.assertIn("SpeechInteractionManager/.replace", marker.read_text())
        bishop = MAGISK / "bishop.replace"
        self.assertTrue(bishop.is_file())
        self.assertIn("com.amazon.bishop/.replace", bishop.read_text())


if __name__ == "__main__":
    unittest.main()
