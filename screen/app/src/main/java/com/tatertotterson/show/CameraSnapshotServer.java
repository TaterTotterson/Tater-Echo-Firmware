package com.tatertotterson.show;

import android.app.Activity;
import android.graphics.ImageFormat;
import android.graphics.SurfaceTexture;
import android.hardware.Camera;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;

import java.io.BufferedReader;
import java.io.BufferedWriter;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.io.OutputStreamWriter;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/** Loopback-only, on-demand front-camera snapshots for Tater Room Vision. */
final class CameraSnapshotServer implements AutoCloseable {
    private static final String TAG = "TaterShowCamera";
    private static final int PORT = 43823;
    private static final int MAX_JPEG_BYTES = 4 * 1024 * 1024;
    private static final long CAPTURE_TIMEOUT_SECONDS = 7L;

    private final Handler main = new Handler(Looper.getMainLooper());
    private final AtomicBoolean running = new AtomicBoolean(false);
    private final AtomicReference<Camera> activeCamera = new AtomicReference<>();
    private Thread worker;
    private volatile ServerSocket serverSocket;

    CameraSnapshotServer(Activity activity) {
        // The Activity owns the server lifecycle and runtime camera permission.
    }

    void start() {
        if (!running.compareAndSet(false, true)) return;
        worker = new Thread(this::serve, "tater-show-camera");
        worker.setDaemon(true);
        worker.start();
    }

    private void serve() {
        // Android 7 resolves getLoopbackAddress() to ::1 on Checkers, while
        // the native daemon deliberately calls 127.0.0.1. Bind the exact IPv4
        // loopback address so the service remains local and both sides agree.
        try (ServerSocket server = new ServerSocket(PORT, 2, InetAddress.getByName("127.0.0.1"))) {
            serverSocket = server;
            while (running.get()) {
                try (Socket socket = server.accept()) {
                    socket.setSoTimeout(9000);
                    handle(socket);
                } catch (Exception error) {
                    if (running.get()) Log.w(TAG, "Snapshot request failed", error);
                }
            }
        } catch (Exception error) {
            if (running.get()) Log.e(TAG, "Camera snapshot server stopped", error);
        } finally {
            serverSocket = null;
        }
    }

    private void handle(Socket socket) throws Exception {
        BufferedReader reader = new BufferedReader(new InputStreamReader(
                socket.getInputStream(), StandardCharsets.US_ASCII));
        String request = reader.readLine();
        String line;
        do {
            line = reader.readLine();
        } while (line != null && !line.isEmpty());
        if (request == null || !request.startsWith("GET /snapshot ")) {
            writeError(socket, 404, "snapshot endpoint not found");
            return;
        }
        try {
            byte[] jpeg = capture();
            if (jpeg.length == 0 || jpeg.length > MAX_JPEG_BYTES) {
                throw new IllegalStateException("camera returned an invalid image size");
            }
            OutputStream output = socket.getOutputStream();
            String headers = "HTTP/1.1 200 OK\r\n"
                    + "Content-Type: image/jpeg\r\n"
                    + "Content-Length: " + jpeg.length + "\r\n"
                    + "Cache-Control: no-store, max-age=0\r\n"
                    + "Connection: close\r\n\r\n";
            output.write(headers.getBytes(StandardCharsets.US_ASCII));
            output.write(jpeg);
            output.flush();
        } catch (Exception error) {
            writeError(socket, 503, error.getMessage() == null ? "camera unavailable" : error.getMessage());
        }
    }

    private byte[] capture() throws Exception {
        CompletableFuture<byte[]> result = new CompletableFuture<>();
        main.post(() -> openAndCapture(result));
        try {
            return result.get(CAPTURE_TIMEOUT_SECONDS, TimeUnit.SECONDS);
        } catch (Exception error) {
            main.post(this::releaseActiveCamera);
            throw error;
        }
    }

