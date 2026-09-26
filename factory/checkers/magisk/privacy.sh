#!/system/bin/sh
# Reversible privacy policy for the certified rooted Fire OS Checkers build.
# Tater and ordinary Android applications keep public Internet access. Amazon
# applications are disabled where safe; retained hardware services are limited
# to loopback, link-local, and private/LAN destinations by application UID.

MODULE_DIR=/data/adb/modules/tater_checkers
PACKAGE_LIST=$MODULE_DIR/privacy-packages.txt
COMPONENT_LIST=$MODULE_DIR/privacy-components.txt
STATE_DIR=/data/local/etc/tater/privacy
DISABLED_BY_TATER=$STATE_DIR/disabled-by-tater.txt
DISABLED_COMPONENTS_BY_TATER=$STATE_DIR/disabled-components-by-tater.txt
INSTALLED_SNAPSHOT=$STATE_DIR/installed-packages.tmp
DISABLED_SNAPSHOT=$STATE_DIR/disabled-packages.tmp
UID_SNAPSHOT=$STATE_DIR/package-uids.tmp
FIREWALL_UIDS=$STATE_DIR/firewall-uids.txt
FIREWALL_FINGERPRINT=$STATE_DIR/firewall-policy.md5
INLOC_WAS_RUNNING=$STATE_DIR/inlocservice-was-running
NATIVE_SERVICES="inlocservice trackerd sidewalk-init halo-regiond halo-hub-broker halo-bssid-scan halo-hub-core halo-logr halo-core-swp"
LOG=/data/local/etc/tater/privacy.log
CHAIN4=TATER_AMAZON
CHAIN6=TATER_AMAZON
FRAMEWORK_UID=1000
MIN_FIREWALL_UIDS=100
FIREWALL_REFRESHED=0

privacy_log() {
    echo "$(date +%s) $*" >> "$LOG"
}

snapshot_package_state() {
    # Query Package Manager once per pass. Besides being much faster on the
    # Show, this keeps Android tools from inheriting the package-list loop's
    # stdin (some Fire OS commands otherwise consume the remaining policy).
    pm list packages </dev/null 2>/dev/null | sed 's/^package://' > "$INSTALLED_SNAPSHOT"
    pm list packages -d </dev/null 2>/dev/null | sed 's/^package://' > "$DISABLED_SNAPSHOT"
}

apply_packages() {
    mkdir -p "$STATE_DIR"
    chmod 700 "$STATE_DIR" 2>/dev/null
    touch "$DISABLED_BY_TATER"
    chmod 600 "$DISABLED_BY_TATER" 2>/dev/null
    snapshot_package_state
    disabled=0
    while IFS= read -r pkg; do
        case "$pkg" in
            ""|\#*) continue ;;
        esac
        grep -Fqx "$pkg" "$INSTALLED_SNAPSHOT" 2>/dev/null || continue
        grep -Fqx "$pkg" "$DISABLED_SNAPSHOT" 2>/dev/null && continue
        if pm disable-user --user 0 "$pkg" </dev/null >/dev/null 2>&1; then
            grep -qx "$pkg" "$DISABLED_BY_TATER" 2>/dev/null || echo "$pkg" >> "$DISABLED_BY_TATER"
            echo "$pkg" >> "$DISABLED_SNAPSHOT"
            disabled=$((disabled + 1))
        else
            privacy_log "package-disable-failed $pkg"
        fi
    done < "$PACKAGE_LIST"
    rm -f "$INSTALLED_SNAPSHOT" "$DISABLED_SNAPSHOT"
    privacy_log "package-policy applied newly-disabled=$disabled"
}

component_is_disabled() {
    component=$1
    pkg=${component%%/*}
    class=${component#*/}
    dumpsys package "$pkg" </dev/null 2>/dev/null \
        | sed -n '/disabledComponents:/,/enabledComponents:/p' \
        | grep -Fq "$class"
}

apply_components() {
    [ -f "$COMPONENT_LIST" ] || return 0
    mkdir -p "$STATE_DIR"
    touch "$DISABLED_COMPONENTS_BY_TATER"
    chmod 600 "$DISABLED_COMPONENTS_BY_TATER" 2>/dev/null
    disabled=0
    while IFS= read -r component; do
        case "$component" in
            ""|\#*) continue ;;
        esac
        component_is_disabled "$component" && continue
        # Fire OS accepts `disable-user` for a component but silently leaves it
        # at the default state. `disable --user` persists the component state;
        # verify Package Manager's resulting state instead of trusting exit 0.
        if pm disable --user 0 "$component" </dev/null >/dev/null 2>&1 \
                && component_is_disabled "$component"; then
            grep -Fqx "$component" "$DISABLED_COMPONENTS_BY_TATER" 2>/dev/null \
                || echo "$component" >> "$DISABLED_COMPONENTS_BY_TATER"
            disabled=$((disabled + 1))
        else
            privacy_log "component-disable-failed $component"
        fi
    done < "$COMPONENT_LIST"
    privacy_log "component-policy applied newly-disabled=$disabled"
}

