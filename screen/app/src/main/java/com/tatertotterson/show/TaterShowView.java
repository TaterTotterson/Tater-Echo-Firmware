package com.tatertotterson.show;

import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.LinearGradient;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.RadialGradient;
import android.graphics.Rect;
import android.graphics.RectF;
import android.graphics.Shader;
import android.os.SystemClock;
import android.view.HapticFeedbackConstants;
import android.view.MotionEvent;
import android.view.View;

import java.text.SimpleDateFormat;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.Date;
import java.util.Locale;
import java.util.SimpleTimeZone;

final class TaterShowView extends View {
    interface CommandSink {
        void send(String action, Integer value);
    }

    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint stroke = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Path wave = new Path();
    private final Path bubble = new Path();
    private final float[] bubbleX = new float[32];
    private final float[] bubbleY = new float[32];
    private final SimpleDateFormat clockFormat = new SimpleDateFormat("h:mm", Locale.getDefault());
    private final SimpleDateFormat dateFormat = new SimpleDateFormat("EEEE, MMMM d", Locale.getDefault());
    private final SimpleDateFormat hourFormat = new SimpleDateFormat("H", Locale.US);
    private ShowState state = ShowState.waiting();
    private String setupError = "";
    private long animationStart = SystemClock.uptimeMillis();
    private long taterClockUnixMs;
    private long taterClockSyncedAtElapsedMs;
    private int taterClockUtcOffsetSeconds = Integer.MIN_VALUE;
    private String taterClockTimezone = "";
    private volatile Bitmap notificationImage;
    private volatile String notificationImageUrl = "";
    private final RectF intercomBounds = new RectF();
    private float intercomCenterX;
    private float intercomCenterY;
    private float intercomRadius;
    private CommandSink commandSink;
    private boolean intercomPressed;

    TaterShowView(Context context) {
        super(context);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        stroke.setStyle(Paint.Style.STROKE);
        stroke.setStrokeCap(Paint.Cap.ROUND);
        setFocusable(true);
        setContentDescription("Tater Show satellite display");
    }

    void setState(ShowState next) {
        String oldNotification = state.notification == null ? "" : state.notification.id;
        String nextNotification = next.notification == null ? "" : next.notification.id;
        if (!next.phase.equals(state.phase) || !next.message.equals(state.message)
                || !next.toolName.equals(state.toolName)
                || !next.toolMessage.equals(state.toolMessage)
                || !nextNotification.equals(oldNotification)) {
            animationStart = SystemClock.uptimeMillis();
        }
        if (next.taterTimeUnixMs > 0) {
            if (next.taterTimeUnixMs != taterClockUnixMs) {
                taterClockUnixMs = next.taterTimeUnixMs;
                taterClockSyncedAtElapsedMs = SystemClock.elapsedRealtime();
            }
            if (next.taterUtcOffsetSeconds != taterClockUtcOffsetSeconds
                    || !next.taterTimezone.equals(taterClockTimezone)) {
                taterClockUtcOffsetSeconds = next.taterUtcOffsetSeconds;
                taterClockTimezone = next.taterTimezone;
                SimpleTimeZone timezone = new SimpleTimeZone(
                        taterClockUtcOffsetSeconds * 1000,
                        taterClockTimezone);
                clockFormat.setTimeZone(timezone);
                dateFormat.setTimeZone(timezone);
                hourFormat.setTimeZone(timezone);
            }
        }
        state = next;
        if ("setup".equals(next.phase)) {
            setContentDescription("Tater Show setup");
        } else if (next.isControllerConnecting()) {
            setContentDescription("Connecting to Tater");
        } else {
            setContentDescription("Tater Show connected");
        }
        if ((!next.connected || next.muted || "setup".equals(next.phase)) && intercomPressed) {
            intercomPressed = false;
        }
        String nextImageUrl = next.notification == null ? "" : next.notification.imageUrl;
        if (!nextImageUrl.equals(notificationImageUrl)) {
            notificationImageUrl = nextImageUrl;
            notificationImage = null;
            if (!nextImageUrl.isEmpty()) loadNotificationImage(nextImageUrl);
        }
        invalidate();
    }

    void setCommandSink(CommandSink sink) {
        commandSink = sink;
    }

    void setSetupError(String error) {
        setupError = error == null ? "" : error.trim();
        invalidate();
    }

    @Override
    protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        float width = getWidth();
        float height = getHeight();
        long now = SystemClock.uptimeMillis();
        float seconds = (now - animationStart) / 1000f;
        int accent = accentFor(state.phase);

        if ("setup".equals(state.phase)) {
            if (isSetupConnecting()) {
                drawConnecting(canvas, width, height, seconds, accent, true);
            } else {
                drawSetup(canvas, width, height, seconds, accent);
            }
            postInvalidateDelayed(33);
            return;
        }

        if (state.isControllerConnecting()) {
            drawConnecting(canvas, width, height, seconds, accent, false);
            postInvalidateDelayed(33);
            return;
        }

        paint.setShader(new LinearGradient(0, 0, width, height,
                new int[]{Color.rgb(6, 11, 19), blend(Color.rgb(6, 11, 19), accent, 0.14f), Color.rgb(9, 13, 24)},
                new float[]{0f, .58f, 1f}, Shader.TileMode.CLAMP));
        canvas.drawRect(0, 0, width, height, paint);
        paint.setShader(null);

        float margin = height * .075f;
        drawClock(canvas, margin, height, accent);
        if (notificationIsActive()) {
            drawNotification(canvas, width, height, seconds, accent);
        } else if (state.weather == null) {
            drawOrb(canvas, width * .70f, height * .43f, height * .235f, seconds, accent);
        } else {
            drawWeather(canvas, width, height, seconds, accent);
        }
        drawStatus(canvas, margin, height, accent);
        drawIntercomControl(canvas, width, height, seconds, accent);
        if (shouldDrawCompactVoiceOrb(state.phase, state.weather != null)) {
            drawCompactVoiceOrb(canvas, width * .50f, height * .905f,
                    height * .058f, seconds, accent);
        }

