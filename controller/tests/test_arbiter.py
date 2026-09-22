import asyncio

from em_arbiter import WakeArbiter

WINDOW = 0.2


def run(coro):
    return asyncio.run(coro)


def test_solo_device_wins_immediately():
    """The winner must not wait. The old design awaited the full window on
    every wake (~364ms measured in the field, on every device) — that is
    the regression this redesign exists to remove."""
    async def main():
        arb = WakeArbiter()
        loop = asyncio.get_running_loop()
        t0 = loop.time()
        won = arb.claim("office", WINDOW)
        return won, loop.time() - t0
    won, elapsed = run(main())
    assert won == "office"
    assert elapsed < 0.01


def test_second_device_in_window_loses():
    async def main():
        arb = WakeArbiter()
        first = arb.claim("office", WINDOW)
        second = arb.claim("lounge", WINDOW)
        return first, second
    first, second = run(main())
    assert first == "office"
    assert second == "office"   # lounge told to stand down


def test_three_way_only_first_answers():
    """The 2026-07-20 field case: three devices within 184ms."""
    async def main():
        arb = WakeArbiter()
        return [arb.claim(d, WINDOW) for d in ("office", "lounge", "retreat")]
    assert run(main()) == ["office"] * 3


def test_loser_never_waits_either():
    async def main():
        arb = WakeArbiter()
        arb.claim("office", WINDOW)
        loop = asyncio.get_running_loop()
        t0 = loop.time()
        arb.claim("lounge", WINDOW)
        return loop.time() - t0
    assert run(main()) < 0.01


def test_claim_expires_after_window():
    async def main():
        arb = WakeArbiter()
        arb.claim("office", 0.05)
        await asyncio.sleep(0.08)
        return arb.claim("lounge", 0.05)
    assert run(main()) == "lounge"


def test_same_device_rewaking_is_not_suppressed():
    """A device answering twice in a row is a real second utterance in the
    room it already serves — it must never be told it lost to itself."""
    async def main():
        arb = WakeArbiter()
        arb.claim("office", WINDOW)
        return arb.claim("office", WINDOW)
    assert run(main()) == "office"


def test_release_frees_the_claim_early():
    async def main():
        arb = WakeArbiter()
        arb.claim("office", 10.0)   # long window
        arb.release("office")
        return arb.claim("lounge", 10.0)
    assert run(main()) == "lounge"


def test_release_by_non_holder_is_ignored():
    async def main():
        arb = WakeArbiter()
        arb.claim("office", 10.0)
        arb.release("lounge")       # not the holder — must be a no-op
        return arb.claim("retreat", 10.0)
    assert run(main()) == "office"


def test_window_rearms_on_each_win():
    """Successive utterances in the same room keep extending suppression,
    so a distant device echoing the winner's TTS stays quiet."""
    async def main():
        arb = WakeArbiter()
        arb.claim("office", 0.15)
        await asyncio.sleep(0.10)
        arb.claim("office", 0.15)   # re-arms from now
        await asyncio.sleep(0.10)
        return arb.claim("lounge", 0.15)
    assert run(main()) == "office"


def test_zero_window_suppresses_nothing():
    async def main():
        arb = WakeArbiter()
        arb.claim("office", 0.0)
        return arb.claim("lounge", 0.0)
    assert run(main()) == "lounge"


# ── capture time (docs/listening.md, "Arbitration") ─────────────────────────

def test_late_claim_heard_first_still_cedes_to_the_granted_one():
    """A granted claim is never revoked, even to an Echo that heard first."""
    async def main():
        arb = WakeArbiter()
        now = asyncio.get_running_loop().time()
        first = arb.claim("office", WINDOW, heard_at=now)
        second = arb.claim("lounge", WINDOW, heard_at=now - 0.05)
        return first, second
    assert run(main()) == ("office", "office")


def test_claim_delayed_in_flight_cedes_within_slack():
    """The bug this fixes: heard 50ms after the winner, arriving a second
    late, it used to fall outside the window and start a second answer."""
    async def main():
        arb = WakeArbiter()
        loop = asyncio.get_running_loop()
        t = loop.time()
        arb.claim("office", WINDOW, heard_at=t)
        await asyncio.sleep(0.4)            # past the window by arrival
        late = arb.claim("lounge", WINDOW, heard_at=t + 0.05, slack_s=1.0)
        return late
    assert run(main()) == "office"


def test_without_slack_a_late_claim_is_a_new_utterance():
    async def main():
        arb = WakeArbiter()
        t = asyncio.get_running_loop().time()
        arb.claim("office", WINDOW, heard_at=t)
        await asyncio.sleep(0.4)
        return arb.claim("lounge", WINDOW, heard_at=t + 0.05)
    assert run(main()) == "lounge"


def test_separate_utterance_heard_outside_the_window_wins():
    async def main():
        arb = WakeArbiter()
        t = asyncio.get_running_loop().time()
        arb.claim("office", WINDOW, heard_at=t - 1.0, slack_s=3.0)
        return arb.claim("lounge", WINDOW, heard_at=t, slack_s=3.0)
    assert run(main()) == "lounge"


# ── a mixed fleet: one Echo wakes itself, one is scored here ────────────────

def _mixed_pair(date_by_capture: bool) -> str:
    """Office wakes on the Echo; Lounge streams and is scored here. Both heard
    the same utterance at the same instant, but Lounge's crossing frame was
    held 1s in a retransmit and then waited behind a backlog — the two things
    measured on the bench, 2026-09-22."""
    import em_listen

    async def main():
        arb = WakeArbiter()
        loop = asyncio.get_running_loop()
        t = loop.time()
        clock = em_listen.CaptureClock()
        # Lounge's stream up to the wake: on time, a few ms of transit.
        for n in range(40):
            clock.observe(n, t - (40 - n) * 0.08 + 0.004)
        # Office's wake arrives 150ms after capture, reporting its age.
        arb.claim("office", 0.3, heard_at=em_listen.heard_at(t, 110, 80),
                  slack_s=1.0)
        # Lounge's crossing frame (captured at t) arrives a second late...
        await asyncio.sleep(1.0)
        arrived = loop.time()
        frame = em_listen.Frame(b"\0" * 2560, arrived, clock.observe(40, arrived))
        # ...and waits another 0.5s to be scored.
        await asyncio.sleep(0.5)
        heard = em_listen.captured(frame, 0) if date_by_capture else loop.time()
        return arb.claim("lounge", 0.3, heard_at=heard, slack_s=3.0)
    return run(main())


def test_mixed_fleet_late_controller_wake_cedes():
    assert _mixed_pair(date_by_capture=True) == "office"


def test_mixed_fleet_timed_at_scoring_answered_twice():
    """What stamping the claim at the end of inference did: the delay read
    as a separate utterance and the second Echo answered too."""
    assert _mixed_pair(date_by_capture=False) == "lounge"