restore_components() {
    restored=0
    if [ -f "$DISABLED_COMPONENTS_BY_TATER" ]; then
        while IFS= read -r component; do
            [ -n "$component" ] || continue
            if pm default-state --user 0 "$component" </dev/null >/dev/null 2>&1; then
                restored=$((restored + 1))
            else
                privacy_log "component-restore-failed $component"
            fi
        done < "$DISABLED_COMPONENTS_BY_TATER"
    fi
    rm -f "$DISABLED_COMPONENTS_BY_TATER"
    privacy_log "component-policy restored count=$restored"
}

restore_packages() {
    restored=0
    mkdir -p "$STATE_DIR"
    pm list packages </dev/null 2>/dev/null | sed 's/^package://' > "$INSTALLED_SNAPSHOT"
    if [ -f "$DISABLED_BY_TATER" ]; then
        while IFS= read -r pkg; do
            [ -n "$pkg" ] || continue
            grep -Fqx "$pkg" "$INSTALLED_SNAPSHOT" 2>/dev/null || continue
            if pm enable --user 0 "$pkg" </dev/null >/dev/null 2>&1; then
                restored=$((restored + 1))
            else
                privacy_log "package-restore-failed $pkg"
            fi
        done < "$DISABLED_BY_TATER"
    fi
    rm -f "$DISABLED_BY_TATER" "$INSTALLED_SNAPSHOT" "$DISABLED_SNAPSHOT" "$UID_SNAPSHOT" \
        "$FIREWALL_UIDS" "$FIREWALL_FINGERPRINT" \
        "$STATE_DIR/firewall-uids-v4.txt" "$STATE_DIR/firewall-uids-v6.txt"
    privacy_log "package-policy restored count=$restored"
}

apply_native_services() {
    # These init daemons belong to Alexa indoor location and Amazon Sidewalk,
    # not Android's Wi-Fi/Bluetooth drivers. Record ownership per service so
    # uninstall restores only daemons Tater itself took down.
    for service in $NATIVE_SERVICES; do
        marker=$STATE_DIR/native-service-$service-was-running
        [ "$service" = "inlocservice" ] && marker=$INLOC_WAS_RUNNING
        state=$(getprop init.svc.$service 2>/dev/null)
        if [ "$state" = "running" ]; then
            [ -e "$marker" ] || : > "$marker"
            stop "$service" >/dev/null 2>&1
            privacy_log "native-service stopped $service"
        fi
    done
}

restore_native_services() {
    for service in $NATIVE_SERVICES; do
        marker=$STATE_DIR/native-service-$service-was-running
        [ "$service" = "inlocservice" ] && marker=$INLOC_WAS_RUNNING
        if [ -e "$marker" ]; then
            start "$service" >/dev/null 2>&1
            rm -f "$marker"
            privacy_log "native-service restored $service"
        fi
    done
}

firewall_uids_valid() {
    [ -s "$FIREWALL_UIDS" ] || return 1
    count=$(wc -l < "$FIREWALL_UIDS" 2>/dev/null)
    case "$count" in
        ""|*[!0-9]*) return 1 ;;
    esac
    [ "$count" -ge "$MIN_FIREWALL_UIDS" ] || return 1
    grep -qx "$FRAMEWORK_UID" "$FIREWALL_UIDS" 2>/dev/null
}

