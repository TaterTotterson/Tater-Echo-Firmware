package com.tatertotterson.show;

import org.json.JSONObject;

final class ShowState {
    static final int PROTOCOL_VERSION = 1;

    static final class Weather {
        final String temperatureText;
        final String temperatureUnit;
        final String indoorTemperatureText;
        final String indoorHumidityText;
        final String condition;
        final String conditionKind;
        final String feelsLikeText;
        final String feelsLikeRelation;
        final String humidityText;
        final String windText;
        final String rainText;
        final String lightningText;
        final String source;
        final boolean stale;

        Weather(
                String temperatureText,
                String temperatureUnit,
                String condition,
                String conditionKind,
                String feelsLikeText,
                String feelsLikeRelation,
                String humidityText,
                String windText,
                String source,
                boolean stale,
                String indoorTemperatureText,
                String indoorHumidityText,
                String rainText,
                String lightningText) {
            this.temperatureText = clean(temperatureText, "");
            this.temperatureUnit = clean(temperatureUnit, "");
            this.indoorTemperatureText = clean(indoorTemperatureText, "");
            this.indoorHumidityText = clean(indoorHumidityText, "");
            this.condition = clean(condition, "Current conditions");
            this.conditionKind = clean(conditionKind, "partly").toLowerCase();
            this.feelsLikeText = clean(feelsLikeText, "");
            this.feelsLikeRelation = clean(feelsLikeRelation, "same").toLowerCase();
            this.humidityText = clean(humidityText, "");
            this.windText = clean(windText, "");
            this.rainText = clean(rainText, "");
            this.lightningText = clean(lightningText, "");
            this.source = clean(source, "Environment Core");
            this.stale = stale;
        }

        static Weather fromJson(JSONObject value) {
            if (value == null) return null;
            return new Weather(
                    value.optString("temperature_text", ""),
                    value.optString("temperature_unit", ""),
                    value.optString("condition", "Current conditions"),
                    value.optString("condition_kind", "partly"),
                    value.optString("feels_like_text", ""),
                    value.optString("feels_like_relation", "same"),
                    value.optString("humidity_text", ""),
                    value.optString("wind_text", ""),
                    value.optString("source", "Environment Core"),
                    value.optBoolean("stale", false),
                    value.optString("indoor_temperature_text", ""),
                    value.optString("indoor_humidity_text", ""),
                    value.optString("rain_text", ""),
                    value.optString("lightning_text", ""));
        }
    }

    static final class Notification {
        final String id;
        final String kind;
        final String priority;
        final String title;
        final String cameraName;
        final String description;
        final String imageUrl;
        final long expiresAtUnixMs;

        Notification(String id, String kind, String priority, String title,
                     String cameraName, String description, String imageUrl,
                     long expiresAtUnixMs) {
            this.id = clean(id, "notification");
            this.kind = clean(kind, "notification");
            this.priority = clean(priority, "normal");
            this.title = clean(title, "Tater Awareness");
            this.cameraName = clean(cameraName, this.title);
            this.description = clean(description, "Awareness detected activity.");
            this.imageUrl = clean(imageUrl, "");
            this.expiresAtUnixMs = Math.max(0L, expiresAtUnixMs);
        }

        static Notification fromJson(JSONObject value) {
            if (value == null) return null;
            return new Notification(
                    value.optString("id", "notification"),
                    value.optString("kind", "notification"),
                    value.optString("priority", "normal"),
                    value.optString("title", "Tater Awareness"),
                    value.optString("camera_name", ""),
                    value.optString("description", ""),
                    value.optString("image_url", ""),
                    value.optLong("expires_at_unix_ms", 0L));
        }
    }

