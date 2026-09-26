#!/system/bin/sh
# Late-start supervisor for the rooted Fire OS Checkers build.

BIN=/data/local/bin/server
CONF=/data/local/etc/tater/native.json
LOG_DIR=/data/local/etc/tater
LOG=$LOG_DIR/checkers-service.log
PACKAGE=com.tatertotterson.show
ACTIVITY=$PACKAGE/.MainActivity
MIN_RUNTIME=15
MAX_FAST_EXITS=3
OTA_DIR=/data/local/etc/tater/ota
OTA_PENDING=$OTA_DIR/pending.env
OTA_HEALTHY=$OTA_DIR/healthy
SUPERVISOR_PID=$LOG_DIR/checkers-service.pid
SCREEN_RECOVERY_STATE=$LOG_DIR/screen-recovery-count
SCREEN_WATCHDOG_PID=
PRIVACY_PID=
SCREEN_RETRY_SECONDS=10
SCREEN_REBOOT_FAILURES=12

mkdir -p "$LOG_DIR"
old_supervisor=$(cat "$SUPERVISOR_PID" 2>/dev/null)
case "$old_supervisor" in
    ""|*[!0-9]*) ;;
    *)
        if [ "$old_supervisor" != "$$" ] && kill -0 "$old_supervisor" >/dev/null 2>&1; then
            old_command=$(tr '\000' ' ' < "/proc/$old_supervisor/cmdline" 2>/dev/null)
            case "$old_command" in
                *"/data/adb/modules/tater_checkers/service.sh"*) exit 0 ;;
                *) echo "ignored stale supervisor pid=$old_supervisor command=$old_command" >> "$LOG" ;;
            esac
        fi
        ;;
esac
echo $$ > "$SUPERVISOR_PID"
cleanup_supervisor() {
    if [ -n "$SCREEN_WATCHDOG_PID" ]; then
        kill "$SCREEN_WATCHDOG_PID" >/dev/null 2>&1
        wait "$SCREEN_WATCHDOG_PID" >/dev/null 2>&1
    fi
    if [ -n "$PRIVACY_PID" ]; then
        kill "$PRIVACY_PID" >/dev/null 2>&1
        wait "$PRIVACY_PID" >/dev/null 2>&1
    fi
    if [ "$(cat "$SUPERVISOR_PID" 2>/dev/null)" = "$$" ]; then
        rm -f "$SUPERVISOR_PID"
    fi
}
# Fire OS mksh inherits EXIT traps into command substitutions while retaining
# the parent's $$, so an ordinary `started=$(date +%s)` would erase this PID
# file. Clean it only when the actual supervisor receives a stop signal. A
# stale file after a crash or reboot is harmless because the next start also
# verifies that a reused PID still belongs to this exact supervisor script.
trap 'cleanup_supervisor; exit 0' TERM INT

if [ -f "$LOG" ] && [ "$(wc -c < "$LOG" 2>/dev/null)" -gt 65536 ]; then
    tail -c 32768 "$LOG" > "$LOG.tmp" 2>/dev/null
    mv "$LOG.tmp" "$LOG" 2>/dev/null
fi

service_log() {
    uptime_s=unknown
    if [ -r /proc/uptime ]; then
        read uptime_s _rest < /proc/uptime
        uptime_s=${uptime_s%.*}
    fi
    echo "up=${uptime_s}s $*" >> "$LOG"
}

finish_boot_animation() {
    # A Fire OS framework restart can leave the bootanimation surface above
    # every application even though sys.boot_completed remains set. Only stop
    # it after the Tater activity has a live process behind it.
    setprop service.bootanim.exit 1 >/dev/null 2>&1
    setprop ctl.stop bootanim >/dev/null 2>&1
}

