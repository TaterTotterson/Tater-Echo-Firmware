package com.tatertotterson.show;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.LinearGradient;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.RectF;
import android.graphics.Shader;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;

import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;

final class TaterShowView extends View {
    interface Commands {
        void muteToggle();
        void volumeDelta(int delta);
        void intercomStart();
        void intercomStop();
    }

    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint stroke = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Path wave = new Path();
    private final Commands commands;
    private final SimpleDateFormat clockFormat = new SimpleDateFormat("h:mm", Locale.getDefault());
    private final SimpleDateFormat dateFormat = new SimpleDateFormat("EEEE, MMMM d", Locale.getDefault());
    private ShowState state = ShowState.waiting();
    private long animationStart = SystemClock.uptimeMillis();
    private boolean intercomHeld;

    TaterShowView(Context context, Commands commands) {
        super(context);
        this.commands = commands;
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.NORMAL));
        stroke.setStyle(Paint.Style.STROKE);
        stroke.setStrokeCap(Paint.Cap.ROUND);
        setFocusable(true);
        setContentDescription("Tater Show satellite display");
    }

    void setState(ShowState next) {
        if (!next.phase.equals(state.phase)) animationStart = SystemClock.uptimeMillis();
        state = next;
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

        paint.setShader(new LinearGradient(0, 0, width, height,
                new int[]{Color.rgb(6, 11, 19), blend(Color.rgb(6, 11, 19), accent, 0.14f), Color.rgb(9, 13, 24)},
                new float[]{0f, .58f, 1f}, Shader.TileMode.CLAMP));
        canvas.drawRect(0, 0, width, height, paint);
        paint.setShader(null);

        float margin = height * .075f;
        drawClock(canvas, margin, height, accent);
        drawOrb(canvas, width * .70f, height * .43f, height * .235f, seconds, accent);
        drawStatus(canvas, margin, height, accent);
        drawControls(canvas, width, height, margin, accent);

        postInvalidateDelayed(33);
    }

    private void drawClock(Canvas canvas, float margin, float height, int accent) {
        Date now = new Date();
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
        String headline = state.mediaTitle.isEmpty() ? state.message : state.mediaTitle;
        String detail = state.mediaTitle.isEmpty() ? state.deviceName
                : (state.mediaArtist.isEmpty() ? state.deviceName : state.mediaArtist);
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

    private void drawControls(Canvas canvas, float width, float height, float margin, int accent) {
        float top = height * .80f;
        float bottom = height * .94f;
        float gap = height * .025f;
        float small = height * .14f;
        float x = margin;
        drawButton(canvas, new RectF(x, top, x + small, bottom), state.muted ? "UNMUTE" : "MUTE", state.muted, accent);
        x += small + gap;
        drawButton(canvas, new RectF(x, top, x + small, bottom), "−", false, accent);
        x += small + gap;
        drawButton(canvas, new RectF(x, top, x + small, bottom), "+", false, accent);

        String level = state.volumePercent + "%";
        paint.setTextAlign(Paint.Align.CENTER);
        paint.setTextSize(height * .027f);
        paint.setColor(Color.rgb(142, 156, 176));
        canvas.drawText(level, margin + small * 2.5f + gap * 2f, height * .985f, paint);
        paint.setTextAlign(Paint.Align.LEFT);

        float talkWidth = width * .25f;
        drawButton(canvas, new RectF(width - margin - talkWidth, top, width - margin, bottom),
                intercomHeld ? "TALKING…" : "HOLD TO TALK", intercomHeld, accent);
    }

    private void drawButton(Canvas canvas, RectF rect, String label, boolean active, int accent) {
        paint.setColor(active ? withAlpha(accent, 210) : Color.argb(145, 25, 34, 50));
        canvas.drawRoundRect(rect, rect.height() * .28f, rect.height() * .28f, paint);
        stroke.setColor(active ? withAlpha(Color.WHITE, 115) : Color.argb(80, 210, 220, 235));
        stroke.setStrokeWidth(1.5f);
        canvas.drawRoundRect(rect, rect.height() * .28f, rect.height() * .28f, stroke);
        paint.setTextAlign(Paint.Align.CENTER);
        paint.setTypeface(android.graphics.Typeface.create("sans", android.graphics.Typeface.BOLD));
        paint.setTextSize(rect.height() * (label.length() == 1 ? .43f : .23f));
        paint.setColor(Color.WHITE);
        canvas.drawText(label, rect.centerX(), rect.centerY() - (paint.ascent() + paint.descent()) / 2f, paint);
        paint.setTextAlign(Paint.Align.LEFT);
    }

    @Override
    public boolean onTouchEvent(MotionEvent event) {
        float width = getWidth();
        float height = getHeight();
        float margin = height * .075f;
        float top = height * .76f;
        float small = height * .14f;
        float gap = height * .025f;
        float x = event.getX();
        float y = event.getY();
        boolean inRow = y >= top;
        boolean inTalk = inRow && x >= width - margin - width * .25f;

        if (event.getActionMasked() == MotionEvent.ACTION_DOWN) {
            if (inTalk && state.connected) {
                intercomHeld = true;
                commands.intercomStart();
                invalidate();
                return true;
            }
            return inRow;
        }
        if (event.getActionMasked() == MotionEvent.ACTION_UP) {
            if (intercomHeld) {
                intercomHeld = false;
                commands.intercomStop();
                invalidate();
                return true;
            }
            if (!inRow || !state.connected) return true;
            if (x >= margin && x <= margin + small) {
                commands.muteToggle();
            } else if (x >= margin + small + gap && x <= margin + small * 2f + gap) {
                commands.volumeDelta(-8);
            } else if (x >= margin + small * 2f + gap * 2f && x <= margin + small * 3f + gap * 2f) {
                commands.volumeDelta(8);
            }
            return true;
        }
        if (event.getActionMasked() == MotionEvent.ACTION_CANCEL && intercomHeld) {
            intercomHeld = false;
            commands.intercomStop();
            invalidate();
        }
        return true;
    }

    private static String ellipsize(String text, int limit) {
        if (text.length() <= limit) return text;
        return text.substring(0, Math.max(1, limit - 1)).trim() + "…";
    }

    private static int accentFor(String phase) {
        switch (phase) {
            case "listening": return Color.rgb(52, 226, 183);
            case "thinking": return Color.rgb(154, 112, 255);
            case "speaking": return Color.rgb(76, 157, 255);
            case "intercom": return Color.rgb(255, 105, 140);
            case "music": return Color.rgb(86, 206, 255);
            case "error": return Color.rgb(255, 94, 94);
            case "setup": return Color.rgb(255, 198, 80);
            case "offline": return Color.rgb(255, 151, 69);
            default: return Color.rgb(73, 218, 169);
        }
    }

    private static String phaseLabel(String phase) {
        switch (phase) {
            case "thinking": return "THINKING";
            case "music": return "PLAYING";
            case "error": return "CHECK";
            case "setup": return "SETUP";
            case "offline": return "OFFLINE";
            default: return "TATER";
        }
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
