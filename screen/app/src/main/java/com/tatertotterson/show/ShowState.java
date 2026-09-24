package com.tatertotterson.show;

import org.json.JSONObject;

final class ShowState {
    static final int PROTOCOL_VERSION = 1;

    final String phase;
    final boolean connected;
    final String deviceName;
    final String room;
    final String message;
    final boolean muted;
    final int volumePercent;
    final float audioLevel;
    final Float directionDegrees;
    final boolean timerActive;
    final String mediaTitle;
    final String mediaArtist;

    ShowState(
            String phase,
            boolean connected,
            String deviceName,
            String room,
            String message,
            boolean muted,
            int volumePercent,
            float audioLevel,
            Float directionDegrees,
            boolean timerActive,
            String mediaTitle,
            String mediaArtist) {
        this.phase = normalizedPhase(phase);
        this.connected = connected;
        this.deviceName = clean(deviceName, "Tater Show");
        this.room = clean(room, "Unassigned room");
        this.message = clean(message, defaultMessage(this.phase, connected));
        this.muted = muted;
        this.volumePercent = clamp(volumePercent, 0, 100);
        this.audioLevel = Math.max(0f, Math.min(1f, audioLevel));
        this.directionDegrees = directionDegrees;
        this.timerActive = timerActive;
        this.mediaTitle = clean(mediaTitle, "");
        this.mediaArtist = clean(mediaArtist, "");
    }

    static ShowState waiting() {
        return new ShowState(
                "offline", false, "Tater Show", "Echo Show 5",
                "Waiting for the Tater satellite service", false, 50, 0f,
                null, false, "", "");
    }

    static ShowState fromJson(String wire) throws Exception {
        JSONObject root = new JSONObject(wire);
        int protocol = root.optInt("protocol", -1);
        if (protocol != PROTOCOL_VERSION || !"snapshot".equals(root.optString("type"))) {
            throw new IllegalArgumentException("unsupported Tater Show message");
        }
        JSONObject media = root.optJSONObject("media");
        Float direction = root.has("direction_degrees") && !root.isNull("direction_degrees")
                ? (float) root.getDouble("direction_degrees") : null;
        return new ShowState(
                root.optString("phase", "idle"),
                root.optBoolean("connected", false),
                root.optString("device_name", "Tater Show"),
                root.optString("room", "Unassigned room"),
                root.optString("message", ""),
                root.optBoolean("muted", false),
                root.optInt("volume_percent", 50),
                (float) root.optDouble("audio_level", 0),
                direction,
                root.optBoolean("timer_active", false),
                media == null ? "" : media.optString("title", ""),
                media == null ? "" : media.optString("artist", ""));
    }

    ShowState disconnected() {
        return new ShowState(
                "offline", false, deviceName, room,
                "Waiting for the Tater satellite service", muted, volumePercent,
                0f, null, timerActive, mediaTitle, mediaArtist);
    }

    private static String normalizedPhase(String value) {
        switch (value == null ? "" : value.trim().toLowerCase()) {
            case "idle":
            case "listening":
            case "thinking":
            case "speaking":
            case "intercom":
            case "music":
            case "error":
            case "setup":
            case "offline":
                return value.trim().toLowerCase();
            default:
                return "idle";
        }
    }

    private static String defaultMessage(String phase, boolean connected) {
        if (!connected || "offline".equals(phase)) return "Connecting to Tater";
        switch (phase) {
            case "listening": return "I’m listening";
            case "thinking": return "Thinking";
            case "speaking": return "Replying";
            case "intercom": return "Intercom is live";
            case "music": return "Now playing";
            case "setup": return "Ready to set up";
            case "error": return "Needs attention";
            default: return "Ready when you are";
        }
    }

    private static String clean(String value, String fallback) {
        if (value == null) return fallback;
        String trimmed = value.trim();
        return trimmed.isEmpty() ? fallback : trimmed;
    }

    private static int clamp(int value, int low, int high) {
        return Math.max(low, Math.min(high, value));
    }
}
