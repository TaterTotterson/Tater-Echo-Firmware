# Crown home-screen layout concepts

2026-11-14. Two image-generation previews for the larger 1280×800 workspace;
not renderer screenshots or installed UI changes. Built-in image generation
was used, not the fallback CLI. Existing working firmware/UI is unchanged.

- [A — balanced clock and weather](crown-home-balanced-v1.png): recommended.
  Uses the panel width, keeps the date clear and avoids a large empty right side.
- [B — centered ambient clock](crown-home-centered-v1.png): quieter alternative,
  with the photo and clock taking priority over the weather.

Both use the main Tater app's warm grey palette and existing supported fields:
configured assistant name, connection dot, room, optional indoor temperature,
clock/date, current outdoor weather, feels-like and selected environment slots.
The sample names, temperatures and times are illustrative, not a device export.
No new forecast feeds or widgets are implied. Disabled fields must collapse
without placeholders, and weather/conditions must disappear together. Preserve
theme support, 12/24-hour clocks, rotating photos and tap-anywhere menu access;
no top bar, menu button or swipe hint. STT, media and notification overlays
remain separate states. Implementation should use the existing scene cache and
GPU fades, not animate/rebuild static home text every frame.

## Requested classic-layout alternative

The owner subsequently requested the original non-GPU lower-right arrangement,
retaining the current GPU version's styling and sizes. These two approved
renders reuse the actual current font faces, 96px clock, 58px outline weather
art, theme colors and native wallpaper. Weather text remains 64px on Show 8
and 48px on Show 5. Date and room move below the clock/weather line; the green
status dot and unlabelled indoor temperature stay beside the room, and optional
readings align along the bottom. Weather conditions are shown only once.

- [Show 8, 1280×800](home-classic-layout-gpu-style-show8-v1-1280x800.png)
- [Show 5, 960×480](home-classic-layout-gpu-style-show5-v1-960x480.png)

These were originally rendered through a test-only Go overlay, not AI-generated
redraws. The owner approved this classic direction instead of B. Production
rendering now matches these previews pixel-for-pixel and is installed in the
Athena development application. Other devices receive it in their next build;
no source or release has been pushed.

## Prompt A (built-in image generation)

Use case: ui-mockup. Asset type: preview-only high fidelity Crown Tater satellite home screen, not an implemented UI. Create one polished flat UI screenshot in a landscape 16:10 aspect ratio corresponding to a 1280 by 800 smart display. Full bleed screen only, no hardware bezel, no perspective, no surrounding canvas, no labels outside the screen. This is OPTION A: balanced editorial ambient layout for a larger screen. Match a warm neutral charcoal grey Tater application aesthetic (#111213, warm white #f1eee8, secondary warm grey #a8a096, restrained burnt orange #d65a1f accents). Absolutely not blue-tinted UI. Background is a beautiful photographic alpine mountain and forest at a muted warm sunset, low contrast dark veil for excellent type legibility; keep scenery visible, not a flat black dashboard. No solid top bar and no cards. At upper left with 54px-equivalent margins show the small heading exactly 'Tater' followed by a small green online status dot, then 'Kitchen  72°' on the same line. The 72° is optional indoor temperature adjacent to room, no label. Large elegant sans serif time '9:41' at left, about 210px equivalent, balanced vertically around the middle; modest 'AM' immediately right of digits. Under time show 'Saturday, November 14'. At the right half place a refined thin-line partly-cloudy weather symbol with warm orange sun and white cloud, alongside large '68°'; below it show 'Partly cloudy' then 'Feels like 66°'. Keep beautiful breathing room, intentional balanced composition spanning the whole wider panel. A single subtle unboxed line near the bottom across the width shows optional supported sensors exactly 'Room humidity 42%   ·   Humidity 55%   ·   Wind NW 5 mph   ·   Rain 0.02 in'. Use readable 26-30px equivalent footer, not tiny text. Do not duplicate weather condition or feels like in this footer. No extra measurements, no forecasts, no navigation, no settings icons, no menu button, no swipe arrow, no tap hint, no microphone icon, no greeting, no made-up widgets, no media. Tap anywhere opens the existing menu but do not depict that instruction. All typography precise, unwarped, warm off-white and professional. A feasible elegant home screen, not science-fiction concept art. Preserve the requested exact words and degree symbols.

## Prompt B (built-in image generation)

Use case: ui-mockup. Asset type: preview-only high-fidelity larger-screen Tater Crown home screen, not implemented UI. Create ONE flat front-facing UI screenshot, full-bleed landscape 16:10 composition matching a 1280 by 800 panel. No hardware bezel, no camera perspective, no external labels or presentation canvas. OPTION B: a calm centered ambient clock layout, distinct from a split clock/weather dashboard. Grey neutral Tater design system: warm white #f1eee8 typography, warm grey #a8a096 secondary text, restrained orange #d65a1f weather accent, no blue UI. Photographic background: peaceful misty alpine lake with charcoal-grey forest silhouettes, mountain ridge and a subtle peach dawn reflected in still water. Soft dark veil to support crisp text. No large solid panels, no cards, no top bar. At upper left on the photo, with 54px-equivalent margins, small light type exactly 'Tater' then a small green online dot then 'Kitchen  72°'; 72° is the indoor temperature beside room, not labelled. Center the very large elegant thin-to-regular sans-serif time '9:41' horizontally, about 230px-equivalent; a smaller 'AM' aligned with time baseline. Under it, centered 'Saturday, November 14' with ample spacing. Beneath date, centered one quiet horizontal group: thin orange-and-white partly-cloudy outline icon, '68°', then 'Partly cloudy · Feels like 66°', with sensible hierarchy: temperature about 56px, condition about 27px. Near bottom center, a single clean unboxed sensor line exactly 'Room humidity 42%   ·   Humidity 55%   ·   Wind NW 5 mph'. All only optional existing sensor fields, no invented forecasts or widgets. Beautiful negative space preserving the landscape, comfortable readable typography from across a room, no content overlap, no repeated temperatures except indoor/outdoor/feels-like each once. No navigation, no footer buttons, no menu icon, no tap hint, no swipe arrow, no microphone icon, no top toolbar, no greeting, no media, no extra measurements. Tap anywhere menu behavior is unchanged and must NOT be written on screen. Render exact text carefully. A realistic tasteful shippable flat UI visual, not sci-fi.
