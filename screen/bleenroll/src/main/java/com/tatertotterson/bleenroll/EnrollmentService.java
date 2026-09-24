package com.tatertotterson.bleenroll;

import android.annotation.SuppressLint;
import android.app.Service;
import android.bluetooth.BluetoothAdapter;
import android.bluetooth.BluetoothDevice;
import android.bluetooth.BluetoothGattCharacteristic;
import android.bluetooth.BluetoothGattServer;
import android.bluetooth.BluetoothGattServerCallback;
import android.bluetooth.BluetoothGattService;
import android.bluetooth.BluetoothManager;
import android.bluetooth.BluetoothProfile;
import android.bluetooth.le.AdvertiseCallback;
import android.bluetooth.le.AdvertiseData;
import android.bluetooth.le.AdvertiseSettings;
import android.bluetooth.le.BluetoothLeAdvertiser;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.os.Handler;
import android.os.IBinder;
import android.os.ParcelUuid;
import android.util.Log;

import java.lang.reflect.Method;
import java.util.UUID;

// This root-installed helper targets the Echo's Android 7 / Fire OS Bluetooth
// stack. BLUETOOTH and BLUETOOTH_ADMIN are install-time permissions there; the
// Android 12 permission names are also declared for newer compatible images.
@SuppressLint("MissingPermission")
public final class EnrollmentService extends Service {
    private static final String TAG = "TaterBleEnroll";
    private static final UUID HEART_RATE_SERVICE =
            UUID.fromString("0000180d-0000-1000-8000-00805f9b34fb");
    private static final UUID HEART_RATE_MEASUREMENT =
            UUID.fromString("00002a37-0000-1000-8000-00805f9b34fb");
    private static final UUID BODY_SENSOR_LOCATION =
            UUID.fromString("00002a38-0000-1000-8000-00805f9b34fb");

    private final Handler handler = new Handler();
    private BluetoothAdapter adapter;
    private BluetoothGattServer gattServer;
    private BluetoothLeAdvertiser advertiser;
    private BluetoothDevice peer;
    private String previousName;
    private String displayName;
    private boolean receiverRegistered;

    private final AdvertiseCallback advertiseCallback = new AdvertiseCallback() {
        @Override
        public void onStartFailure(int errorCode) {
            Log.e(TAG, "Advertising failed: " + errorCode);
            stopSelf();
        }
    };

    private final BluetoothGattServerCallback gattCallback = new BluetoothGattServerCallback() {
        @Override
        public void onConnectionStateChange(BluetoothDevice device, int status, int newState) {
            if (status != 0 || newState != BluetoothProfile.STATE_CONNECTED) {
                return;
            }
            if (peer != null && !peer.getAddress().equals(device.getAddress())) {
                gattServer.cancelConnection(device);
                return;
            }
            peer = device;
            if (device.getBondState() != BluetoothDevice.BOND_BONDED) {
                device.createBond();
            }
        }

        @Override
        public void onCharacteristicReadRequest(
                BluetoothDevice device,
                int requestId,
                int offset,
                BluetoothGattCharacteristic characteristic
        ) {
            if (BODY_SENSOR_LOCATION.equals(characteristic.getUuid())) {
                gattServer.sendResponse(device, requestId, 0, offset, new byte[] {0});
            } else {
                gattServer.sendResponse(device, requestId, 2, offset, null);
            }
        }

        @Override
        public void onServiceAdded(int status, BluetoothGattService service) {
            if (status == 0 && HEART_RATE_SERVICE.equals(service.getUuid())) {
                startAdvertising();
            } else {
                stopSelf();
            }
        }
    };

