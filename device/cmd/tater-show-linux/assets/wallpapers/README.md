# Bundled landscape additions

Three original landscape assets generated with the built-in image tool on
2026-11-12 for the satellite UI/background request. No stock photos or
device-proprietary image assets were used for these additions.

The seven older scenes are unchanged. New assets are appended in
`wallpapers.go`; existing rotation settings and uploaded photos still apply.

## Firmware assets

| Scene | Checkers / Cronos | Crown / Athena / Rook source |
| --- | --- | --- |
| alpine-sunset | [960×480 JPEG](checkers/alpine-sunset.jpg) | [1280×800 JPEG](crown/alpine-sunset.jpg) |
| misty-lake | [960×480 JPEG](checkers/misty-lake.jpg) | [1280×800 JPEG](crown/misty-lake.jpg) |
| desert-dusk | [960×480 JPEG](checkers/desert-dusk.jpg) | [1280×800 JPEG](crown/desert-dusk.jpg) |

Final files are native-sized JPEGs (quality 88). Generated 1774×887 masters
remain in the image tool's generated-images directory. For firmware packaging,
Checkers copies are resampled proportionally to 960×480; Crown copies are
resampled proportionally to 1600×800 then center-cropped to 1280×800.
This is asset sizing/encoding only; the compositions are generated artwork.
The existing renderer handles Rook's centered square cover and round mask.

## Generation prompts and master identities

### alpine-sunset

Master: `exec-820066a7-2d0a-4a4b-88ec-3ea0f94de7d4.png`  
SHA-256: `95cec46d8079780eabce38347c65037f1e2e8a164ff64479428143ab30ffc952`

```text
Standalone firmware wallpaper, photorealistic cinematic landscape photograph, no text, no logos, no UI, no border, no people. Wide landscape 2:1 composition with important scenery concentrated in central 60 percent so a centered round-screen crop also looks good. Restrained natural colors, rich charcoal shadows, readable quiet negative space on left for a white clock. Not blue-tinted. Output a single full-bleed landscape image at 1920 by 960 if possible. Snow-capped rugged alpine mountain centered in distance, receding dark evergreen forest ridges in foreground, glowing copper-orange sunset horizon and delicate muted rose cloud streaks, deep neutral slate sky. Beautiful peaceful real landscape, inspired by the mountain sunset in our Tater mockup. Horizon roughly 55 percent down, no exaggerated sci-fi elements.
```

### misty-lake

Master: `exec-73ae565b-efad-4f59-8503-2ee92fea54e0.png`  
SHA-256: `0e3ffe5967aaaae0f1e734d169a2d4c5ea2f029a12081019ffe4f0ca47b91e33`

```text
Standalone firmware wallpaper, photorealistic cinematic landscape photograph, no text, no logos, no UI, no border, no people. Wide landscape 2:1 composition with important scenery concentrated in central 60 percent so a centered round-screen crop also looks good. Restrained natural colors, rich charcoal shadows, readable quiet negative space on left for a white clock. Not blue-tinted. Output a single full-bleed landscape image at 1920 by 960 if possible. Still alpine lake at early dawn, mist weaving through charcoal pine forest, distant soft mountains and a muted warm amber light at horizon. Lake reflections, low-contrast soft grey upper sky, delicately warm landscape, not saturated. Calm elegant atmosphere.
```

### desert-dusk

Master: `exec-387462eb-809a-4bac-a337-b0afa40206f2.png`  
SHA-256: `58deda1fb95765034a020714edf16efbfb38966cea6549973803e4ccc7a9f5fe`

```text
Standalone firmware wallpaper, photorealistic cinematic landscape photograph, no text, no logos, no UI, no border, no people. Wide landscape 2:1 composition with important scenery concentrated in central 60 percent so a centered round-screen crop also looks good. Restrained natural colors, rich charcoal shadows, readable quiet negative space on left for a white clock. Not blue-tinted. Output a single full-bleed landscape image at 1920 by 960 if possible. Sculpted desert dunes at dusk, graceful layered sand ridges with muted terracotta and copper rim lighting, distant charcoal rocky mesas, expansive warm-grey twilight sky. A natural beautiful quiet landscape photograph, subtle orange sunset glow on horizon, neutral shadows, no oversaturation.
```