snapshot_package_uids() {
    # This is deliberately the first operation on a clean install so the
    # firewall is present before the slower package quarantine. Create its
    # state directory here rather than relying on apply_packages to do it
    # later in the pass.
    mkdir -p "$STATE_DIR"
    chmod 700 "$STATE_DIR" 2>/dev/null
    fingerprint=$(md5sum "$PACKAGE_LIST" "$MODULE_DIR/privacy.sh" 2>/dev/null \
        | md5sum | sed 's/[[:space:]].*//')
    if [ -n "$fingerprint" ] && firewall_uids_valid \
            && [ "$(cat "$FIREWALL_FINGERPRINT" 2>/dev/null)" = "$fingerprint" ]; then
        return 0
    fi
    : > "$UID_SNAPSHOT"
    firewall_packages | while IFS= read -r pkg; do
        case "$pkg" in
            ""|\#*) continue ;;
        esac
        uid=$(dumpsys package "$pkg" </dev/null 2>/dev/null \
            | sed -n 's/^[[:space:]]*userId=//p' | head -n 1)
        case "$uid" in
            ""|*[!0-9]*) continue ;;
        esac
        echo "$pkg $uid" >> "$UID_SNAPSHOT"
    done
    : > "$FIREWALL_UIDS.tmp"
    while IFS=' ' read -r _pkg uid; do
        case "$uid" in
            ""|*[!0-9]*) continue ;;
        esac
        # Never inherit arbitrary platform/service UIDs from Amazon packages.
        # The one audited shared framework UID is added explicitly below.
        [ "$uid" -gt 1000 ] || continue
        grep -qx "$uid" "$FIREWALL_UIDS.tmp" 2>/dev/null || echo "$uid" >> "$FIREWALL_UIDS.tmp"
    done < "$UID_SNAPSHOT"
    # Fire OS hosts Arcus/config/metrics code inside Settings and system_server.
    # Their components cannot be disabled without crash loops. Make their
    # shared UID LAN-only instead; Tater's root daemon and app UID retain public
    # access, and the firewall chains permit private/link-local traffic first.
    grep -qx "$FRAMEWORK_UID" "$FIREWALL_UIDS.tmp" 2>/dev/null \
        || echo "$FRAMEWORK_UID" >> "$FIREWALL_UIDS.tmp"
    mv "$FIREWALL_UIDS.tmp" "$FIREWALL_UIDS"
    if ! firewall_uids_valid; then
        privacy_log "firewall UID snapshot invalid"
        rm -f "$FIREWALL_UIDS" "$FIREWALL_FINGERPRINT"
        return 1
    fi
    echo "$fingerprint" > "$FIREWALL_FINGERPRINT"
    chmod 600 "$FIREWALL_UIDS" "$FIREWALL_FINGERPRINT" 2>/dev/null
    FIREWALL_REFRESHED=1
}

firewall_packages() {
    cat "$PACKAGE_LIST" 2>/dev/null
    # These packages remain enabled because they provide hardware/platform
    # support. None needs public Internet access for Tater's use of the Show.
cat <<'EOF'
amazon.fireos
amazon.jackson19
android.amazon.perm
com.amazon.application.compatibility.enforcer
com.amazon.application.compatibility.enforcer.sdk.library
com.amazon.android.service.wifiprofilemanager
com.amazon.client.metrics.api
com.amazon.dcp.contracts.framework.library
com.amazon.dcp.contracts.library
com.amazon.device.echoaudioservice
com.amazon.device.messaging.sdk.internal.library
com.amazon.device.messaging.sdk.library
com.amazon.device.settings
com.amazon.device.settings.sdk.internal.library
com.amazon.device.sync.sdk.internal
com.amazon.knight.btavrcp
com.amazon.realtimeregistry.device.sdk.library
com.amazon.tcomm.jackson
com.amazon.webview
com.amazon.webview.chromium
com.amazon.wifi.sync
EOF
}