    private void openAndCapture(CompletableFuture<byte[]> result) {
        Camera camera = null;
        SurfaceTexture preview = null;
        try {
            int cameraId = frontCameraId();
            if (cameraId < 0) throw new IllegalStateException("front camera not found");
            camera = Camera.open(cameraId);
            if (!activeCamera.compareAndSet(null, camera)) {
                camera.release();
                throw new IllegalStateException("camera is already capturing");
            }

            Camera.Parameters parameters = camera.getParameters();
            parameters.setPictureFormat(ImageFormat.JPEG);
            parameters.setJpegQuality(86);
            Camera.Size selected = selectPictureSize(parameters.getSupportedPictureSizes());
            if (selected != null) parameters.setPictureSize(selected.width, selected.height);
            List<String> focusModes = parameters.getSupportedFocusModes();
            if (focusModes != null && focusModes.contains(Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE)) {
                parameters.setFocusMode(Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE);
            }
            Camera.CameraInfo info = new Camera.CameraInfo();
            Camera.getCameraInfo(cameraId, info);
            parameters.setRotation(info.orientation);
            camera.setParameters(parameters);

            preview = new SurfaceTexture(11);
            camera.setPreviewTexture(preview);
            camera.startPreview();
            Camera captureCamera = camera;
            SurfaceTexture capturePreview = preview;
            main.postDelayed(() -> takePicture(captureCamera, capturePreview, result), 450L);
        } catch (Exception error) {
            if (preview != null) preview.release();
            if (camera != null && activeCamera.compareAndSet(camera, null)) camera.release();
            result.completeExceptionally(error);
        }
    }

    private void takePicture(Camera camera, SurfaceTexture preview, CompletableFuture<byte[]> result) {
        try {
            camera.takePicture(null, null, (jpeg, completedCamera) -> {
                try {
                    if (jpeg == null || jpeg.length == 0) {
                        result.completeExceptionally(new IllegalStateException("camera returned no JPEG data"));
                    } else if (jpeg.length > MAX_JPEG_BYTES) {
                        result.completeExceptionally(new IllegalStateException("camera image is too large"));
                    } else {
                        result.complete(jpeg);
                    }
                } finally {
                    preview.release();
                    if (activeCamera.compareAndSet(completedCamera, null)) completedCamera.release();
                }
            });
        } catch (Exception error) {
            preview.release();
            if (activeCamera.compareAndSet(camera, null)) camera.release();
            result.completeExceptionally(error);
        }
    }

    private static int frontCameraId() {
        Camera.CameraInfo info = new Camera.CameraInfo();
        for (int id = 0; id < Camera.getNumberOfCameras(); id++) {
            Camera.getCameraInfo(id, info);
            if (info.facing == Camera.CameraInfo.CAMERA_FACING_FRONT) return id;
        }
        return Camera.getNumberOfCameras() > 0 ? 0 : -1;
    }

    private static Camera.Size selectPictureSize(List<Camera.Size> sizes) {
        if (sizes == null || sizes.isEmpty()) return null;
        Camera.Size best = null;
        long bestArea = -1L;
        for (Camera.Size size : sizes) {
            long area = (long) size.width * size.height;
            if (area <= 1920L * 1080L && area > bestArea) {
                best = size;
                bestArea = area;
            }
        }
        if (best != null) return best;
        for (Camera.Size size : sizes) {
            long area = (long) size.width * size.height;
            if (best == null || area < bestArea) {
                best = size;
                bestArea = area;
            }
        }
        return best;
    }

    private static void writeError(Socket socket, int status, String message) throws Exception {
        String safe = String.valueOf(message == null ? "camera unavailable" : message)
                .replace('\r', ' ').replace('\n', ' ');
        byte[] body = safe.getBytes(StandardCharsets.UTF_8);
        BufferedWriter writer = new BufferedWriter(new OutputStreamWriter(
                socket.getOutputStream(), StandardCharsets.US_ASCII));
        writer.write(String.format(Locale.US,
                "HTTP/1.1 %d Error\r\nContent-Type: text/plain; charset=utf-8\r\n"
                        + "Content-Length: %d\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n",
                status, body.length));
        writer.flush();
        socket.getOutputStream().write(body);
        socket.getOutputStream().flush();
    }

    private void releaseActiveCamera() {
        Camera camera = activeCamera.getAndSet(null);
        if (camera != null) {
            try {
                camera.stopPreview();
            } catch (Exception ignored) {
            }
            camera.release();
        }
    }

    @Override
    public void close() {
        running.set(false);
        try {
            if (serverSocket != null) serverSocket.close();
        } catch (Exception ignored) {
        }
        main.post(this::releaseActiveCamera);
        if (worker != null) worker.interrupt();
    }
}