        postInvalidateDelayed(33);
    }

    @Override
    public boolean onTouchEvent(MotionEvent event) {
        if ("setup".equals(state.phase)) return false;
        float x = event.getX();
        float y = event.getY();
        switch (event.getActionMasked()) {
            case MotionEvent.ACTION_DOWN:
                if (!state.connected || state.muted
                        || !pointInCircle(x, y, intercomCenterX, intercomCenterY, intercomRadius)) {
                    return false;
                }
                intercomPressed = true;
                performHapticFeedback(HapticFeedbackConstants.LONG_PRESS);
                if (commandSink != null) commandSink.send("intercom.start", null);
                invalidate();
                return true;
            case MotionEvent.ACTION_UP:
            case MotionEvent.ACTION_CANCEL:
                if (!intercomPressed) return false;
                intercomPressed = false;
                if (commandSink != null) commandSink.send("intercom.stop", null);
                performClick();
                invalidate();
                return true;
            default:
                return intercomPressed;
        }
    }

    @Override
    public boolean performClick() {
        super.performClick();
        return true;
    }

    private void drawIntercomControl(Canvas canvas, float width, float height, float seconds, int accent) {
        float radius = Math.min(height * .175f, width * .097f);
        float cx = -radius * .04f;
        float cy = height + radius * .03f;
        intercomCenterX = cx;
        intercomCenterY = cy;
        intercomRadius = radius;
        intercomBounds.set(cx - radius, cy - radius, cx + radius, cy + radius);

        boolean active = intercomPressed || "intercom".equals(state.phase);
        boolean enabled = state.connected && !state.muted;
        float pulse = .5f + .5f * (float) Math.sin(seconds * 5.2f);
        int buttonColor = active ? Color.rgb(255, 105, 140) : accent;
        if (active) {
            paint.setColor(withAlpha(buttonColor, 25 + (int) (pulse * 30f)));
            canvas.drawCircle(cx, cy, radius * (1.08f + pulse * .05f), paint);
        }
        paint.setShader(new RadialGradient(
                cx - radius * .28f, cy - radius * .58f, radius * 1.45f,
                withAlpha(blend(buttonColor, Color.WHITE, active ? .20f : .08f),
                        active ? 160 : enabled ? 104 : 48),
                withAlpha(blend(buttonColor, Color.BLACK, .48f),
                        active ? 120 : enabled ? 74 : 32),
                Shader.TileMode.CLAMP));
        canvas.drawCircle(cx, cy, radius, paint);
        paint.setShader(null);

        stroke.setStrokeWidth(Math.max(2f, height * .004f));
        stroke.setColor(withAlpha(buttonColor, active ? 245 : enabled ? 178 : 65));
        canvas.drawCircle(cx, cy, radius, stroke);

        float contentX = radius * .38f;
        float micX = contentX;
        float micY = height - radius * .51f;
        float micSize = radius * .105f;
        stroke.setStrokeWidth(Math.max(2f, height * .0047f));
        stroke.setColor(enabled ? Color.WHITE : Color.rgb(98, 105, 116));
        canvas.drawRoundRect(new RectF(micX - micSize * .48f, micY - micSize,
                micX + micSize * .48f, micY + micSize * .48f),
                micSize * .45f, micSize * .45f, stroke);
        canvas.drawArc(new RectF(micX - micSize * .86f, micY - micSize * .06f,
                micX + micSize * .86f, micY + micSize * 1.06f), 0f, 180f, false, stroke);
        canvas.drawLine(micX, micY + micSize * 1.06f, micX, micY + micSize * 1.42f, stroke);

        paint.setTextAlign(Paint.Align.CENTER);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .017f);
        paint.setColor(enabled ? Color.rgb(242, 245, 249) : Color.rgb(105, 112, 124));
        String label = active ? "RELEASE" : state.muted ? "MUTED" : "INTERCOM";
        canvas.drawText(label, contentX, height - radius * .18f, paint);
        paint.setTextAlign(Paint.Align.LEFT);
    }

    static boolean pointInCircle(float x, float y, float cx, float cy, float radius) {
        if (radius <= 0f) return false;
        float dx = x - cx;
        float dy = y - cy;
        return dx * dx + dy * dy <= radius * radius;
    }

    static boolean shouldDrawCompactVoiceOrb(String phase, boolean hasWeather) {
        if (!hasWeather) return false;
        return "listening".equals(phase)
                || "thinking".equals(phase)
                || "tool_call".equals(phase)
                || "speaking".equals(phase)
                || "intercom".equals(phase);
    }

    static float compactOrbAudioEnergy(float audioLevel) {
        return Math.max(0f, Math.min(1f, audioLevel * 2.2f));
    }

    static float compactOrbDistortion(float audioEnergy) {
        return .070f + Math.max(0f, Math.min(1f, audioEnergy)) * .180f;
    }

    private void drawCompactVoiceOrb(Canvas canvas, float cx, float baseCy, float radius,
                                     float seconds, int accent) {
        float audio = compactOrbAudioEnergy(state.audioLevel);
        float breath = .5f + .5f * (float) Math.sin(seconds * 2.15f);
        float voice = .5f + .5f * (float) Math.sin(seconds * 12.4f);
        float cy = baseCy + (float) Math.sin(seconds * 1.7f) * radius * .08f;
        float shellRadius = radius * (1f + breath * .025f + audio * (.08f + voice * .045f));

        // A soft grounding shadow makes the orb feel like it is hovering above the display edge.
        paint.setShader(new RadialGradient(cx, baseCy + radius * 1.30f, radius * 1.25f,
                withAlpha(Color.BLACK, 78), Color.TRANSPARENT, Shader.TileMode.CLAMP));
        canvas.save();
        canvas.scale(1f, .24f, cx, baseCy + radius * 1.30f);
        canvas.drawCircle(cx, baseCy + radius * 1.30f, radius * 1.25f, paint);
        canvas.restore();
        paint.setShader(null);

        float auraRadius = shellRadius * (1.65f + audio * .20f);
        paint.setShader(new RadialGradient(cx, cy, auraRadius,
                new int[]{withAlpha(blend(accent, Color.WHITE, .18f), 42 + (int) (audio * 45f)),
                        withAlpha(accent, 18 + (int) (audio * 30f)), Color.TRANSPARENT},
                new float[]{0f, .48f, 1f}, Shader.TileMode.CLAMP));
        canvas.drawCircle(cx, cy, auraRadius, paint);
        paint.setShader(null);

        // Build a smoothly connected, asymmetric bubble. Live audio increases both its size
        // and the perimeter deformation, so the bubble itself responds rather than merely
        // containing an equalizer.
        float distortion = compactOrbDistortion(audio);
        float squash = (float) Math.sin(seconds * 2.9f + .35f)
                * (.060f + audio * .075f);
        float sway = (float) Math.sin(seconds * 2.15f) * radius * (.025f + audio * .025f);
        for (int index = 0; index < bubbleX.length; index++) {
            float angle = (float) (Math.PI * 2d * index / bubbleX.length);
            float wobble = (float) Math.sin(angle * 3f + seconds * 3.2f) * distortion * .62f
                    + (float) Math.sin(angle * 5f - seconds * 4.4f) * distortion * .25f
                    + (float) Math.sin(angle * 2f + seconds * 2.1f) * distortion * .13f;
            float localRadius = shellRadius * (1f + wobble);
            bubbleX[index] = cx + sway + (float) Math.cos(angle) * localRadius * (1f + squash);
            bubbleY[index] = cy + (float) Math.sin(angle) * localRadius * (1f - squash * .72f);
        }
        int last = bubbleX.length - 1;
        bubble.reset();
        bubble.moveTo((bubbleX[last] + bubbleX[0]) / 2f,
                (bubbleY[last] + bubbleY[0]) / 2f);
        for (int index = 0; index < bubbleX.length; index++) {
            int next = (index + 1) % bubbleX.length;
            bubble.quadTo(bubbleX[index], bubbleY[index],
                    (bubbleX[index] + bubbleX[next]) / 2f,
                    (bubbleY[index] + bubbleY[next]) / 2f);
        }
        bubble.close();

        paint.setShader(new RadialGradient(cx - shellRadius * .30f, cy - shellRadius * .35f,
                shellRadius * 1.55f,
                new int[]{withAlpha(blend(accent, Color.WHITE, .76f), 62 + (int) (audio * 18f)),
                        withAlpha(accent, 34 + (int) (audio * 22f)),
                        withAlpha(blend(accent, Color.BLACK, .25f), 18 + (int) (audio * 18f)),
                        withAlpha(blend(accent, Color.WHITE, .45f), 72 + (int) (audio * 58f))},
                new float[]{0f, .35f, .76f, 1f}, Shader.TileMode.CLAMP));
        canvas.drawPath(bubble, paint);
        paint.setShader(null);

        stroke.setStrokeWidth(Math.max(1.5f, radius * .050f));
        stroke.setColor(withAlpha(blend(accent, Color.WHITE, .62f),
                118 + (int) (audio * 92f)));
        canvas.drawPath(bubble, stroke);

        // Five voice filaments live inside the orb. Their overall height comes directly from
        // the reported audio level; the phase offsets only make the response feel fluid.
        float spacing = radius * .25f;
        for (int index = -2; index <= 2; index++) {
            float centerWeight = 1f - Math.abs(index) * .12f;
            float motion = .55f + .45f * (float) Math.sin(seconds * 13.5f + index * .92f);
            float flutter = .5f + .5f * (float) Math.sin(seconds * 20.5f - index * 1.17f);
            float halfHeight = radius * (.055f
                    + audio * (.20f + .48f * motion + .10f * flutter) * centerWeight);
            float barWidth = radius * (.10f + audio * .025f);
            float verticalDrift = (float) Math.sin(seconds * 10.8f + index * .74f)
                    * radius * audio * .075f;
            paint.setColor(withAlpha(Color.WHITE, 112 + (int) (audio * 125f)));
            canvas.drawRoundRect(new RectF(cx + index * spacing - barWidth / 2f,
                            cy + verticalDrift - halfHeight,
                            cx + index * spacing + barWidth / 2f,
                            cy + verticalDrift + halfHeight),
                    barWidth / 2f, barWidth / 2f, paint);
        }

        paint.setColor(withAlpha(Color.WHITE, 92 + (int) (audio * 45f)));
        canvas.drawCircle(cx - shellRadius * .31f, cy - shellRadius * .36f,
                radius * (.07f + breath * .015f), paint);
    }

    private long currentTaterUnixMs() {
        return taterClockUnixMs > 0
                ? taterClockUnixMs + (SystemClock.elapsedRealtime() - taterClockSyncedAtElapsedMs)
                : System.currentTimeMillis();
    }

    private boolean notificationIsActive() {
        return state.notification != null
                && (state.notification.expiresAtUnixMs <= 0
                || currentTaterUnixMs() < state.notification.expiresAtUnixMs);
    }

    private void loadNotificationImage(String imageUrl) {
        final String requested = imageUrl;
        new Thread(() -> {
            HttpURLConnection connection = null;
            try {
                connection = (HttpURLConnection) new URL(requested).openConnection();
                connection.setConnectTimeout(1800);
                connection.setReadTimeout(3000);
                connection.setUseCaches(false);
                try (InputStream stream = connection.getInputStream()) {
                    Bitmap decoded = BitmapFactory.decodeStream(stream);
                    if (decoded != null && requested.equals(notificationImageUrl)) {
                        notificationImage = decoded;
                        postInvalidate();
                    }
                }
            } catch (Exception ignored) {
                // Text remains useful when an optional event image cannot load.
            } finally {
                if (connection != null) connection.disconnect();
            }
        }, "tater-notification-image").start();
    }

    private void drawSetup(Canvas canvas, float width, float height, float seconds, int accent) {
        paint.setShader(new LinearGradient(0, 0, width, height,
                new int[]{Color.rgb(17, 9, 5), Color.rgb(36, 17, 8), Color.rgb(8, 13, 18)},
                new float[]{0f, .55f, 1f}, Shader.TileMode.CLAMP));
        canvas.drawRect(0, 0, width, height, paint);
        paint.setShader(null);

        float margin = height * .075f;
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setColor(Color.rgb(255, 194, 132));
        paint.setTextSize(height * .031f);
        canvas.drawText("TATER ECHO SETUP", margin, margin + height * .028f, paint);
        paint.setColor(Color.WHITE);
        paint.setTextSize(height * .064f);
        canvas.drawText("Connect this Show to Tater", margin, margin + height * .098f, paint);

        float cardTop = height * .285f;
        float cardBottom = height * .405f;
        float gap = height * .035f;
        float cardWidth = (width - margin * 2f - gap) / 2f;
        drawSetupCard(canvas, new RectF(margin, cardTop, margin + cardWidth, cardBottom),
                "1", "WI-FI", state.deviceName, accent);
        drawSetupCard(canvas, new RectF(margin + cardWidth + gap, cardTop, width - margin, cardBottom),
                "2", "SETUP PAGE", "192.168.4.1", accent);

        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .044f);
        paint.setColor(Color.WHITE);
        canvas.drawText("In Tater: Satellites  ›  Add Satellite", margin, height * .505f, paint);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        paint.setTextSize(height * .031f);
        paint.setColor(Color.rgb(180, 188, 199));
        canvas.drawText("Enter the pairing code, Wi-Fi details, room and device name in the setup page.",
                margin, height * .575f, paint);

        if (!setupError.isEmpty()) {
            paint.setColor(Color.rgb(255, 105, 92));
            paint.setTextSize(height * .029f);
            canvas.drawText(ellipsize(setupError, 72), margin, height * .74f, paint);
        } else {
            drawWaitingForSetup(canvas, margin, height * .70f, height, seconds, accent);
        }
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        paint.setTextSize(height * .025f);
        paint.setColor(Color.rgb(126, 136, 150));
        canvas.drawText("The temporary setup network turns off after the Show connects.",
                margin, height * .885f, paint);
    }

    private void drawSetupCard(Canvas canvas, RectF rect, String number, String label, String value, int accent) {
        paint.setColor(Color.argb(205, 25, 28, 34));
        canvas.drawRoundRect(rect, rect.height() * .12f, rect.height() * .12f, paint);
        stroke.setColor(Color.argb(90, 255, 190, 125));
        stroke.setStrokeWidth(1.5f);
        canvas.drawRoundRect(rect, rect.height() * .12f, rect.height() * .12f, stroke);
        float inset = rect.height() * .50f;
        paint.setColor(accent);
        canvas.drawCircle(rect.left + inset, rect.centerY(), inset * .38f, paint);
        paint.setTextAlign(Paint.Align.CENTER);
        paint.setColor(Color.rgb(35, 16, 5));
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(rect.height() * .27f);
        canvas.drawText(number, rect.left + inset, rect.centerY() - (paint.ascent() + paint.descent()) / 2f, paint);
        paint.setTextAlign(Paint.Align.LEFT);
        paint.setTextSize(rect.height() * .22f);
        paint.setColor(Color.rgb(170, 178, 190));
        float textY = rect.centerY() - (paint.ascent() + paint.descent()) / 2f;
        float labelX = rect.left + rect.height() * .95f;
        canvas.drawText(label, labelX, textY, paint);
        float valueX = labelX + paint.measureText(label) + rect.height() * .32f;
        paint.setTextSize(rect.height() * .34f);
        paint.setColor(Color.WHITE);
        canvas.drawText(ellipsize(value, 24), valueX,
                rect.centerY() - (paint.ascent() + paint.descent()) / 2f, paint);
    }

    private void drawWaitingForSetup(Canvas canvas, float left, float centerY,
                                     float height, float seconds, int accent) {
        float pillHeight = height * .095f;
        float pillWidth = height * .49f;
        RectF pill = new RectF(left, centerY - pillHeight / 2f,
                left + pillWidth, centerY + pillHeight / 2f);
        float breath = .5f + .5f * (float) Math.sin(seconds * 2.4f);
        paint.setColor(Color.argb(82 + (int) (breath * 28), 62, 35, 22));
        canvas.drawRoundRect(pill, pillHeight / 2f, pillHeight / 2f, paint);
        stroke.setStrokeWidth(1.5f);
        stroke.setColor(withAlpha(accent, 65 + (int) (breath * 55)));
        canvas.drawRoundRect(pill, pillHeight / 2f, pillHeight / 2f, stroke);

        float originX = left + pillHeight * .52f;
        for (int i = 0; i < 3; i++) {
            float progress = (seconds * .62f + i / 3f) % 1f;
            stroke.setStrokeWidth(Math.max(1.5f, height * .004f * (1f - progress)));
            stroke.setColor(withAlpha(accent, (int) (115f * (1f - progress))));
            canvas.drawCircle(originX, centerY, height * (.011f + .025f * progress), stroke);
        }
        paint.setColor(accent);
        canvas.drawCircle(originX, centerY, height * .010f, paint);

        int dotCount = ((int) (seconds * 1.8f)) % 4;
        StringBuilder label = new StringBuilder("Waiting for setup");
        for (int i = 0; i < dotCount; i++) label.append('.');
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .030f);
        paint.setColor(Color.rgb(230, 205, 185));
        canvas.drawText(label.toString(), left + pillHeight * 1.08f,
                centerY - (paint.ascent() + paint.descent()) / 2f, paint);
    }

    private void drawConnecting(Canvas canvas, float width, float height,
                                float seconds, int accent, boolean joiningWifi) {
        paint.setShader(new LinearGradient(0, 0, width, height,
                new int[]{Color.rgb(31, 12, 5), Color.rgb(15, 19, 23), Color.rgb(5, 10, 16)},
                new float[]{0f, .52f, 1f}, Shader.TileMode.CLAMP));
        canvas.drawRect(0, 0, width, height, paint);
        paint.setShader(null);

        float cx = width * .5f;
        float cy = height * .38f;
        float baseRadius = height * .105f;
        float bloom = Math.max(0f, 1f - seconds / .85f);
        if (bloom > 0f) {
            paint.setColor(withAlpha(accent, (int) (95f * bloom)));
            canvas.drawCircle(cx, cy, height * (.18f + .72f * (1f - bloom)), paint);
        }

        for (int i = 0; i < 3; i++) {
            float progress = (seconds * .38f + i / 3f) % 1f;
            float radius = baseRadius * (1.15f + progress * 1.65f);
            stroke.setStrokeWidth(Math.max(1.5f, height * .008f * (1f - progress)));
            stroke.setColor(withAlpha(accent, (int) (145f * (1f - progress))));
            canvas.drawCircle(cx, cy, radius, stroke);
        }

        paint.setShader(new LinearGradient(cx - baseRadius, cy - baseRadius,
                cx + baseRadius, cy + baseRadius,
                Color.rgb(255, 181, 105), Color.rgb(255, 112, 34), Shader.TileMode.CLAMP));
        canvas.drawCircle(cx, cy, baseRadius, paint);
        paint.setShader(null);
        paint.setTextAlign(Paint.Align.CENTER);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(baseRadius * .92f);
        paint.setColor(Color.rgb(43, 17, 5));
        canvas.drawText("T", cx, cy - (paint.ascent() + paint.descent()) / 2f, paint);

        float orbitRadius = baseRadius * 1.58f;
        for (int i = 0; i < 3; i++) {
            double angle = seconds * 1.7 + i * Math.PI * 2.0 / 3.0;
            paint.setColor(withAlpha(Color.WHITE, 205 - i * 35));
            canvas.drawCircle(cx + (float) Math.cos(angle) * orbitRadius,
                    cy + (float) Math.sin(angle) * orbitRadius,
                    height * (.009f - i * .0015f), paint);
        }

        float fade = Math.min(1f, seconds / .45f);
        paint.setTextSize(height * .062f);
        paint.setColor(withAlpha(Color.WHITE, (int) (255f * fade)));
        canvas.drawText(joiningWifi ? "Connecting your Echo Show" : "Connecting to Tater",
                cx, height * .665f, paint);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        paint.setTextSize(height * .031f);
        paint.setColor(withAlpha(Color.rgb(190, 198, 208), (int) (255f * fade)));
        canvas.drawText(joiningWifi
                        ? "Joining Wi-Fi and linking securely to Tater"
                        : "Starting your satellite and establishing a secure connection",
                cx, height * .725f, paint);

        float trackLeft = width * .31f;
        float trackRight = width * .69f;
        float trackY = height * .805f;
        float trackHeight = height * .012f;
        paint.setColor(Color.argb(80, 255, 255, 255));
        canvas.drawRoundRect(new RectF(trackLeft, trackY, trackRight, trackY + trackHeight),
                trackHeight / 2f, trackHeight / 2f, paint);
        float travel = (seconds * .34f) % 1f;
        float segment = (trackRight - trackLeft) * .28f;
        float segmentLeft = trackLeft + (trackRight - trackLeft + segment) * travel - segment;
        float segmentRight = Math.min(trackRight, segmentLeft + segment);
        segmentLeft = Math.max(trackLeft, segmentLeft);
        if (segmentRight > segmentLeft) {
            paint.setShader(new LinearGradient(segmentLeft, trackY, segmentRight, trackY,
                    withAlpha(accent, 90), Color.rgb(255, 209, 157), Shader.TileMode.CLAMP));
            canvas.drawRoundRect(new RectF(segmentLeft, trackY, segmentRight, trackY + trackHeight),
                    trackHeight / 2f, trackHeight / 2f, paint);
            paint.setShader(null);
        }

        paint.setTextSize(height * .025f);
        paint.setColor(Color.rgb(126, 136, 150));
        canvas.drawText(joiningWifi
                        ? "This usually takes less than a minute"
                        : "Your dashboard will appear when Tater is ready",
                cx, height * .90f, paint);
        paint.setTextAlign(Paint.Align.LEFT);
    }

    private boolean isSetupConnecting() {
        return state.message.toLowerCase(Locale.US).contains("joining wi-fi");
    }

    private void drawClock(Canvas canvas, float margin, float height, int accent) {
        long nowUnixMs = currentTaterUnixMs();
        Date now = new Date(nowUnixMs);
        paint.setColor(Color.WHITE);
        paint.setTextSize(height * .20f);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        canvas.drawText(clockFormat.format(now), margin, height * .30f, paint);

        paint.setTextSize(height * .046f);
        paint.setColor(Color.rgb(180, 190, 207));
        canvas.drawText(dateFormat.format(now), margin + 3, height * .375f, paint);

        paint.setTextSize(height * .035f);
        paint.setColor(accent);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        canvas.drawText(state.room.toUpperCase(Locale.getDefault()), margin + 3, height * .445f, paint);
    }

    private void drawStatus(Canvas canvas, float margin, float height, int accent) {
        boolean toolCall = "tool_call".equals(state.phase);
        String headline;
        String detail;
        if (toolCall) {
            String tool = toolDisplayName(state.toolName);
            headline = tool.isEmpty() ? "Tater is working" : "Using " + tool;
            detail = state.toolMessage.isEmpty() ? state.message : state.toolMessage;
        } else {
            headline = state.mediaTitle.isEmpty() ? state.message : state.mediaTitle;
            detail = state.mediaTitle.isEmpty() ? state.deviceName
                    : (state.mediaArtist.isEmpty() ? state.deviceName : state.mediaArtist);
        }
        if (!toolCall && state.mediaTitle.isEmpty() && state.connected && "idle".equals(state.phase)
                && "Ready when you are".equals(state.message)) {
            try {
                headline = ShowState.timeGreeting(Integer.parseInt(
                        hourFormat.format(new Date(currentTaterUnixMs()))));
            } catch (NumberFormatException ignored) {
                headline = "Good day";
            }
        }
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .055f);
        paint.setColor(Color.WHITE);
        canvas.drawText(ellipsize(headline, 31), margin, height * .58f, paint);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        paint.setTextSize(height * .036f);
        paint.setColor(Color.rgb(151, 164, 184));
        canvas.drawText(ellipsize(detail, 39), margin, height * .645f, paint);

        float dotX = margin + 4;
        float dotY = height * .704f;
        paint.setColor(state.connected ? Color.rgb(77, 223, 158) : Color.rgb(255, 158, 72));
        canvas.drawCircle(dotX, dotY - height * .011f, height * .011f, paint);
        paint.setTextSize(height * .030f);
        paint.setColor(Color.rgb(150, 163, 181));
        canvas.drawText(state.connected ? "Tater connected" : "Native service offline",
                dotX + height * .026f, dotY, paint);
    }

    private void drawToolCall(Canvas canvas, float width, float height, float seconds, int accent) {
        float cx = width * .73f;
        float cy = height * .40f;
        float radius = height * .22f;
        float pulse = .5f + .5f * (float) Math.sin(seconds * Math.PI * 1.35f);

        paint.setShader(new RadialGradient(cx, cy, radius * 1.55f,
                withAlpha(accent, 62 + (int) (pulse * 24f)), Color.TRANSPARENT,
                Shader.TileMode.CLAMP));
        canvas.drawCircle(cx, cy, radius * 1.55f, paint);
        paint.setShader(null);

        for (int ring = 0; ring < 3; ring++) {
            float ringRadius = radius * (.56f + ring * .20f);
            RectF bounds = new RectF(cx - ringRadius, cy - ringRadius,
                    cx + ringRadius, cy + ringRadius);
            stroke.setStrokeWidth(Math.max(2f, height * (.006f - ring * .0008f)));
            stroke.setColor(withAlpha(blend(accent, Color.WHITE, ring * .12f),
                    80 + ring * 28));
            float rotation = seconds * (ring % 2 == 0 ? 54f : -42f) + ring * 96f;
            canvas.drawArc(bounds, rotation, 82f + ring * 12f, false, stroke);
            canvas.drawArc(bounds, rotation + 180f, 38f + ring * 8f, false, stroke);
        }

        for (int dot = 0; dot < 4; dot++) {
            double angle = seconds * (dot % 2 == 0 ? 1.8 : -1.35) + dot * Math.PI / 2d;
            float orbit = radius * (dot < 2 ? .72f : .94f);
            float x = cx + (float) Math.cos(angle) * orbit;
            float y = cy + (float) Math.sin(angle) * orbit;
            paint.setColor(withAlpha(blend(accent, Color.WHITE, .35f), 185));
            canvas.drawCircle(x, y, height * (dot < 2 ? .014f : .010f), paint);
        }

        float coreRadius = radius * (.39f + pulse * .025f);
        paint.setShader(new RadialGradient(cx - coreRadius * .20f, cy - coreRadius * .22f,
                coreRadius * 1.35f, blend(accent, Color.WHITE, .30f),
                blend(accent, Color.BLACK, .30f), Shader.TileMode.CLAMP));
        canvas.drawCircle(cx, cy, coreRadius, paint);
        paint.setShader(null);

        float dotSpacing = radius * .14f;
        for (int dot = -1; dot <= 1; dot++) {
            float dotPulse = .55f + .45f * (float) Math.sin(seconds * 5.2f + dot * .9f);
            paint.setColor(withAlpha(Color.WHITE, 150 + (int) (dotPulse * 95f)));
            canvas.drawCircle(cx + dot * dotSpacing, cy - height * .012f,
                    height * (.010f + dotPulse * .004f), paint);
        }

        String tool = toolDisplayName(state.toolName);
        paint.setTextAlign(Paint.Align.CENTER);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .025f);
        paint.setColor(withAlpha(Color.WHITE, 185));
        canvas.drawText("WORKING", cx, cy + radius * .25f, paint);
        if (!tool.isEmpty()) {
            paint.setTextSize(height * .031f);
            paint.setColor(Color.WHITE);
            canvas.drawText(ellipsize(tool, 19), cx, cy + radius * 1.32f, paint);
        }
        paint.setTextAlign(Paint.Align.LEFT);
    }

    private void drawOrb(Canvas canvas, float cx, float cy, float radius, float seconds, int accent) {
        float breathing = 1f + .035f * (float) Math.sin(seconds * Math.PI * 1.25);
        float audio = Math.min(1f, state.audioLevel * 1.8f);
        float activeRadius = radius * breathing * (1f + .10f * audio);

        paint.setColor(withAlpha(accent, 24));
        canvas.drawCircle(cx, cy, activeRadius * 1.30f, paint);
        paint.setColor(withAlpha(accent, 50));
        canvas.drawCircle(cx, cy, activeRadius * 1.12f, paint);
        paint.setShader(new LinearGradient(cx - radius, cy - radius, cx + radius, cy + radius,
                blend(accent, Color.WHITE, .30f), blend(accent, Color.BLACK, .45f), Shader.TileMode.CLAMP));
        canvas.drawCircle(cx, cy, activeRadius * .76f, paint);
        paint.setShader(null);

        stroke.setColor(withAlpha(Color.WHITE, 165));
        stroke.setStrokeWidth(Math.max(3f, radius * .025f));
        if (state.directionDegrees != null && ("listening".equals(state.phase) || "speaking".equals(state.phase))) {
            RectF ring = new RectF(cx - radius, cy - radius, cx + radius, cy + radius);
            canvas.drawArc(ring, state.directionDegrees - 102f, 24f, false, stroke);
        }

        if ("listening".equals(state.phase) || "speaking".equals(state.phase) || "intercom".equals(state.phase)) {
            wave.reset();
            float waveWidth = radius * .80f;
            for (int i = 0; i <= 32; i++) {
                float x = cx - waveWidth / 2f + waveWidth * i / 32f;
                float envelope = (float) Math.sin(Math.PI * i / 32f);
                float phase = seconds * ("listening".equals(state.phase) ? 10f : 6f) + i * .68f;
                float y = cy + (float) Math.sin(phase) * radius * (.06f + .12f * audio) * envelope;
                if (i == 0) wave.moveTo(x, y); else wave.lineTo(x, y);
            }
            stroke.setColor(withAlpha(Color.WHITE, 215));
            stroke.setStrokeWidth(Math.max(4f, radius * .035f));
            canvas.drawPath(wave, stroke);
        } else {
            paint.setColor(withAlpha(Color.WHITE, 225));
            paint.setTextAlign(Paint.Align.CENTER);
            paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
            paint.setTextSize(radius * .24f);
            canvas.drawText(state.muted ? "MUTED" : phaseLabel(state.phase), cx, cy + radius * .08f, paint);
            paint.setTextAlign(Paint.Align.LEFT);
        }

        if (state.timerActive) {
            paint.setColor(Color.rgb(255, 208, 92));
            canvas.drawCircle(cx + radius * .74f, cy - radius * .70f, radius * .10f, paint);
        }
    }

    private void drawWeather(Canvas canvas, float width, float height, float seconds, int accent) {
        ShowState.Weather weather = state.weather;
        float margin = height * .075f;
        float left = width * .535f;
        float right = width - margin;
        float artX = right - height * .13f;
        float artY = height * .17f;
        float artSize = height * .12f;
        int weatherColor = weatherColor(weather.conditionKind);

        paint.setShader(new RadialGradient(artX, artY, height * .30f,
                withAlpha(weatherColor, 52), Color.TRANSPARENT, Shader.TileMode.CLAMP));
        canvas.drawCircle(artX, artY, height * .30f, paint);
        paint.setShader(null);

        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .025f);
        paint.setColor(withAlpha(accent, 225));
        canvas.drawText("OUTSIDE CONDITIONS", left, height * .105f, paint);

        drawWeatherIcon(canvas, artX, artY, artSize, weather.conditionKind, seconds);

        if (!weather.temperatureText.isEmpty()) {
            String temperature = ellipsize(weather.temperatureText, 6);
            paint.setColor(Color.WHITE);
            paint.setTextSize(height * .14f);
            canvas.drawText(temperature, left, height * .285f, paint);
            float tempWidth = paint.measureText(temperature);
            if (!weather.temperatureUnit.isEmpty()) {
                paint.setTextSize(height * .030f);
                paint.setColor(Color.rgb(174, 189, 209));
                canvas.drawText(weather.temperatureUnit, left + tempWidth + height * .010f,
                        height * .235f, paint);
            }
            if (!weather.feelsLikeText.isEmpty()) {
                String feels = weather.feelsLikeText;
                if (feels.toLowerCase(Locale.US).startsWith("feels like ")) {
                    feels = feels.substring("feels like ".length());
                }
                int feelsColor = "cooler".equals(weather.feelsLikeRelation)
                        ? Color.rgb(83, 178, 255)
                        : "warmer".equals(weather.feelsLikeRelation)
                        ? Color.rgb(255, 147, 66)
                        : Color.WHITE;
                float feelsLeft = left + tempWidth + height * .034f;
                paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
                paint.setTextSize(height * .018f);
                paint.setColor(withAlpha(feelsColor, 210));
                canvas.drawText("FEELS LIKE", feelsLeft, height * .245f, paint);
                paint.setTextSize(height * .040f);
                paint.setColor(feelsColor);
                canvas.drawText(ellipsize(feels, 8), feelsLeft, height * .290f, paint);
            }
        }

        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .037f);
        paint.setColor(Color.rgb(235, 240, 248));
        canvas.drawText(ellipsize(weather.condition, 24), left, height * .370f, paint);

        String[] outdoorKinds = {"humidity", "wind", "rain", "lightning"};
        String[] outdoorLabels = {"HUMIDITY", "WIND", "RAIN", "LIGHTNING"};
        String[] outdoorValues = {
                weather.humidityText, weather.windText, weather.rainText, weather.lightningText
        };
        int outdoorCount = 0;
        for (String value : outdoorValues) {
            if (!value.isEmpty()) outdoorCount++;
        }

        if (outdoorCount > 1) {
            stroke.setStrokeWidth(Math.max(1f, height * .0022f));
            stroke.setColor(withAlpha(weatherColor, 30));
            float slotWidth = (right - left) / outdoorCount;
            canvas.drawLine(left + slotWidth * .50f, height * .442f,
                    right - slotWidth * .50f, height * .442f, stroke);
        }

        float[][] metricPositions = outdoorMetricPositions(outdoorCount, left, right, height);
        int outdoorIndex = 0;
        for (int index = 0; index < outdoorValues.length; index++) {
            if (outdoorValues[index].isEmpty()) continue;
            int metricColor = "humidity".equals(outdoorKinds[index])
                    ? Color.rgb(83, 178, 255)
                    : "wind".equals(outdoorKinds[index])
                    ? Color.rgb(143, 211, 222)
                    : "rain".equals(outdoorKinds[index])
                    ? Color.rgb(108, 162, 255)
                    : Color.rgb(255, 208, 92);
            drawWeatherMetricCluster(canvas,
                    metricPositions[outdoorIndex][0], metricPositions[outdoorIndex][1],
                    outdoorKinds[index], outdoorLabels[index], outdoorValues[index], metricColor);
            outdoorIndex++;
        }

        boolean hasIndoor = !weather.indoorTemperatureText.isEmpty()
                || !weather.indoorHumidityText.isEmpty();
        if (hasIndoor) {
            drawIndoorClimateStrip(canvas, left, right, height, accent,
                    indoorClimateTitle(), weather.indoorTemperatureText,
                    weather.indoorHumidityText);
        }

        if (weather.stale) {
            paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
            paint.setTextSize(height * .018f);
            paint.setColor(Color.rgb(255, 170, 92));
            paint.setTextAlign(Paint.Align.RIGHT);
            canvas.drawText("STALE", right - height * .028f, height * .105f, paint);
            paint.setTextAlign(Paint.Align.LEFT);
        }

        if (state.timerActive) {
            paint.setColor(Color.rgb(255, 208, 92));
            canvas.drawCircle(right - height * .012f, height * .087f, height * .010f, paint);
        }
    }

    private void drawNotification(Canvas canvas, float width, float height, float seconds, int accent) {
        ShowState.Notification notification = state.notification;
        float margin = height * .075f;
        float left = width * .535f;
        float right = width - margin;
        int noticeColor = "critical".equals(notification.priority)
                ? Color.rgb(255, 92, 92) : Color.rgb(255, 147, 66);

        paint.setShader(new RadialGradient(right - height * .16f, height * .25f, height * .36f,
                withAlpha(noticeColor, 55), Color.TRANSPARENT, Shader.TileMode.CLAMP));
        canvas.drawCircle(right - height * .16f, height * .25f, height * .36f, paint);
        paint.setShader(null);

        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .025f);
        paint.setColor(noticeColor);
        canvas.drawText("AWARENESS  ·  " + ellipsize(notification.cameraName.toUpperCase(Locale.getDefault()), 24),
                left, height * .09f, paint);

        RectF imageRect = new RectF(left, height * .125f, right, height * .48f);
        Bitmap image = notificationImage;
        if (image != null) {
            Path clip = new Path();
            clip.addRoundRect(imageRect, height * .025f, height * .025f, Path.Direction.CW);
            int save = canvas.save();
            canvas.clipPath(clip);
            float sourceRatio = image.getWidth() / (float) image.getHeight();
            float targetRatio = imageRect.width() / imageRect.height();
            Rect source;
            if (sourceRatio > targetRatio) {
                float visibleWidth = image.getHeight() * targetRatio;
                float offset = (image.getWidth() - visibleWidth) / 2f;
                source = new Rect(Math.round(offset), 0, Math.round(offset + visibleWidth), image.getHeight());
            } else {
                float visibleHeight = image.getWidth() / targetRatio;
                float offset = (image.getHeight() - visibleHeight) / 2f;
                source = new Rect(0, Math.round(offset), image.getWidth(), Math.round(offset + visibleHeight));
            }
            canvas.drawBitmap(image, source, imageRect, paint);
            paint.setShader(new LinearGradient(0, imageRect.top, 0, imageRect.bottom,
                    Color.TRANSPARENT, Color.argb(150, 5, 9, 16), Shader.TileMode.CLAMP));
            canvas.drawRect(imageRect, paint);
            paint.setShader(null);
            canvas.restoreToCount(save);
        } else {
            paint.setColor(Color.argb(95, 26, 31, 42));
            canvas.drawRoundRect(imageRect, height * .025f, height * .025f, paint);
            float cx = imageRect.centerX();
            float cy = imageRect.centerY();
            float pulse = .5f + .5f * (float) Math.sin(seconds * 2.2f);
            stroke.setColor(withAlpha(noticeColor, 70 + (int) (pulse * 70)));
            stroke.setStrokeWidth(height * .006f);
            canvas.drawCircle(cx, cy, height * (.045f + pulse * .018f), stroke);
            paint.setColor(withAlpha(noticeColor, 210));
            canvas.drawCircle(cx, cy, height * .018f, paint);
        }

        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        paint.setTextSize(height * .052f);
        paint.setColor(Color.rgb(215, 224, 236));
        drawWrappedText(canvas, notification.description, left, height * .560f,
                right - left, height * .059f, 4);

        float ttlProgress = 0f;
        if (notification.expiresAtUnixMs > 0) {
            ttlProgress = Math.max(0f, Math.min(1f,
                    (notification.expiresAtUnixMs - currentTaterUnixMs()) / 90000f));
        }
        stroke.setStrokeWidth(height * .004f);
        stroke.setColor(withAlpha(noticeColor, 120));
        canvas.drawLine(left, height * .820f,
                left + (right - left) * ttlProgress, height * .820f, stroke);
    }

    private void drawWrappedText(Canvas canvas, String text, float left, float top,
                                 float maxWidth, float lineHeight, int maxLines) {
        String[] words = (text == null ? "" : text.trim()).split("\\s+");
        String line = "";
        int lineIndex = 0;
        for (String word : words) {
            String candidate = line.isEmpty() ? word : line + " " + word;
            if (!line.isEmpty() && paint.measureText(candidate) > maxWidth) {
                canvas.drawText(ellipsize(line, 74), left, top + lineIndex * lineHeight, paint);
                lineIndex++;
                if (lineIndex >= maxLines) return;
                line = word;
            } else {
                line = candidate;
            }
        }
        if (!line.isEmpty() && lineIndex < maxLines) {
            canvas.drawText(ellipsize(line, 74), left, top + lineIndex * lineHeight, paint);
        }
    }

    private float[][] outdoorMetricPositions(int count, float left, float right, float height) {
        float panelWidth = right - left;
        float[][] positions = new float[Math.max(0, count)][2];
        for (int index = 0; index < count; index++) {
            positions[index] = new float[]{
                    left + panelWidth * (index + .5f) / count,
                    height * .442f
            };
        }
        return positions;
    }

    private void drawWeatherMetricCluster(Canvas canvas, float iconX, float centerY,
                                          String kind, String label, String value, int color) {
        if (value == null || value.isEmpty()) return;
        float height = getHeight();
        float iconRadius = height * .028f;
        paint.setColor(withAlpha(color, 28));
        canvas.drawCircle(iconX, centerY, iconRadius * 1.45f, paint);
        stroke.setStrokeWidth(Math.max(1.5f, height * .0032f));
        stroke.setColor(withAlpha(color, 225));
        drawWeatherMetricGlyph(canvas, iconX, centerY, iconRadius * .84f, kind, color);

        paint.setTextAlign(Paint.Align.CENTER);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .041f);
        paint.setColor(Color.rgb(238, 243, 250));
        canvas.drawText(ellipsize(value, 11), iconX, centerY + height * .078f, paint);
        paint.setTextSize(height * .019f);
        paint.setColor(withAlpha(color, 215));
        canvas.drawText(label, iconX, centerY + height * .112f, paint);
        paint.setTextAlign(Paint.Align.LEFT);
    }

    private void drawWeatherMetricGlyph(Canvas canvas, float cx, float cy, float radius,
                                        String kind, int color) {
        if ("humidity".equals(kind)) {
            wave.reset();
            wave.moveTo(cx, cy - radius * .72f);
            wave.cubicTo(cx - radius * .65f, cy - radius * .05f,
                    cx - radius * .62f, cy + radius * .68f, cx, cy + radius * .72f);
            wave.cubicTo(cx + radius * .62f, cy + radius * .68f,
                    cx + radius * .65f, cy - radius * .05f, cx, cy - radius * .72f);
            paint.setColor(withAlpha(color, 225));
            canvas.drawPath(wave, paint);
        } else if ("wind".equals(kind)) {
            canvas.drawLine(cx - radius * .70f, cy - radius * .28f,
                    cx + radius * .47f, cy - radius * .28f, stroke);
            canvas.drawArc(new RectF(cx + radius * .22f, cy - radius * .58f,
                    cx + radius * .80f, cy), -90f, 180f, false, stroke);
            canvas.drawLine(cx - radius * .70f, cy + radius * .30f,
                    cx + radius * .18f, cy + radius * .30f, stroke);
        } else if ("rain".equals(kind)) {
            for (int i = -1; i <= 1; i++) {
                float x = cx + i * radius * .48f;
                canvas.drawLine(x + radius * .12f, cy - radius * .52f,
                        x - radius * .12f, cy + radius * .52f, stroke);
            }
        } else if ("temperature".equals(kind)) {
            stroke.setStrokeWidth(Math.max(2f, radius * .28f));
            canvas.drawLine(cx, cy - radius * .63f, cx, cy + radius * .28f, stroke);
            paint.setColor(withAlpha(color, 235));
            canvas.drawCircle(cx, cy + radius * .47f, radius * .38f, paint);
            stroke.setStrokeWidth(Math.max(1.2f, radius * .15f));
            canvas.drawRoundRect(new RectF(cx - radius * .28f, cy - radius * .82f,
                    cx + radius * .28f, cy + radius * .52f),
                    radius * .28f, radius * .28f, stroke);
        } else {
            wave.reset();
            wave.moveTo(cx + radius * .18f, cy - radius * .78f);
            wave.lineTo(cx - radius * .48f, cy + radius * .10f);
            wave.lineTo(cx - radius * .02f, cy + radius * .06f);
            wave.lineTo(cx - radius * .22f, cy + radius * .80f);
            wave.lineTo(cx + radius * .55f, cy - radius * .18f);
            wave.lineTo(cx + radius * .08f, cy - radius * .12f);
            wave.close();
            paint.setColor(withAlpha(color, 235));
            canvas.drawPath(wave, paint);
        }
    }

    private String indoorClimateTitle() {
        String room = state.room == null ? "" : state.room.trim();
        if (room.isEmpty() || "unassigned room".equalsIgnoreCase(room)
                || "echo show 5".equalsIgnoreCase(room)) {
            return "INDOOR CLIMATE";
        }
        return room.toUpperCase(Locale.getDefault()) + " CLIMATE";
    }

    private void drawIndoorClimateStrip(Canvas canvas, float left, float right, float height,
                                        int accent, String title,
                                        String temperature, String humidity) {
        float titleY = height * .625f;
        float iconX = left + height * .022f;
        float iconY = titleY - height * .008f;
        float iconRadius = height * .016f;
        paint.setColor(withAlpha(accent, 36));
        canvas.drawCircle(iconX, iconY, iconRadius * 1.65f, paint);
        wave.reset();
        wave.moveTo(iconX - iconRadius * .72f, iconY - iconRadius * .08f);
        wave.lineTo(iconX, iconY - iconRadius * .74f);
        wave.lineTo(iconX + iconRadius * .72f, iconY - iconRadius * .08f);
        wave.moveTo(iconX - iconRadius * .50f, iconY - iconRadius * .20f);
        wave.lineTo(iconX - iconRadius * .50f, iconY + iconRadius * .64f);
        wave.lineTo(iconX + iconRadius * .50f, iconY + iconRadius * .64f);
        wave.lineTo(iconX + iconRadius * .50f, iconY - iconRadius * .20f);
        stroke.setStrokeWidth(Math.max(1.5f, height * .0032f));
        stroke.setColor(withAlpha(accent, 225));
        canvas.drawPath(wave, stroke);

        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .022f);
        paint.setColor(withAlpha(accent, 230));
        String sectionTitle = ellipsize(title, 24);
        float titleX = left + height * .058f;
        canvas.drawText(sectionTitle, titleX, titleY, paint);
        float titleEnd = titleX + paint.measureText(sectionTitle) + height * .025f;
        stroke.setStrokeWidth(Math.max(1f, height * .002f));
        stroke.setColor(withAlpha(accent, 42));
        if (titleEnd < right) {
            canvas.drawLine(titleEnd, titleY - height * .008f,
                    right, titleY - height * .008f, stroke);
        }

        boolean hasTemperature = temperature != null && !temperature.isEmpty();
        boolean hasHumidity = humidity != null && !humidity.isEmpty();
        if (hasTemperature && hasHumidity) {
            drawRoomMetricInline(canvas, left + (right - left) * .19f, height * .705f,
                    "temperature", "TEMPERATURE", temperature, Color.rgb(255, 147, 66));
            drawRoomMetricInline(canvas, left + (right - left) * .64f, height * .705f,
                    "humidity", "HUMIDITY", humidity, Color.rgb(83, 178, 255));
        } else if (hasTemperature) {
            drawRoomMetricInline(canvas, left + (right - left) * .42f, height * .705f,
                    "temperature", "TEMPERATURE", temperature, Color.rgb(255, 147, 66));
        } else if (hasHumidity) {
            drawRoomMetricInline(canvas, left + (right - left) * .42f, height * .705f,
                    "humidity", "HUMIDITY", humidity, Color.rgb(83, 178, 255));
        }
    }

    private void drawRoomMetricInline(Canvas canvas, float iconX, float centerY,
                                      String kind, String label, String value, int color) {
        float height = getHeight();
        float iconRadius = height * .028f;
        paint.setColor(withAlpha(color, 28));
        canvas.drawCircle(iconX, centerY, iconRadius * 1.45f, paint);
        stroke.setStrokeWidth(Math.max(1.5f, height * .0032f));
        stroke.setColor(withAlpha(color, 225));
        drawWeatherMetricGlyph(canvas, iconX, centerY, iconRadius * .84f, kind, color);

        float textX = iconX + height * .047f;
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(height * .019f);
        paint.setColor(withAlpha(color, 215));
        canvas.drawText(label, textX, centerY - height * .008f, paint);
        paint.setTextSize(height * .041f);
        paint.setColor(Color.rgb(238, 243, 250));
        canvas.drawText(ellipsize(value, 10), textX, centerY + height * .038f, paint);
    }

    private void drawWeatherIcon(Canvas canvas, float cx, float cy, float size, String kind, float seconds) {
        String token = kind == null ? "partly" : kind;
        boolean sun = "sun".equals(token) || "partly".equals(token);
        boolean cloud = !"sun".equals(token) && !"wind".equals(token) && !"fog".equals(token);
        float drift = (float) Math.sin(seconds * .42f) * size * .07f;

        if (sun) {
            float sunX = "partly".equals(token) ? cx - size * .34f : cx;
            float sunY = "partly".equals(token) ? cy - size * .23f : cy;
            float breathe = 1f + .035f * (float) Math.sin(seconds * 1.7f);
            paint.setColor(Color.argb(25, 255, 190, 75));
            canvas.drawCircle(sunX, sunY, size * .72f * breathe, paint);
            paint.setColor(Color.rgb(255, 194, 79));
            canvas.drawCircle(sunX, sunY, size * .38f * breathe, paint);
            stroke.setStrokeWidth(Math.max(2f, size * .075f));
            stroke.setColor(Color.rgb(255, 191, 81));
            for (int i = 0; i < 8; i++) {
                double angle = Math.PI * 2 * i / 8.0 + seconds * .16f;
                canvas.drawLine(
                        sunX + (float) Math.cos(angle) * size * .48f,
                        sunY + (float) Math.sin(angle) * size * .48f,
                        sunX + (float) Math.cos(angle) * size * .64f,
                        sunY + (float) Math.sin(angle) * size * .64f,
                        stroke);
            }
        }
        if (cloud) {
            if ("cloud".equals(token) || "rain".equals(token) || "storm".equals(token)) {
                drawCloud(canvas, cx - size * .42f - drift * .45f, cy - size * .18f,
                        size * .76f, Color.argb(105, 122, 143, 169));
                drawCloud(canvas, cx + size * .40f + drift * .30f, cy - size * .03f,
                        size * .62f, Color.argb(115, 144, 162, 185));
            }
            int cloudColor = ("storm".equals(token))
                    ? Color.rgb(139, 151, 175)
                    : Color.rgb(215, 226, 239);
            drawCloud(canvas, cx + drift, cy + size * .12f, size, cloudColor);
        }
        if ("rain".equals(token) || "storm".equals(token)) {
            stroke.setStrokeWidth(Math.max(2f, size * .08f));
            for (int i = 0; i < 6; i++) {
                float progress = (seconds * .72f + i * .19f) % 1f;
                float x = cx - size * .62f + (i % 4) * size * .40f + drift;
                float y = cy + size * (.50f + progress * .76f);
                stroke.setColor(withAlpha(Color.rgb(83, 178, 255), (int) (210f * (1f - progress))));
                canvas.drawLine(x, y, x - size * .10f, y + size * .24f, stroke);
            }
            if ("storm".equals(token) && Math.sin(seconds * 5.7f) > .72f) {
                wave.reset();
                wave.moveTo(cx + size * .08f, cy + size * .48f);
                wave.lineTo(cx - size * .10f, cy + size * .88f);
                wave.lineTo(cx + size * .10f, cy + size * .82f);
                wave.lineTo(cx - size * .06f, cy + size * 1.22f);
                stroke.setColor(Color.rgb(255, 213, 79));
                stroke.setStrokeWidth(size * .10f);
                canvas.drawPath(wave, stroke);
            }
        } else if ("snow".equals(token)) {
            paint.setColor(Color.rgb(190, 226, 255));
            for (int i = 0; i < 7; i++) {
                float progress = (seconds * .24f + i * .17f) % 1f;
                float x = cx - size * .70f + (i % 4) * size * .45f
                        + (float) Math.sin(seconds + i) * size * .08f;
                canvas.drawCircle(x, cy + size * (.50f + progress * .83f), size * .055f, paint);
            }
        } else if ("fog".equals(token) || "wind".equals(token)) {
            stroke.setStrokeWidth(Math.max(2f, size * .075f));
            stroke.setColor(Color.rgb(191, 208, 225));
            for (int i = -1; i <= 1; i++) {
                float y = cy + i * size * .27f;
                float travel = (float) Math.sin(seconds * .50f + i) * size * .12f;
                canvas.drawLine(cx - size * (.72f - Math.abs(i) * .10f) + travel, y,
                        cx + size * (.72f - Math.abs(i) * .10f) + travel, y, stroke);
            }
        }
    }

    private void drawCloud(Canvas canvas, float cx, float cy, float size, int color) {
        paint.setColor(color);
        canvas.drawCircle(cx - size * .34f, cy + size * .10f, size * .31f, paint);
        canvas.drawCircle(cx, cy - size * .08f, size * .42f, paint);
        canvas.drawCircle(cx + size * .38f, cy + size * .12f, size * .28f, paint);
        canvas.drawRoundRect(new RectF(cx - size * .62f, cy + size * .05f,
                cx + size * .63f, cy + size * .39f), size * .16f, size * .16f, paint);
    }

    private static int weatherColor(String kind) {
        if ("sun".equals(kind) || "partly".equals(kind)) return Color.rgb(255, 190, 75);
        if ("rain".equals(kind)) return Color.rgb(76, 169, 255);
        if ("storm".equals(kind)) return Color.rgb(165, 132, 255);
        if ("snow".equals(kind)) return Color.rgb(174, 224, 255);
        if ("fog".equals(kind) || "wind".equals(kind)) return Color.rgb(165, 191, 214);
        return Color.rgb(157, 178, 205);
    }

    private static String ellipsize(String text, int limit) {
        if (text.length() <= limit) return text;
        return text.substring(0, Math.max(1, limit - 1)).trim() + "…";
    }

    private static int accentFor(String phase) {
        switch (phase) {
            case "listening": return Color.rgb(52, 226, 183);
            case "thinking": return Color.rgb(154, 112, 255);
            case "tool_call": return Color.rgb(255, 132, 48);
            case "speaking": return Color.rgb(76, 157, 255);
            case "intercom": return Color.rgb(255, 105, 140);
            case "music": return Color.rgb(86, 206, 255);
            case "error": return Color.rgb(255, 94, 94);
            case "setup": return Color.rgb(255, 198, 80);
            case "offline": return Color.rgb(255, 151, 69);
            default: return Color.rgb(255, 132, 48);
        }
    }

    private static String phaseLabel(String phase) {
        switch (phase) {
            case "thinking": return "THINKING";
            case "tool_call": return "WORKING";
            case "music": return "PLAYING";
            case "error": return "CHECK";
            case "setup": return "SETUP";
            case "offline": return "OFFLINE";
            default: return "TATER";
        }
    }

    static String toolDisplayName(String raw) {
        if (raw == null) return "";
        String cleaned = raw.trim().replace('_', ' ').replace('-', ' ');
        if (cleaned.isEmpty()) return "";
        StringBuilder result = new StringBuilder(cleaned.length());
        boolean capitalize = true;
        for (int index = 0; index < cleaned.length(); index++) {
            char value = cleaned.charAt(index);
            if (Character.isWhitespace(value)) {
                if (result.length() > 0 && result.charAt(result.length() - 1) != ' ') {
                    result.append(' ');
                }
                capitalize = true;
            } else {
                result.append(capitalize ? Character.toUpperCase(value) : value);
                capitalize = false;
            }
        }
        return result.toString().trim();
    }

    private static int withAlpha(int color, int alpha) {
        return Color.argb(alpha, Color.red(color), Color.green(color), Color.blue(color));
    }

    private static int blend(int a, int b, float amount) {
        float x = Math.max(0f, Math.min(1f, amount));
        return Color.rgb(
                (int) (Color.red(a) * (1f - x) + Color.red(b) * x),
                (int) (Color.green(a) * (1f - x) + Color.green(b) * x),
                (int) (Color.blue(a) * (1f - x) + Color.blue(b) * x));
    }
}