screen_watchdog() {
    # Do not inherit the supervisor's cleanup trap: Android's mksh retains the
    # parent's $$ in background functions, which could otherwise remove the
    # live supervisor lock when only this child exits.
    trap 'exit 0' TERM INT
    failures=0
    healthy_checks=0
    last_system_server=
    service_log "screen watchdog started"
    while true; do
        sleep "$SCREEN_RETRY_SECONDS"
        [ "$(getprop sys.boot_completed)" = "1" ] || continue
        pm path "$PACKAGE" </dev/null >/dev/null 2>&1 || continue

        system_server_pid=$(pidof system_server 2>/dev/null)
        if [ -n "$last_system_server" ] && [ -n "$system_server_pid" ] \
                && [ "$system_server_pid" != "$last_system_server" ]; then
            service_log "Android framework restarted old=$last_system_server new=$system_server_pid"
        fi
        [ -n "$system_server_pid" ] && last_system_server=$system_server_pid

        if pidof "$PACKAGE" >/dev/null 2>&1; then
            failures=0
            healthy_checks=$((healthy_checks + 1))
            finish_boot_animation
            # A full minute of healthy display operation rearms the single
            # guarded reboot used to recover a wedged Android framework.
            if [ $healthy_checks -ge 6 ] && [ -f "$SCREEN_RECOVERY_STATE" ]; then
                rm -f "$SCREEN_RECOVERY_STATE"
                service_log "screen recovery guard rearmed"
            fi
            continue
        fi

        healthy_checks=0
        failures=$((failures + 1))
        service_log "screen missing; relaunch attempt=$failures system_server=$system_server_pid"
        am start --user 0 -W -n "$ACTIVITY" \
            > /data/local/tmp/tater-screen-relaunch.log 2>&1
        sleep 2
        if pidof "$PACKAGE" >/dev/null 2>&1; then
            failures=0
            finish_boot_animation
            service_log "screen relaunched"
            continue
        fi

        # If Amazon's modified ActivityManager is internally wedged, repeated
        # am starts cannot repair it. Permit one clean reboot, then keep retrying
        # without creating a reboot loop if the APK itself is damaged/missing.
        if [ $failures -ge $SCREEN_REBOOT_FAILURES ]; then
            recovery_count=$(cat "$SCREEN_RECOVERY_STATE" 2>/dev/null)
            case "$recovery_count" in
                ""|*[!0-9]*) recovery_count=0 ;;
            esac
            if [ "$recovery_count" -lt 1 ]; then
                echo 1 > "$SCREEN_RECOVERY_STATE"
                sync
                service_log "screen recovery exhausted; requesting one guarded reboot"
                reboot
                sleep 60
            fi
            failures=0
            service_log "screen recovery reboot already used; continuing relaunch attempts"
        fi
    done
}

ota_value() {
    key=$1
    sed -n "s/^${key}=//p" "$OTA_PENDING" 2>/dev/null | head -n 1
}

rollback_pending_ota() {
    previous=$(ota_value previous_slot)
    rollback_apk=$(ota_value rollback_apk)
    staged_apk=$(ota_value staged_apk)
    case "$previous" in
        server_a|server_b) ;;
        *)
            service_log "OTA rollback refused invalid previous slot=$previous"
            rm -f "$OTA_PENDING" "$OTA_HEALTHY"
            return 1
            ;;
    esac
    if [ -x "/data/local/bin/$previous" ]; then
        ln -sf "$previous" "$BIN"
        service_log "OTA native rollback -> $previous"
    else
        service_log "OTA native rollback missing slot=$previous"
    fi
    if [ -f "$rollback_apk" ]; then
        if pm install -r -d -g "$rollback_apk" >/data/local/tmp/tater-apk-rollback.log 2>&1; then
            service_log "OTA screen APK rolled back"
        else
            service_log "OTA screen APK rollback failed; see tater-apk-rollback.log"
        fi
    else
        service_log "OTA screen rollback APK missing"
    fi
    rm -f "$OTA_PENDING" "$OTA_HEALTHY" "$staged_apk"
    am force-stop "$PACKAGE" >/dev/null 2>&1
}

# Magisk late_start normally runs after Android services exist, but the first
# boot after an APK install can still reach us before PackageManager is ready.
i=0
while [ "$(getprop sys.boot_completed)" != "1" ] && [ $i -lt 120 ]; do
    sleep 1
    i=$((i + 1))
done

if [ ! -x "$BIN" ]; then
    service_log "firmware missing at $BIN; service stopped"
    exit 1
fi

# Tater Show owns the repurposed display. Disabling only Amazon's first-run
# activity prevents an unregistered Fire OS image from covering it after every
# reboot; uninstall.sh reverses this when the module is removed.
appops set "$PACKAGE" android:write_settings allow >/dev/null 2>&1
pm grant "$PACKAGE" android.permission.ACCESS_FINE_LOCATION >/dev/null 2>&1
pm grant "$PACKAGE" android.permission.CAMERA >/dev/null 2>&1
settings put secure location_mode 3 >/dev/null 2>&1
cmd package set-home-activity --user 0 "$ACTIVITY" >/dev/null 2>&1
pm disable-user --user 0 com.amazon.ds2.oobe.efd >/dev/null 2>&1

