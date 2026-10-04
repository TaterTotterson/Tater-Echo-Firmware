#!/bin/sh
# Rook's Broadcom virtual ap0 accepts DHCP but stops transmitting shortly
# afterward. Use the primary wlan0 in AP mode instead; this was verified with
# an iPhone loading the setup form and does not affect Checkers' ap0 path.
set -eu
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH

[ ! -s /data/local/etc/tater/native.json ] || exit 0
if [ -s /run/tater-setup-ssid ] &&
   [ -s /run/tater-hostapd.pid ] && [ -s /run/tater-dnsmasq.pid ] &&
   kill -0 "$(cat /run/tater-hostapd.pid)" 2>/dev/null &&
   kill -0 "$(cat /run/tater-dnsmasq.pid)" 2>/dev/null; then
    exit 0
fi

# TECHO5's boot script starts wpa_supplicant even before first pairing. It
# must release wlan0 before the driver can switch from managed to AP mode.
for pid in $(pidof wpa_supplicant 2>/dev/null || true); do
    kill "$pid" 2>/dev/null || true
done
if ! iw dev wlan0 info | grep -q 'type AP'; then
    ip link set wlan0 down
    iw dev wlan0 set type __ap
fi

# A down/up cycle resets this USB-backed Broadcom device. Its first up may
# return EBUSY while the driver reprobes; retry rather than reporting a false
# setup failure. If it never recovers, the USB provisioning path stays open.
attempt=0
until ip link set wlan0 up 2>/dev/null; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 20 ]; then
        echo "Rook Wi-Fi did not return after switching to AP mode; use USB provisioning" >&2
        exit 1
    fi
    sleep 1
done

TATER_SETUP_AP_IFACE=wlan0 TATER_SETUP_DHCP_BROADCAST=1 /usr/local/sbin/tater-setup-ap-base