clear_firewall4() {
    command -v iptables >/dev/null 2>&1 || return 0
    # Remove both the current single hook and legacy per-UID hooks created by
    # early development builds. The rule text comes only from iptables itself.
    while true; do
        rule=$(iptables -S OUTPUT 2>/dev/null | grep -F -- "-j $CHAIN4" | head -n 1)
        [ -n "$rule" ] || break
        delete_rule=${rule#-A }
        iptables -D $delete_rule >/dev/null 2>&1 || break
    done
    iptables -F "$CHAIN4" >/dev/null 2>&1 || true
    iptables -X "$CHAIN4" >/dev/null 2>&1 || true
}

clear_firewall6() {
    command -v ip6tables >/dev/null 2>&1 || return 0
    while true; do
        rule=$(ip6tables -S OUTPUT 2>/dev/null | grep -F -- "-j $CHAIN6" | head -n 1)
        [ -n "$rule" ] || break
        delete_rule=${rule#-A }
        ip6tables -D $delete_rule >/dev/null 2>&1 || break
    done
    ip6tables -F "$CHAIN6" >/dev/null 2>&1 || true
    ip6tables -X "$CHAIN6" >/dev/null 2>&1 || true
}

append_owner_reject4() {
    uid=$1
    attempt=0
    while [ $attempt -lt 3 ]; do
        iptables -A "$CHAIN4" -m owner --uid-owner "$uid" -j REJECT && return 0
        attempt=$((attempt + 1))
        sleep 1
    done
    privacy_log "ipv4-uid-failed $uid attempts=$attempt"
    return 1
}

append_owner_reject6() {
    uid=$1
    attempt=0
    while [ $attempt -lt 3 ]; do
        ip6tables -A "$CHAIN6" -m owner --uid-owner "$uid" -j REJECT && return 0
        attempt=$((attempt + 1))
        sleep 1
    done
    privacy_log "ipv6-uid-failed $uid attempts=$attempt"
    return 1
}

build_firewall4() {
    command -v iptables >/dev/null 2>&1 || {
        privacy_log "ipv4-firewall unavailable"
        return 0
    }
    clear_firewall4
    iptables -N "$CHAIN4" || return 1
    for net in 127.0.0.0/8 10.0.0.0/8 100.64.0.0/10 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 224.0.0.0/4; do
        iptables -A "$CHAIN4" -d "$net" -j RETURN || return 1
    done
    iptables -A "$CHAIN4" -d 255.255.255.255/32 -j RETURN || return 1
    failed=0
    while IFS= read -r uid; do
        [ -n "$uid" ] || continue
        append_owner_reject4 "$uid" || failed=1
    done < "$FIREWALL_UIDS"
    iptables -A "$CHAIN4" -j RETURN || return 1
    iptables -A OUTPUT -j "$CHAIN4" || return 1
    privacy_log "ipv4-firewall applied uids=$(wc -l < "$FIREWALL_UIDS" 2>/dev/null)"
    [ $failed -eq 0 ]
}

build_firewall6() {
    command -v ip6tables >/dev/null 2>&1 || {
        privacy_log "ipv6-firewall unavailable"
        return 0
    }
    clear_firewall6
    ip6tables -N "$CHAIN6" || return 1
    for net in ::1/128 fc00::/7 fe80::/10 ff00::/8; do
        ip6tables -A "$CHAIN6" -d "$net" -j RETURN || return 1
    done
    failed=0
    while IFS= read -r uid; do
        [ -n "$uid" ] || continue
        append_owner_reject6 "$uid" || failed=1
    done < "$FIREWALL_UIDS"
    ip6tables -A "$CHAIN6" -j RETURN || return 1
    ip6tables -A OUTPUT -j "$CHAIN6" || return 1
    privacy_log "ipv6-firewall applied uids=$(wc -l < "$FIREWALL_UIDS" 2>/dev/null)"
    [ $failed -eq 0 ]
}

firewall_is_live() {
    expected=$(wc -l < "$FIREWALL_UIDS" 2>/dev/null)
    case "$expected" in
        ""|*[!0-9]*) return 1 ;;
    esac
    iptables -C OUTPUT -j "$CHAIN4" >/dev/null 2>&1 || return 1
    ip6tables -C OUTPUT -j "$CHAIN6" >/dev/null 2>&1 || return 1
    actual4=$(iptables -S "$CHAIN4" 2>/dev/null | grep -c uid-owner)
    actual6=$(ip6tables -S "$CHAIN6" 2>/dev/null | grep -c uid-owner)
    [ "$actual4" -eq "$expected" ] && [ "$actual6" -eq "$expected" ]
}

apply_cached_firewall() {
    if ! firewall_uids_valid; then
        privacy_log "cached firewall unavailable"
        return 1
    fi
    if [ "${1:-}" != "force" ] && firewall_is_live; then
        privacy_log "firewall already active uids=$(wc -l < "$FIREWALL_UIDS")"
        return 0
    fi
    status=0
    build_firewall4 || {
        privacy_log "ipv4-firewall apply failed"
        status=1
    }
    build_firewall6 || {
        privacy_log "ipv6-firewall apply failed"
        status=1
    }
    return $status
}

refresh_firewall() {
    FIREWALL_REFRESHED=0
    snapshot_package_uids || return 1
    if [ "$FIREWALL_REFRESHED" = "1" ]; then
        apply_cached_firewall force
    else
        apply_cached_firewall
    fi
    status=$?
    rm -f "$UID_SNAPSHOT"
    return $status
}

case "${1:-apply}" in
    apply)
        # Establish the LAN-only boundary before the slower PackageManager
        # cleanup. The supervisor runs this pass in the background so Tater's
        # setup/connecting UI and native service remain responsive throughout.
        status=0
        refresh_firewall || status=1
        apply_native_services
        apply_packages
        apply_components
        exit $status
        ;;
    firewall)
        # Installer-time pass: PackageManager is available, so construct and
        # validate the cache before setup can trigger the pairing reboot.
        refresh_firewall
        exit $?
        ;;
    firewall-cached)
        # Magisk post-fs-data runs before PackageManager. Rebuild only from the
        # installer-validated cache so Amazon cannot establish public sockets
        # during Android startup.
        apply_cached_firewall
        exit $?
        ;;
    restore)
        clear_firewall4
        clear_firewall6
        restore_native_services
        restore_components
        restore_packages
        ;;
    *)
        echo "usage: privacy.sh [apply|firewall|firewall-cached|restore]" >&2
        exit 2
        ;;
esac

exit 0