# Put Tater on screen before the first privacy pass. Disabling the stock Amazon
# packages can take several minutes on this hardware; it must not leave a fresh
# install apparently stuck on the Fire OS launcher while setup is starting.
am start --user 0 -n "$ACTIVITY" >/dev/null 2>&1
screen_watchdog &
SCREEN_WATCHDOG_PID=$!
service_log "screen watchdog pid=$SCREEN_WATCHDOG_PID"

if [ -x /data/adb/modules/tater_checkers/privacy.sh ]; then
    (
        # Background shells inherit the parent's TERM trap under Fire OS mksh.
        # Keep this child from clearing the live supervisor PID when it exits.
        trap 'exit 0' TERM INT
        if /system/bin/sh /data/adb/modules/tater_checkers/privacy.sh apply; then
            service_log "privacy policy applied"
        else
            service_log "privacy policy reported an error; continuing with Tater"
        fi
    ) &
    PRIVACY_PID=$!
    service_log "privacy policy pid=$PRIVACY_PID"
fi

fast_exits=0
while true; do
    started=$(date +%s)
    if grep -q '"url"' "$CONF" 2>/dev/null; then
        mode=native
        "$BIN" >> /data/local/tmp/tater-server.log 2>&1 &
        server_pid=$!
        sleep 2
        am force-stop "$PACKAGE" >/dev/null 2>&1
        am start -n "$ACTIVITY" >/dev/null 2>&1
    else
        mode=setup
        "$BIN" setup-mode >> /data/local/tmp/tater-server.log 2>&1 &
        server_pid=$!
    fi
    service_log "start mode=$mode pid=$server_pid slot=$(readlink "$BIN" 2>/dev/null)"

    # A Checkers OTA is healthy only when the new native daemon has reached
    # Tater and the matching-version APK has connected over the loopback
    # screen protocol. Until then both old components remain recoverable.
    if [ "$mode" = native ] && [ -f "$OTA_PENDING" ]; then
        expected=$(ota_value version)
        healthy=0
        waited=0
        while kill -0 "$server_pid" >/dev/null 2>&1 && [ $waited -lt 90 ]; do
            if [ -f "$OTA_HEALTHY" ] && [ "$(tr -d '\r\n' < "$OTA_HEALTHY")" = "$expected" ]; then
                healthy=1
                break
            fi
            sleep 1
            waited=$((waited + 1))
        done
        if [ $healthy -eq 1 ]; then
            rollback_apk=$(ota_value rollback_apk)
            staged_apk=$(ota_value staged_apk)
            rm -f "$OTA_PENDING" "$OTA_HEALTHY" "$rollback_apk" "$staged_apk"
            service_log "OTA generation $expected committed after native+screen health"
        else
            service_log "OTA generation $expected failed coordinated health after ${waited}s"
            kill "$server_pid" >/dev/null 2>&1
            wait "$server_pid" >/dev/null 2>&1
            rollback_pending_ota
            sleep 2
            continue
        fi
    fi

    wait $server_pid
    status=$?
    ended=$(date +%s)
    runtime=$((ended - started))
    service_log "exit mode=$mode pid=$server_pid status=$status runtime=${runtime}s"

    if [ $runtime -ge $MIN_RUNTIME ]; then
        fast_exits=0
        sleep 2
        continue
    fi
    fast_exits=$((fast_exits + 1))
    if [ $fast_exits -lt $MAX_FAST_EXITS ]; then
        sleep 3
        continue
    fi

    current=$(readlink "$BIN" 2>/dev/null)
    case "$current" in
        server_a) fallback=server_b ;;
        server_b) fallback=server_a ;;
        *)
            service_log "cannot roll back unknown slot=$current"
            exit 1
            ;;
    esac
    if [ ! -x "/data/local/bin/$fallback" ]; then
        service_log "cannot roll back missing slot=$fallback"
        exit 1
    fi
    ln -sf "$fallback" "$BIN"
    service_log "rolled back $current -> $fallback after $MAX_FAST_EXITS fast exits"
    fast_exits=0
    sleep 2
done
