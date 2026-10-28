# Tater Linux SSH access

Every Tater Linux satellite image includes a small Dropbear SSH service for
maintenance when USB serial is unavailable. SSH is off by default and can be
enabled independently for each supported Echo from its **Satellite Settings**
popup in Tater.

Tater shows the complete login next to the switch. The username is `root`; the
password is a different readable, randomly generated value for every satellite.
There is no fleet-wide default password. Saving the switch sends the credential
over the satellite's authenticated native connection. The satellite stores only
a salted SHA-512 password hash in `/data/tater-linux/ssh`, and the settings
acknowledgement never sends the password back.

The firmware root filesystem remains read-only. While SSH is enabled, the SSH
supervisor builds an ephemeral shadow file under `/run` and bind-mounts it over
`/etc/shadow`; disabling SSH or rebooting removes that runtime override. The
managed password never modifies the immutable root image.

SSH is independent of the network-selection policy. It listens on port 22 on
the device's available network interfaces, so the same login works over Wi-Fi
or USB Ethernet. If an active session uses Wi-Fi when Ethernet takes over, that
session will disconnect with the Wi-Fi interface; reconnect to the wired IP.

## Connect from Tater's satellite popup

1. Open the satellite in Tater and choose **Satellite Settings**.
2. Under **Remote Maintenance**, turn on **SSH Access**.
3. Copy the displayed login and password, then save the settings.
4. Connect from a terminal:

   ```sh
   ssh root@DEVICE_IP
   ```

The popup's login uses the address from the satellite's current native
connection. On networks where that address is not directly reachable, use the
router's DHCP lease for the satellite instead.

The image hostname is `tater-biscuit`, `tater-radar`, `tater-checkers`,
`tater-crown`, `tater-athena`, or `tater-rook`. A router may make that hostname
resolvable, but Tater Linux does not currently promise `.local` discovery, so
the current address is the reliable first test.

Changing the password in the popup safely restarts the listener. Re-saving an
unchanged password does not interrupt an active session. Turning SSH off stops
new connections; an already authenticated child session may remain until it is
closed.

## Optional public-key login

Password access is intended to make first provisioning and hardware tests easy.
For regular maintenance, a public key can also be added while logged in:

```sh
tater-ssh-authorize add 'ssh-ed25519 AAAA... your-computer'
tater-ssh-authorize status
```

The command accepts one public key on standard input. Never copy the private key
to the satellite. Provisioned keys are restricted from agent, X11, and port
forwarding. A public key never enables the listener by itself; the Tater switch
is the only control for whether SSH is running.

## Ethernet diagnostics over SSH

These commands cover the same evidence normally collected over USB serial:

```sh
cat /run/tater-usb-ethernet.state
tater-usb-inventory --include-logs
ip address
ip route
tail -f /data/tater-linux/sshd.log
```

`/run/tater-sshd.state` reports whether SSH is disabled or active and which
authentication mode is in use. `tater-ssh-control status` reports the managed
switch state. The listener is restricted to the dedicated `tater-ssh` login
group, whose only member is root. Local and remote port forwarding are disabled.
Authentication failures and server diagnostics go to
`/data/tater-linux/sshd.log`.

SSH is part of the Linux root filesystem. Installing only an application OTA
does not add it to an older image; use a factory or full Linux update that
contains this rootfs first.
