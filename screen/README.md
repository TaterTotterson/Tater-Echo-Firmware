# Tater Show APK

`com.tatertotterson.show` is the full-screen renderer for screen-equipped Echo
targets. It is intentionally small: one Activity, one custom-drawn View, and a
loopback client. Wake word, microphones, AEC, playback, media synchronization,
and Tater connectivity remain in the native firmware process. The screen adds
a bottom-center press-and-hold intercom control and uses Android's BLE stack to
send bounded presence-advertisement batches to the loopback daemon.

For explicit Room Vision requests, the APK also exposes a single-shot front
camera endpoint to the native daemon on IPv4 loopback only
(`127.0.0.1:43823/snapshot`). The endpoint is not reachable from the LAN, does
not stream video, and does not save captured frames to storage. The native
daemon forwards the bounded in-memory JPEG to Tater only after receiving a
correlated `camera.snapshot` request.

## Build

```bash
ANDROID_HOME=/path/to/android-sdk \
TATER_SHOW_VERSION_NAME=v0.2.0-dev \
TATER_SHOW_VERSION_CODE=2 \
./gradlew -p screen :app:testDebugUnitTest :app:assembleDebug
```

The APK is written to `screen/app/build/outputs/apk/debug/app-debug.apk`.
Android 7.1 / API 25 is the minimum runtime so the UI and read-only profiler
can be tested while Checkers is still running stock Fire OS 6.

Tagged release builds use `assembleRelease` and require
`TATER_SHOW_KEYSTORE_PATH`, `TATER_SHOW_KEY_ALIAS`,
`TATER_SHOW_KEY_PASSWORD`, and `TATER_SHOW_STORE_PASSWORD`. Keep this signing
identity stable: Android will reject an OTA APK signed with a different key.

## Run the visual demo

```bash
adb install --no-streaming -r screen/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -W -n com.tatertotterson.show/.MainActivity --ez demo true
```

Demo mode cycles idle, listening, thinking, tool-call, speaking, music, and
intercom. The
factory bundle exposes the same mode as `./install.sh --demo`.

Without the demo extra, the app connects to the native daemon at
`127.0.0.1:43821`. The versioned protocol and bring-up sequence are documented
in [`docs/checkers.md`](../docs/checkers.md).
