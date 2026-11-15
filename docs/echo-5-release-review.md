# Echo 5.0.0 release acceptance

Date: 2026-11-15. Accepted for release preparation after the owner's explicit
decision to review the remaining paths instead of performing more device tests.
This is not a claim that every planned hardware test passed, or that a release
has already been built or published.

## Evidence and accepted scope

| Area | Completed evidence | Accepted without another device test |
| --- | --- | --- |
| Biscuit | Two independent older-firmware USB upgrades; settings, permanent hardware gain/filter, voice, privacy, controls, early TWRP and return checks | Extended acoustic/headroom soak; final fleet OTA/rollback |
| Checkers, Cronos, Crown, Athena | Settings-preserving full USB installs, automatic device-local runtime setup, committed root slots, GPU/voice/controls/privacy, hardware video, teardown and physical recovery/return checks; Athena Sendspin audio | Final fleet OTA/rollback and extended soak |
| Rook | Corrected full USB reinstall on the development unit; complete settings/runtime/firmware/boot read-backs; normal health commit/restart; Rook/GPU UI, voice, interruption, preview and bounded real hardware call | Untouched older-Rook migration, sustained call/audio/privacy and final installed cold-recovery/return retest |
| Radar | Shared Dot installer, settings preservation, exact Radar board/kernel/profile and early-recovery paths reviewed; actual candidate verifies, and its kernel rejects Biscuit identity | Physical Radar USB, voice/privacy/gain/filter/headroom and recovery checks |
| OTA after the v5 USB migration | Application and system updater code, atomic application activation, inactive-root staging, retained vendor/runtime state, trial/health/rollback policy and target/platform/hash checks reviewed; host race/regression tests pass | Final on-device application/System OTA and induced-rollback trials |

The older-Rook path retains its strict published-boot allowlist, exact partition
map, complete pre-erasure backups and verified settings preservation. The
development unit's one-time boot preparation was separately authorized and
does not stand in for an untouched older-firmware migration test.

Radar uses its own pinned kernel and board identity, not a substituted Biscuit
image. Its shared four-ADC gain/filter code verifies all channels, rejects
unexpected controls, restores partial gain/filter writes on failure, and only
skips software filtering after the versioned hardware status is complete.
This is code-review acceptance, not measured Radar acoustic performance.

The actual pinned v4.2.3 updater rejects all 21 new-format combinations (seven
targets × application/bridge/system), without staging or restarting. Every
pre-v5 device still requires the one-time full matching USB installation.
Post-migration System OTA contains only the root filesystem: it does not change
the kernel, rescue image, bootloader or hardware platform. Future incompatible
kernel/runtime changes require a separately reviewed delivery strategy.

## Acceptance and remaining publication work

No blocking issue was identified in this review. The owner accepts the listed
physical-test gaps. The platform locks' `release_qualified` flags now mean
**release packaging approved under this recorded scope**, not all planned tests
performed. Exact source/kernel/helper/runtime hashes, board restrictions,
package schema and installed-platform checks remain enforced; none are bypassed.
USB-only candidate archives remain marked unqualified and are not rewritten.

The final release must still build from the accepted source, pass its normal CI
and artifact verification, and include the one-time USB migration instructions.
No firmware push, final tag or release publication is part of this review.
Private settings, credentials, device identities, vendor binaries, media and
backups remain outside source and public artifacts.
