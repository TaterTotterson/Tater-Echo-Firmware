package com.tatertotterson.show;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public final class ShowStateTest {
    @Test
    public void parsesCompleteSnapshotAndBoundsMeters() throws Exception {
        ShowState state = ShowState.fromJson("{"
                + "\"protocol\":1,\"type\":\"snapshot\","
                + "\"phase\":\"listening\",\"connected\":true,"
                + "\"device_name\":\"Kitchen Show\",\"room\":\"Kitchen\","
                + "\"muted\":false,\"volume_percent\":180,\"audio_level\":-1,"
                + "\"direction_degrees\":92.5,\"timer_active\":true,"
                + "\"tater_time_unix_ms\":1799999999000,"
                + "\"tater_utc_offset_seconds\":-18000,\"tater_timezone\":\"CST\","
                + "\"media\":{\"title\":\"Song\",\"artist\":\"Artist\"},"
                + "\"weather\":{\"temperature_text\":\"74°\",\"temperature_unit\":\"F\","
                + "\"condition\":\"Partly cloudy\",\"condition_kind\":\"partly\","
                + "\"feels_like_text\":\"Feels like 72°\",\"feels_like_relation\":\"cooler\",\"humidity_text\":\"43%\","
                + "\"wind_text\":\"SE 8 mph\",\"rain_text\":\"0.2 in/hr\",\"lightning_text\":\"2\","
                + "\"source\":\"WeatherAPI.com\","
                + "\"indoor_temperature_text\":\"70°\",\"indoor_humidity_text\":\"39%\"},"
                + "\"notification\":{\"id\":\"event-1\",\"camera_name\":\"Front Door\","
                + "\"description\":\"A package was delivered.\",\"image_url\":\"http://127.0.0.1/image\","
                + "\"expires_at_unix_ms\":1800000000000}}"
        );
        assertEquals("listening", state.phase);
        assertTrue(state.connected);
        assertEquals(100, state.volumePercent);
        assertEquals(0f, state.audioLevel, 0f);
        assertEquals(92.5f, state.directionDegrees, 0f);
        assertTrue(state.timerActive);
        assertEquals("Song", state.mediaTitle);
        assertEquals("Artist", state.mediaArtist);
        assertEquals("74°", state.weather.temperatureText);
        assertEquals("Partly cloudy", state.weather.condition);
        assertEquals("cooler", state.weather.feelsLikeRelation);
        assertEquals("43%", state.weather.humidityText);
        assertEquals("SE 8 mph", state.weather.windText);
        assertEquals("0.2 in/hr", state.weather.rainText);
        assertEquals("2", state.weather.lightningText);
        assertEquals("70°", state.weather.indoorTemperatureText);
        assertEquals("39%", state.weather.indoorHumidityText);
        assertEquals("Front Door", state.notification.cameraName);
        assertEquals("A package was delivered.", state.notification.description);
        assertEquals(1799999999000L, state.taterTimeUnixMs);
        assertEquals(-18000, state.taterUtcOffsetSeconds);
        assertEquals("CST", state.taterTimezone);
    }

    @Test
    public void rejectsWrongProtocolAndMessageType() {
        assertThrows(Exception.class, () -> ShowState.fromJson(
                "{\"protocol\":2,\"type\":\"snapshot\"}"));
        assertThrows(Exception.class, () -> ShowState.fromJson(
                "{\"protocol\":1,\"type\":\"command\"}"));
    }

    @Test
    public void disconnectRetainsUserStateButStopsActivity() {
        ShowState before = new ShowState(
                "speaking", true, "Show", "Office", "Replying", true,
                42, .7f, 180f, false, "", "");
        ShowState after = before.disconnected();
        assertEquals("offline", after.phase);
        assertFalse(after.connected);
        assertTrue(after.muted);
        assertEquals(42, after.volumePercent);
        assertEquals(0f, after.audioLevel, 0f);
        assertEquals(null, after.directionDegrees);
    }

    @Test
    public void missingWeatherKeepsOrbFallback() throws Exception {
        ShowState state = ShowState.fromJson(
                "{\"protocol\":1,\"type\":\"snapshot\",\"phase\":\"idle\"}");
        assertEquals(null, state.weather);
    }

    @Test
    public void parsesToolCallPresentation() throws Exception {
        ShowState state = ShowState.fromJson("{"
                + "\"protocol\":1,\"type\":\"snapshot\",\"connected\":true,"
                + "\"phase\":\"tool_call\",\"tool_name\":\"room_vision\","
                + "\"tool_message\":\"I’m taking a quick look now.\"}");
        assertEquals("tool_call", state.phase);
        assertEquals("room_vision", state.toolName);
        assertEquals("I’m taking a quick look now.", state.toolMessage);
        assertEquals("Room Vision", TaterShowView.toolDisplayName(state.toolName));
    }

    @Test
    public void connectingScreenTracksRealControllerStateAndNotSetup() {
        assertTrue(ShowState.waiting().isControllerConnecting());
        assertTrue(new ShowState(
                "offline", false, "Show", "Office", "Connecting to Tater", false,
                50, 0f, null, false, "", "").isControllerConnecting());
        assertFalse(ShowState.setup("Tater-Setup-Test").isControllerConnecting());
        assertFalse(new ShowState(
                "idle", true, "Show", "Office", "Ready when you are", false,
                50, 0f, null, false, "", "").isControllerConnecting());
    }

    @Test
    public void greetingFollowsLocalTimeOfDay() {
        assertEquals("Good night", ShowState.timeGreeting(4));
        assertEquals("Good morning", ShowState.timeGreeting(5));
        assertEquals("Good morning", ShowState.timeGreeting(11));
        assertEquals("Good afternoon", ShowState.timeGreeting(12));
        assertEquals("Good afternoon", ShowState.timeGreeting(16));
        assertEquals("Good evening", ShowState.timeGreeting(17));
        assertEquals("Good evening", ShowState.timeGreeting(20));
        assertEquals("Good night", ShowState.timeGreeting(21));
        assertEquals("Good night", ShowState.timeGreeting(24));
    }

    @Test
    public void intercomCornerHitTargetFollowsVisibleCircle() {
        assertTrue(TaterShowView.pointInCircle(40f, 430f, 65f, 490f, 100f));
        assertTrue(TaterShowView.pointInCircle(120f, 450f, 65f, 490f, 100f));
        assertFalse(TaterShowView.pointInCircle(260f, 430f, 65f, 490f, 100f));
        assertFalse(TaterShowView.pointInCircle(65f, 490f, 0f, 0f, 0f));
    }

    @Test
    public void compactVoiceOrbReplacesWeatherActivityLineOnlyWhenUseful() {
        assertTrue(TaterShowView.shouldDrawCompactVoiceOrb("listening", true));
        assertTrue(TaterShowView.shouldDrawCompactVoiceOrb("thinking", true));
        assertTrue(TaterShowView.shouldDrawCompactVoiceOrb("tool_call", true));
        assertTrue(TaterShowView.shouldDrawCompactVoiceOrb("speaking", true));
        assertTrue(TaterShowView.shouldDrawCompactVoiceOrb("intercom", true));
        assertFalse(TaterShowView.shouldDrawCompactVoiceOrb("idle", true));
        assertFalse(TaterShowView.shouldDrawCompactVoiceOrb("speaking", false));
    }

    @Test
    public void compactVoiceOrbAudioEnergyIsBoundedAndResponsive() {
        assertEquals(0f, TaterShowView.compactOrbAudioEnergy(-1f), 0f);
        assertEquals(.44f, TaterShowView.compactOrbAudioEnergy(.2f), .001f);
        assertEquals(1f, TaterShowView.compactOrbAudioEnergy(1f), 0f);
        assertEquals(.070f, TaterShowView.compactOrbDistortion(0f), .001f);
        assertEquals(.250f, TaterShowView.compactOrbDistortion(1f), .001f);
    }
}
