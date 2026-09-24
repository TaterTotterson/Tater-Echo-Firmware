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
                + "\"media\":{\"title\":\"Song\",\"artist\":\"Artist\"}}"
        );
        assertEquals("listening", state.phase);
        assertTrue(state.connected);
        assertEquals(100, state.volumePercent);
        assertEquals(0f, state.audioLevel, 0f);
        assertEquals(92.5f, state.directionDegrees, 0f);
        assertTrue(state.timerActive);
        assertEquals("Song", state.mediaTitle);
        assertEquals("Artist", state.mediaArtist);
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
}
