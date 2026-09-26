package com.tatertotterson.show;

import android.Manifest;
import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.view.Window;
import android.view.WindowManager;

public final class MainActivity extends Activity {
    private static final int CAMERA_PERMISSION_REQUEST = 4202;
    private TaterSocketClient link;
    private TaterShowView showView;
    private BlePresenceScanner bleScanner;
    private CameraSnapshotServer cameraServer;
    private final Handler demoHandler = new Handler(Looper.getMainLooper());
    private int demoStep;
    private final Runnable demoTick = new Runnable() {
        @Override public void run() {
            String[] phases = {"idle", "listening", "thinking", "tool_call", "speaking", "music", "intercom"};
            String phase = phases[demoStep % phases.length];
            String title = "music".equals(phase) ? "Tater Show preview" : "";
            String artist = "music".equals(phase) ? "Screen and touch test" : "";
            showView.setState(new ShowState(
                    phase, true, "Tater Show Preview", "Family Room", "",
                    false, 54, "speaking".equals(phase) ? .32f : .10f,
                    ("listening".equals(phase) || "speaking".equals(phase)) ? 92f : null,
                    demoStep % 3 == 0, title, artist,
                    new ShowState.Weather("74°", "F", "Partly cloudy", "partly",
                            "Feels like 72°", "cooler", "43%", "SE 8 mph", "Environment Core", false,
                            "70°", "39%", "0.2 in/hr", "2"),
                    null, 0L, 0, "",
                    "tool_call".equals(phase) ? "room_vision" : "",
                    "tool_call".equals(phase) ? "I’m taking a quick look now." : ""));
            demoStep++;
            demoHandler.postDelayed(this, 4200);
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        getWindow().addFlags(
                WindowManager.LayoutParams.FLAG_FULLSCREEN |
                WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON |
                WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS);
        enterImmersiveMode();

        showView = new TaterShowView(this);
        setContentView(showView);
        startCameraSnapshots();
        applyLaunchIntent(getIntent());
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        applyLaunchIntent(intent);
    }

    private void applyLaunchIntent(Intent intent) {
        stopActiveMode();
        if (intent == null) intent = new Intent();
        if (intent.getBooleanExtra("setup", false)) {
            String network = intent.getStringExtra("setup_ssid");
            if (network == null || network.trim().isEmpty()) {
                network = "Tater-Setup-Echo";
            }
            showView.setState(ShowState.setup(network));
            if (intent.getBooleanExtra("manage_hotspot", false)) {
                new HotspotController(this).start(network, error -> runOnUiThread(() -> {
                    if (error != null) showView.setSetupError(error);
                }));
            }

            // Setup state is still driven by the native service. Keeping this
            // link open lets the saved-page handoff replace the instructions
            // with a full-screen connecting animation before Wi-Fi changes.
            link = new TaterSocketClient(showView::setState);
            showView.setCommandSink(link::sendCommand);
            link.start();
        } else if (intent.getBooleanExtra("demo", false)) {
            demoHandler.post(demoTick);
        } else {
            showView.setState(ShowState.waiting());
            link = new TaterSocketClient(showView::setState);
            showView.setCommandSink(link::sendCommand);
            link.start();
            bleScanner = new BlePresenceScanner(this, link::sendBleAdvertisements);
            bleScanner.start();
        }
    }

    private void stopActiveMode() {
        demoHandler.removeCallbacksAndMessages(null);
        if (bleScanner != null) {
            bleScanner.close();
            bleScanner = null;
        }
        if (link != null) {
            link.close();
            link = null;
        }
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) enterImmersiveMode();
    }

    private void enterImmersiveMode() {
        getWindow().getDecorView().setSystemUiVisibility(
                View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY |
                View.SYSTEM_UI_FLAG_FULLSCREEN |
                View.SYSTEM_UI_FLAG_HIDE_NAVIGATION |
                View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN |
                View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION |
                View.SYSTEM_UI_FLAG_LAYOUT_STABLE);
    }

    private void startCameraSnapshots() {
        if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.CAMERA}, CAMERA_PERMISSION_REQUEST);
            return;
        }
        if (cameraServer == null) {
            cameraServer = new CameraSnapshotServer(this);
            cameraServer.start();
        }
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == CAMERA_PERMISSION_REQUEST
                && grantResults.length > 0
                && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
            startCameraSnapshots();
        }
    }

    @Override
    protected void onDestroy() {
        stopActiveMode();
        if (cameraServer != null) {
            cameraServer.close();
            cameraServer = null;
        }
        super.onDestroy();
    }
}