    private final BroadcastReceiver stateReceiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            if (BluetoothAdapter.ACTION_STATE_CHANGED.equals(intent.getAction())
                    && intent.getIntExtra(BluetoothAdapter.EXTRA_STATE, -1) == BluetoothAdapter.STATE_ON) {
                openPeripheral();
            }
        }
    };

    @Override
    public void onCreate() {
        super.onCreate();
        BluetoothManager manager = (BluetoothManager) getSystemService(Context.BLUETOOTH_SERVICE);
        adapter = manager == null ? null : manager.getAdapter();
        IntentFilter filter = new IntentFilter(BluetoothAdapter.ACTION_STATE_CHANGED);
        registerReceiver(stateReceiver, filter);
        receiverRegistered = true;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String action = intent == null ? "" : intent.getStringExtra("action");
        if ("stop".equals(action)) {
            String address = intent.getStringExtra("address");
            if (address != null && BluetoothAdapter.checkBluetoothAddress(address) && adapter != null) {
                removeBond(adapter.getRemoteDevice(address));
            }
            stopSelf();
            return START_NOT_STICKY;
        }
        displayName = intent == null ? "Tater Enroll" : intent.getStringExtra("display_name");
        if (displayName == null || displayName.length() == 0 || displayName.length() > 28 || adapter == null) {
            stopSelf();
            return START_NOT_STICKY;
        }
        int timeout = intent.getIntExtra("timeout_s", 90);
        timeout = Math.max(30, Math.min(180, timeout));
        handler.postDelayed(this::stopSelf, timeout * 1000L);
        previousName = adapter.getName();
        adapter.setName(displayName);
        if (adapter.isEnabled()) {
            openPeripheral();
        } else {
            adapter.enable();
        }
        return START_NOT_STICKY;
    }

    private void openPeripheral() {
        if (gattServer != null || adapter == null || !adapter.isEnabled()) {
            return;
        }
        BluetoothManager manager = (BluetoothManager) getSystemService(Context.BLUETOOTH_SERVICE);
        gattServer = manager.openGattServer(this, gattCallback);
        if (gattServer == null) {
            stopSelf();
            return;
        }
        BluetoothGattService service = new BluetoothGattService(
                HEART_RATE_SERVICE,
                BluetoothGattService.SERVICE_TYPE_PRIMARY
        );
        service.addCharacteristic(new BluetoothGattCharacteristic(
                HEART_RATE_MEASUREMENT,
                BluetoothGattCharacteristic.PROPERTY_NOTIFY,
                BluetoothGattCharacteristic.PERMISSION_READ_ENCRYPTED
        ));
        service.addCharacteristic(new BluetoothGattCharacteristic(
                BODY_SENSOR_LOCATION,
                BluetoothGattCharacteristic.PROPERTY_READ,
                BluetoothGattCharacteristic.PERMISSION_READ
        ));
        gattServer.addService(service);
    }

    private void startAdvertising() {
        advertiser = adapter.getBluetoothLeAdvertiser();
        if (advertiser == null) {
            stopSelf();
            return;
        }
        AdvertiseSettings settings = new AdvertiseSettings.Builder()
                .setAdvertiseMode(AdvertiseSettings.ADVERTISE_MODE_LOW_LATENCY)
                .setConnectable(true)
                .setTimeout(0)
                .setTxPowerLevel(AdvertiseSettings.ADVERTISE_TX_POWER_MEDIUM)
                .build();
        AdvertiseData data = new AdvertiseData.Builder()
                .setIncludeDeviceName(true)
                .addServiceUuid(new ParcelUuid(HEART_RATE_SERVICE))
                .build();
        advertiser.startAdvertising(settings, data, advertiseCallback);
    }

    private static void removeBond(BluetoothDevice device) {
        try {
            Method method = BluetoothDevice.class.getMethod("removeBond");
            method.invoke(device);
        } catch (ReflectiveOperationException error) {
            Log.w(TAG, "Could not remove temporary bond", error);
        }
    }

    private void cleanup() {
        handler.removeCallbacksAndMessages(null);
        if (advertiser != null) {
            advertiser.stopAdvertising(advertiseCallback);
            advertiser = null;
        }
        if (gattServer != null) {
            if (peer != null) {
                gattServer.cancelConnection(peer);
            }
            gattServer.close();
            gattServer = null;
        }
        if (adapter != null && previousName != null && previousName.length() > 0) {
            adapter.setName(previousName);
        }
        if (receiverRegistered) {
            unregisterReceiver(stateReceiver);
            receiverRegistered = false;
        }
    }

    @Override
    public void onDestroy() {
        cleanup();
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
