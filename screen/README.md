# Tater Show APK

`com.tatertotterson.show` is the full-screen renderer for screen-equipped Echo
targets. It is intentionally small: one Activity, one custom-drawn View, and a
loopback client. Wake word, microphones, AEC, playback, media synchronization,
and Tater connectivity remain in the native firmware process.

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

## Run the visual demo

```bash
adb install --no-streaming -r screen/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -W -n com.tatertotterson.show/.MainActivity --ez demo true
```

Demo mode cycles idle, listening, thinking, speaking, music, and intercom. The
factory bundle exposes the same mode as `./install.sh --demo`.

Without the demo extra, the app connects to the native daemon at
`127.0.0.1:43821`. The versioned protocol and bring-up sequence are documented
in [`docs/checkers.md`](../docs/checkers.md).
