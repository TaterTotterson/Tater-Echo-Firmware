package com.tatertotterson.show;

import android.content.Context;
import android.net.wifi.WifiConfiguration;
import android.net.wifi.WifiManager;
import android.util.Log;

import java.lang.reflect.Method;

/** Requests Fire OS's hostapd/DHCP lifecycle after native code selects ap0. */
final class HotspotController {
    interface Listener { void complete(String error); }

    private static final String TAG = "TaterSetupHotspot";
    private final WifiManager wifi;

    HotspotController(Context context) {
        wifi = (WifiManager) context.getApplicationContext().getSystemService(Context.WIFI_SERVICE);
    }

    void start(String network, Listener listener) {
        new Thread(() -> {
            String error = null;
            try {
                if (wifi == null) throw new IllegalStateException("Wi-Fi service unavailable");
                WifiConfiguration config = new WifiConfiguration();
                config.SSID = network;
                config.hiddenSSID = false;
                config.allowedKeyManagement.clear();
                config.allowedKeyManagement.set(WifiConfiguration.KeyMgmt.NONE);
                Method method = WifiManager.class.getMethod(
                        "setWifiApEnabled", WifiConfiguration.class, boolean.class);
                Object result = method.invoke(wifi, config, true);
                if (result instanceof Boolean && !((Boolean) result)) {
                    throw new IllegalStateException("Fire OS rejected setup hotspot");
                }
                Log.i(TAG, "requested setup hotspot " + network);
            } catch (Exception e) {
                Log.e(TAG, "could not start setup hotspot", e);
                Throwable cause = e.getCause() == null ? e : e.getCause();
                error = "Could not start the setup network: " + cause.getMessage();
            }
            listener.complete(error);
        }, "tater-hotspot").start();
    }
}
