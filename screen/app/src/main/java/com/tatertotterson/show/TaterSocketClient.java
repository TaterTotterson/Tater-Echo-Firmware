package com.tatertotterson.show;

import android.os.Handler;
import android.os.Looper;
import android.util.Log;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.BufferedWriter;
import java.io.InputStreamReader;
import java.io.OutputStreamWriter;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicBoolean;

final class TaterSocketClient implements AutoCloseable {
    interface Listener {
        void onState(ShowState state);
    }

    private static final String TAG = "TaterShowLink";
    private static final String HOST = "127.0.0.1";
    private static final int PORT = 43821;
    private static final int MAX_LINE_CHARS = 64 * 1024;

    private final Listener listener;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final AtomicBoolean running = new AtomicBoolean(false);
    private final Object writerLock = new Object();
    private volatile BufferedWriter writer;
    private volatile ShowState lastState = ShowState.waiting();
    private Thread worker;

    TaterSocketClient(Listener listener) {
        this.listener = listener;
    }

    void start() {
        if (!running.compareAndSet(false, true)) return;
        worker = new Thread(this::runLoop, "tater-show-link");
        worker.start();
    }

    void sendCommand(String action) {
        sendCommand(action, null);
    }

    void sendCommand(String action, Integer value) {
        try {
            JSONObject command = new JSONObject()
                    .put("protocol", ShowState.PROTOCOL_VERSION)
                    .put("type", "command")
                    .put("action", action);
            if (value != null) command.put("value", value);
            synchronized (writerLock) {
                if (writer == null) return;
                writer.write(command.toString());
                writer.newLine();
                writer.flush();
            }
        } catch (Exception error) {
            Log.w(TAG, "Unable to send screen command", error);
        }
    }

    private void runLoop() {
        while (running.get()) {
            try (Socket socket = new Socket()) {
                socket.connect(new InetSocketAddress(HOST, PORT), 1200);
                socket.setTcpNoDelay(true);
                BufferedWriter nextWriter = new BufferedWriter(new OutputStreamWriter(
                        socket.getOutputStream(), StandardCharsets.UTF_8));
                synchronized (writerLock) {
                    writer = nextWriter;
                }
                sendCommand("screen.ready");
                try (BufferedReader reader = new BufferedReader(new InputStreamReader(
                        socket.getInputStream(), StandardCharsets.UTF_8))) {
                    String line;
                    while (running.get() && (line = reader.readLine()) != null) {
                        if (line.length() > MAX_LINE_CHARS) throw new IllegalArgumentException("oversized state message");
                        ShowState state = ShowState.fromJson(line);
                        lastState = state;
                        main.post(() -> listener.onState(state));
                    }
                }
            } catch (Exception error) {
                if (running.get()) {
                    ShowState disconnected = lastState.disconnected();
                    main.post(() -> listener.onState(disconnected));
                    try {
                        Thread.sleep(1500);
                    } catch (InterruptedException ignored) {
                        Thread.currentThread().interrupt();
                    }
                }
            } finally {
                synchronized (writerLock) {
                    writer = null;
                }
            }
        }
    }

    @Override
    public void close() {
        running.set(false);
        synchronized (writerLock) {
            try {
                if (writer != null) writer.close();
            } catch (Exception ignored) {
            }
            writer = null;
        }
        if (worker != null) worker.interrupt();
    }
}
