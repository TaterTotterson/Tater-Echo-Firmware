package com.tatertotterson.show;

import android.Manifest;
import android.annotation.SuppressLint;
import android.app.Activity;
import android.bluetooth.BluetoothAdapter;
import android.bluetooth.BluetoothManager;
import android.bluetooth.le.BluetoothLeScanner;
import android.bluetooth.le.ScanCallback;
import android.bluetooth.le.ScanResult;
import android.bluetooth.le.ScanSettings;
import android.content.Context;
import android.content.pm.PackageManager;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Low-duty Android BLE scanner used by Checkers' room-presence receiver. */
@SuppressLint("MissingPermission")
final class BlePresenceScanner implements AutoCloseable {
    interface Sink {
        void send(JSONArray adverts, long seen, int unique, boolean scanning, String error);
    }

    private static final int LOCATION_REQUEST = 4205;
    private static final long FLUSH_MS = 750L;
    private static final long UNIQUE_WINDOW_MS = 5L * 60L * 1000L;
    private static final int MAX_BATCH = 32;

    private final Activity activity;
    private final Sink sink;
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Object lock = new Object();
    private final Map<String, JSONObject> pending = new LinkedHashMap<>();
    private final Map<String, Long> unique = new LinkedHashMap<>();
    private BluetoothAdapter adapter;
    private BluetoothLeScanner scanner;
    private boolean scanning;
    private boolean closed;
    private long seen;

    private final Runnable flush = new Runnable() {
        @Override public void run() {
            flushNow();
            if (!closed) handler.postDelayed(this, FLUSH_MS);
        }
    };

    private final ScanCallback callback = new ScanCallback() {
        @Override public void onScanResult(int callbackType, ScanResult result) {
            if (result == null || result.getDevice() == null || result.getScanRecord() == null) return;
            byte[] bytes = result.getScanRecord().getBytes();
            String address = result.getDevice().getAddress();
            if (address == null || bytes == null || bytes.length == 0) return;
            try {
                JSONObject row = new JSONObject()
                        .put("address", address.toLowerCase(Locale.US))
                        .put("address_type", 0)
                        .put("event_type", 0)
                        .put("rssi", result.getRssi())
                        .put("data", hex(bytes));
                synchronized (lock) {
                    seen++;
                    unique.put(address.toLowerCase(Locale.US), SystemClock.elapsedRealtime());
                    pending.put(address + ':' + row.optString("data"), row);
                    while (pending.size() > MAX_BATCH) {
                        String first = pending.keySet().iterator().next();
                        pending.remove(first);
                    }
                }
            } catch (Exception ignored) {
                // A malformed platform result is skipped; scanning continues.
            }
        }

        @Override public void onBatchScanResults(List<ScanResult> results) {
            if (results == null) return;
            for (ScanResult result : results) onScanResult(0, result);
        }

        @Override public void onScanFailed(int errorCode) {
            scanning = false;
            sink.send(new JSONArray(), seen, uniqueSize(), false, "Android BLE scan failed: " + errorCode);
        }
    };

    BlePresenceScanner(Activity activity, Sink sink) {
        this.activity = activity;
        this.sink = sink;
    }

    void start() {
        if (closed) return;
        if (android.os.Build.VERSION.SDK_INT >= 23
                && activity.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION)
                != PackageManager.PERMISSION_GRANTED) {
            activity.requestPermissions(new String[]{Manifest.permission.ACCESS_FINE_LOCATION}, LOCATION_REQUEST);
            handler.postDelayed(this::startScanner, 1500L);
            return;
        }
        startScanner();
    }

    private void startScanner() {
        if (closed || scanning) return;
        BluetoothManager manager = (BluetoothManager) activity.getSystemService(Context.BLUETOOTH_SERVICE);
        adapter = manager == null ? null : manager.getAdapter();
        if (adapter == null) {
            sink.send(new JSONArray(), seen, uniqueSize(), false, "Bluetooth is unavailable");
            return;
        }
        if (!adapter.isEnabled()) adapter.enable();
        scanner = adapter.getBluetoothLeScanner();
        if (scanner == null) {
            sink.send(new JSONArray(), seen, uniqueSize(), false, "BLE scanner is unavailable");
            return;
        }
        ScanSettings settings = new ScanSettings.Builder()
                .setScanMode(ScanSettings.SCAN_MODE_LOW_POWER)
                .setReportDelay(0L)
                .build();
        scanner.startScan(null, settings, callback);
        scanning = true;
        sink.send(new JSONArray(), seen, uniqueSize(), true, "");
        handler.removeCallbacks(flush);
        handler.postDelayed(flush, FLUSH_MS);
    }

    private int uniqueSize() {
        synchronized (lock) {
            pruneUniqueLocked(SystemClock.elapsedRealtime());
            return unique.size();
        }
    }

    private void flushNow() {
        List<JSONObject> rows;
        long total;
        int uniqueCount;
        synchronized (lock) {
            pruneUniqueLocked(SystemClock.elapsedRealtime());
            rows = new ArrayList<>(pending.values());
            pending.clear();
            total = seen;
            uniqueCount = unique.size();
        }
        if (rows.isEmpty()) return;
        JSONArray array = new JSONArray();
        for (JSONObject row : rows) array.put(row);
        sink.send(array, total, uniqueCount, scanning, "");
    }

    private void pruneUniqueLocked(long now) {
        unique.entrySet().removeIf(entry -> now - entry.getValue() > UNIQUE_WINDOW_MS);
    }

    private static String hex(byte[] value) {
        StringBuilder out = new StringBuilder(value.length * 2);
        for (byte item : value) out.append(String.format(Locale.US, "%02x", item & 0xff));
        return out.toString();
    }

    @Override public void close() {
        closed = true;
        handler.removeCallbacksAndMessages(null);
        if (scanner != null && scanning) scanner.stopScan(callback);
        scanning = false;
        flushNow();
    }
}