    final String phase;
    final boolean connected;
    final String deviceName;
    final String room;
    final String message;
    final String toolName;
    final String toolMessage;
    final boolean muted;
    final int volumePercent;
    final float audioLevel;
    final Float directionDegrees;
    final boolean timerActive;
    final String mediaTitle;
    final String mediaArtist;
    final Weather weather;
    final Notification notification;
    final long taterTimeUnixMs;
    final int taterUtcOffsetSeconds;
    final String taterTimezone;

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
        this(phase, connected, deviceName, room, message, muted, volumePercent,
                audioLevel, directionDegrees, timerActive, mediaTitle, mediaArtist, null);
    }

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
            String mediaArtist,
            Weather weather) {
        this(phase, connected, deviceName, room, message, muted, volumePercent,
                audioLevel, directionDegrees, timerActive, mediaTitle, mediaArtist,
                weather, 0L, 0, "");
    }

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
            String mediaArtist,
            Weather weather,
            long taterTimeUnixMs,
            int taterUtcOffsetSeconds,
            String taterTimezone) {
        this(phase, connected, deviceName, room, message, muted, volumePercent,
                audioLevel, directionDegrees, timerActive, mediaTitle, mediaArtist,
                weather, null, taterTimeUnixMs, taterUtcOffsetSeconds, taterTimezone);
    }

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
            String mediaArtist,
            Weather weather,
            Notification notification,
            long taterTimeUnixMs,
            int taterUtcOffsetSeconds,
            String taterTimezone) {
        this(phase, connected, deviceName, room, message, muted, volumePercent,
                audioLevel, directionDegrees, timerActive, mediaTitle, mediaArtist,
                weather, notification, taterTimeUnixMs, taterUtcOffsetSeconds,
                taterTimezone, "", "");
    }

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
            String mediaArtist,
            Weather weather,
            Notification notification,
            long taterTimeUnixMs,
            int taterUtcOffsetSeconds,
            String taterTimezone,
            String toolName,
            String toolMessage) {
        this.phase = normalizedPhase(phase);
        this.connected = connected;
        this.deviceName = clean(deviceName, "Tater Show");
        this.room = clean(room, "Unassigned room");
        this.message = clean(message, defaultMessage(this.phase, connected));
        this.toolName = clean(toolName, "");
        this.toolMessage = clean(toolMessage, "");
        this.muted = muted;
        this.volumePercent = clamp(volumePercent, 0, 100);
        this.audioLevel = Math.max(0f, Math.min(1f, audioLevel));
        this.directionDegrees = directionDegrees;
        this.timerActive = timerActive;
        this.mediaTitle = clean(mediaTitle, "");
        this.mediaArtist = clean(mediaArtist, "");
        this.weather = weather;
        this.notification = notification;
        this.taterTimeUnixMs = Math.max(0L, taterTimeUnixMs);
        this.taterUtcOffsetSeconds = Math.max(-64800, Math.min(64800, taterUtcOffsetSeconds));
        this.taterTimezone = clean(taterTimezone, "Tater");
    }

    static ShowState waiting() {
        return new ShowState(
                "offline", false, "Tater Show", "Echo Show 5",
                "Waiting for the Tater satellite service", false, 50, 0f,
                null, false, "", "");
    }

    static ShowState setup(String network) {
        return new ShowState(
                "setup", false, clean(network, "Tater-Setup-Echo"), "192.168.4.1",
                "Open Tater, choose Satellites, then Add Satellite", false, 50, 0f,
                null, false, "", "");
    }

    static ShowState fromJson(String wire) throws Exception {
        JSONObject root = new JSONObject(wire);
        int protocol = root.optInt("protocol", -1);
        if (protocol != PROTOCOL_VERSION || !"snapshot".equals(root.optString("type"))) {
            throw new IllegalArgumentException("unsupported Tater Show message");
        }
        JSONObject media = root.optJSONObject("media");
        JSONObject weather = root.optJSONObject("weather");
        JSONObject notification = root.optJSONObject("notification");
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
                media == null ? "" : media.optString("artist", ""),
                Weather.fromJson(weather),
                Notification.fromJson(notification),
                root.optLong("tater_time_unix_ms", 0L),
                root.optInt("tater_utc_offset_seconds", 0),
                root.optString("tater_timezone", "Tater"),
                root.optString("tool_name", ""),
                root.optString("tool_message", ""));
    }

    ShowState disconnected() {
        return new ShowState(
                "offline", false, deviceName, room,
                "Waiting for the Tater satellite service", muted, volumePercent,
                0f, null, timerActive, mediaTitle, mediaArtist, weather,
                notification,
                taterTimeUnixMs, taterUtcOffsetSeconds, taterTimezone);
    }

    boolean isControllerConnecting() {
        return !connected && !"setup".equals(phase);
    }

    static String timeGreeting(int hour) {
        int normalizedHour = ((hour % 24) + 24) % 24;
        if (normalizedHour >= 5 && normalizedHour < 12) return "Good morning";
        if (normalizedHour >= 12 && normalizedHour < 17) return "Good afternoon";
        if (normalizedHour >= 17 && normalizedHour < 21) return "Good evening";
        return "Good night";
    }

    private static String normalizedPhase(String value) {
        switch (value == null ? "" : value.trim().toLowerCase()) {
            case "idle":
            case "listening":
            case "thinking":
            case "tool_call":
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
            case "tool_call": return "Working on that now";
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
