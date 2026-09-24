package com.tatertotterson.show;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.view.Window;
import android.view.WindowManager;

public final class MainActivity extends Activity {
    private TaterSocketClient link;
    private TaterShowView showView;
    private final Handler demoHandler = new Handler(Looper.getMainLooper());
    private int demoStep;
    private final Runnable demoTick = new Runnable() {
        @Override public void run() {
            String[] phases = {"idle", "listening", "thinking", "speaking", "music", "intercom"};
            String phase = phases[demoStep % phases.length];
            String title = "music".equals(phase) ? "Tater Show preview" : "";
            String artist = "music".equals(phase) ? "Screen and touch test" : "";
            showView.setState(new ShowState(
                    phase, true, "Tater Show Preview", "Family Room", "",
                    false, 54, "speaking".equals(phase) ? .32f : .10f,
                    ("listening".equals(phase) || "speaking".equals(phase)) ? 92f : null,
                    demoStep % 3 == 0, title, artist));
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

        showView = new TaterShowView(this, new TaterShowView.Commands() {
            @Override public void muteToggle() { if (link != null) link.sendCommand("mute.toggle"); }
            @Override public void volumeDelta(int delta) { if (link != null) link.sendCommand("volume.delta", delta); }
            @Override public void intercomStart() { if (link != null) link.sendCommand("intercom.start"); }
            @Override public void intercomStop() { if (link != null) link.sendCommand("intercom.stop"); }
        });
        setContentView(showView);
        if (getIntent().getBooleanExtra("demo", false)) {
            demoHandler.post(demoTick);
        } else {
            link = new TaterSocketClient(showView::setState);
            link.start();
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

    @Override
    protected void onDestroy() {
        demoHandler.removeCallbacksAndMessages(null);
        if (link != null) link.close();
        super.onDestroy();
    }
}
