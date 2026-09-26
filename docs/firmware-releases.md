# Firmware release and OTA contract

## Versioning

Tater Echo firmware uses annotated `vMAJOR.MINOR.PATCH` tags. The exact tag is
compiled into the userspace binary and copied into `firmware-manifest.json`.
Other product namespaces inherited from EchoMuse (`emos-v*`, `controller-v*`)
do not trigger a Tater Echo firmware release.

## Target identity

Every native `hello` reports `firmware_target`, alongside the existing Echo
hardware identity. Tater uses that target key with `targets/targets.json`;
current keys are `biscuit` and `checkers`. A future model receives its own
target and factory installer even when its userspace binary can be shared.

## Factory artifact

The factory archive is for a device already unlocked with the target's listed
amonet version and currently booted into TWRP. It includes Tater userspace,
microWakeWord runtime/model, the emOS init and networking tools, the image
builder, installer, licenses, and corresponding BusyBox source.

It does not include a redistributable boot image. The device's kernel, device
trees, boot addresses, and MTK wrapper are read from that device's stock boot
partition. The installer then creates and verifies the final image locally.

## OTA artifacts

Biscuit's OTA asset is the ARM userspace ELF. Checkers uses a deterministic ZIP
containing the ARM daemon, signed screen APK, and an inner manifest with the
version, sizes, and SHA-256 hashes. Tater sends the same target-independent
command envelope for either format:

```json
{
  "type": "ota.url",
  "url": "https://…/tater-echo-biscuit-v0.1.0-ota.bin",
  "sha256": "<64 lowercase hex characters>",
  "size_bytes": 16410287
}
```

The actual command envelope includes the command/request identifiers used by
the Tater native protocol; this is the command payload relevant to release
selection. The device rejects a missing/invalid digest, a size mismatch, a
non-ELF download, an HTTP error, or an artifact over 128 MiB.

On Biscuit, success writes the inactive userspace slot and atomically switches
`/data/local/bin/server`; the supervisor switches back after three fast startup
failures. On Checkers, the installer also backs up and upgrades the APK. The
supervisor commits only when the new daemon connects to Tater and a
matching-version APK reports ready over loopback, otherwise it restores both.

## Manifest

`firmware-manifest.json` is the API consumed by Tater. For every target it
records:

- unlock compatibility and support status;
- whether factory and OTA are available;
- the exact asset names;
- byte sizes; and
- SHA-256 hashes.

Tater should use the manifest rather than infer filenames. This lets future
Echo models use different factory formats without changing the OTA UI.
