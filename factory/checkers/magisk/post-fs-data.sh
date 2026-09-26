#!/system/bin/sh
# Restore the validated Amazon LAN-only firewall before Android applications
# and system_server can establish public connections.

PRIVACY=/data/adb/modules/tater_checkers/privacy.sh
LOG=/data/local/etc/tater/privacy.log

if [ ! -x "$PRIVACY" ]; then
    echo "$(date +%s) early-firewall privacy script missing" >> "$LOG"
    exit 1
fi

if "$PRIVACY" firewall-cached; then
    echo "$(date +%s) early-firewall applied" >> "$LOG"
    exit 0
fi

echo "$(date +%s) early-firewall failed" >> "$LOG"
exit 1
