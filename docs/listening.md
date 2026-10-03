# On-device listening sessions

The local wake path keeps ambient microphone audio on the satellite. Audio
leaves the device only for an accepted voice turn, an explicitly enabled
trainer sample, or a user-initiated intercom action.

## States

The compatibility gate in `device/internal/listen` has three states:

| State | Behavior |
|---|---|
| `stream` | Continuous wake-stream transport for a peer that does not negotiate local sessions |
| `local` | Score locally and keep audio private until a numbered session opens |
| `degraded` | Local listening was required but no working scorer is available; do not silently fall back to streaming |

Tater-native mode uses active local wake and its normal acknowledged voice-turn
transport. The numbered gate remains for negotiated peers and for the privacy
boundary shared by older device control paths.

## Session flow

1. The satellite continuously retains a bounded two-second PCM ring.
2. A local wake opens a nonzero session ID.
3. Pre-roll and new audio are tagged with that session.
4. The peer acknowledges or closes that exact session.
5. Late messages for an older session are ignored.

The frame format is:

```text
[0x07][session u32 big-endian][sequence u16 big-endian][16 kHz mono PCM]
```

Session IDs prevent control and audio messages crossing on separate sockets
from opening, closing, or accepting the wrong turn.

## Device-enforced limits

The limits live on the satellite so a lost packet, network partition, or
server failure cannot leave the microphone streaming indefinitely:

- recent-audio ring: 2 seconds;
- acknowledgement timeout: 3 seconds; and
- absolute open-session limit: 30 seconds.

Mute closes the open session immediately. Unacknowledged sessions close with
`ack_timeout`; acknowledged sessions still cannot exceed `max_open`. These
rules are pure state behind a mutex and are covered without microphone, socket,
or clock dependencies.

## Failure policy

Privacy mode is fail-closed: if local wake is required but the model/runtime
cannot start, the satellite reports degraded state and keeps ambient audio
local. The action button or Checkers on-screen intercom remains an explicit
user gesture and can still start its own bounded turn.
