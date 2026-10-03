#!/bin/sh
# First-boot Checkers hotspot. The vendor mt76x8 driver pre-creates ap0;
# trying to turn wlan0 into an AP fails even though iw advertises AP mode.
set -eu

PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
HOSTAPD=${TATER_HOSTAPD:-/usr/sbin/hostapd}
DNSMASQ=${TATER_DNSMASQ:-/usr/sbin/dnsmasq}
RUN_DIR=${TATER_SETUP_RUN_DIR:-/run}

[ ! -s /data/local/etc/tater/native.json ] || exit 0
[ -x "$HOSTAPD" ] && [ -x "$DNSMASQ" ] || exit 1
if [ -s "$RUN_DIR/tater-setup-ssid" ] && \
   [ -s "$RUN_DIR/tater-hostapd.pid" ] && [ -s "$RUN_DIR/tater-dnsmasq.pid" ] && \
   kill -0 "$(cat "$RUN_DIR/tater-hostapd.pid")" 2>/dev/null && \
   kill -0 "$(cat "$RUN_DIR/tater-dnsmasq.pid")" 2>/dev/null; then
    exit 0
fi

n=0
until ip link show ap0 >/dev/null 2>&1; do
    n=$((n+1))
    [ "$n" -lt 10 ] || exit 1
    sleep 1
done

mkdir -p "$RUN_DIR/techo5"
mkdir -p "$RUN_DIR/tater-dnsmasq"
chown nobody:nobody "$RUN_DIR/tater-dnsmasq"

suffix=$(cat /sys/class/net/ap0/address | tr -d ':' | tail -c 5 | tr 'a-f' 'A-F')
case "$suffix" in
    [0-9A-F][0-9A-F][0-9A-F][0-9A-F]) ;;
    *) exit 1 ;;
esac
ssid="Tater-Setup-$suffix"
# When a TWRP install carried over a saved 2.4 GHz station connection,
# this shared radio must advertise the AP on that same channel.
station_freq=$(iw dev wlan0 link 2>/dev/null | sed -n 's/^[[:space:]]*freq: \([0-9]*\).*/\1/p' | head -1)
channel=6
if [ -n "$station_freq" ] && [ "$station_freq" -ge 2412 ] && [ "$station_freq" -le 2472 ]; then
    channel=$(((station_freq - 2407) / 5))
fi

cleanup() {
    for process in tater-dnsmasq tater-hostapd; do
        if [ -s "$RUN_DIR/$process.pid" ]; then
            kill "$(cat "$RUN_DIR/$process.pid")" 2>/dev/null || true
            rm -f "$RUN_DIR/$process.pid"
        fi
    done
    ip addr del 192.168.4.1/24 dev ap0 2>/dev/null || true
    rm -f "$RUN_DIR/tater-setup-ssid" "$RUN_DIR/techo5/wifi-setup"
}
trap cleanup EXIT

# ap0 is isolated from the station LAN. Only the captive page and its local
# DHCP/DNS are reachable from setup clients; forwarding is forbidden.
iptables-legacy -N TATER-SETUP-IN 2>/dev/null || true
iptables-legacy -F TATER-SETUP-IN
iptables-legacy -A TATER-SETUP-IN -p udp --dport 67 -j ACCEPT
iptables-legacy -A TATER-SETUP-IN -p udp --dport 53 -j ACCEPT
iptables-legacy -A TATER-SETUP-IN -p tcp --dport 53 -j ACCEPT
iptables-legacy -A TATER-SETUP-IN -p tcp --dport 80 -j ACCEPT
iptables-legacy -A TATER-SETUP-IN -p icmp -j ACCEPT
iptables-legacy -A TATER-SETUP-IN -j DROP
iptables-legacy -C INPUT -i ap0 -j TATER-SETUP-IN 2>/dev/null || iptables-legacy -I INPUT 1 -i ap0 -j TATER-SETUP-IN
iptables-legacy -C FORWARD -i ap0 -j DROP 2>/dev/null || iptables-legacy -I FORWARD 1 -i ap0 -j DROP
ip6tables-legacy -C INPUT -i ap0 -j DROP 2>/dev/null || ip6tables-legacy -I INPUT 1 -i ap0 -j DROP
ip6tables-legacy -C FORWARD -i ap0 -j DROP 2>/dev/null || ip6tables-legacy -I FORWARD 1 -i ap0 -j DROP

(umask 077; printf 'interface=ap0\ndriver=nl80211\nssid=%s\nhw_mode=g\nchannel=%s\nauth_algs=1\n' \
    "$ssid" "$channel" > "$RUN_DIR/tater-hostapd.conf")
"$HOSTAPD" -B -P "$RUN_DIR/tater-hostapd.pid" "$RUN_DIR/tater-hostapd.conf" > "$RUN_DIR/tater-hostapd.log" 2>&1
ip addr add 192.168.4.1/24 dev ap0
"$DNSMASQ" --conf-file=/dev/null --interface=ap0 --bind-interfaces \
    --user=nobody --group=nobody \
    --dhcp-range=192.168.4.10,192.168.4.80,255.255.255.0,1h \
    --dhcp-option=option:router,192.168.4.1 \
    --dhcp-option=option:dns-server,192.168.4.1 \
    --address=/#/192.168.4.1 --no-resolv --no-hosts --dhcp-authoritative \
    --dhcp-leasefile="$RUN_DIR/tater-dnsmasq/leases" \
    --pid-file="$RUN_DIR/tater-dnsmasq.pid" --log-facility="$RUN_DIR/tater-dnsmasq/log"
kill -0 "$(cat "$RUN_DIR/tater-hostapd.pid")"
kill -0 "$(cat "$RUN_DIR/tater-dnsmasq.pid")"
printf '%s\n' "$ssid" > "$RUN_DIR/tater-setup-ssid"
touch "$RUN_DIR/techo5/wifi-setup"
trap - EXIT
echo "tater-setup-ap: $ssid ready at 192.168.4.1"
