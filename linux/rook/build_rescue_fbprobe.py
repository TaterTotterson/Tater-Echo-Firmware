#!/usr/bin/env python3
"""Build the pinned TECHO5 rescue renderer for Rook's upright round panel.

Only a Go source overlay is changed; the TECHO5 checkout stays untouched.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


SOURCE_SHA256 = "bf915b9f295e6c4fb5a402dc22709c464615b944f6ceea8f0825fdee8942ce72"
PALETTE = {
    "bg := color.RGBA{0x1c, 0x15, 0x11, 0xff}":
        "bg := color.RGBA{0x11, 0x09, 0x05, 0xff}",
    "amber := color.RGBA{0xe9, 0xa2, 0x3b, 0xff}":
        "amber := color.RGBA{0xff, 0x84, 0x30, 0xff}",
    "paper := color.RGBA{0xe8, 0xdc, 0xc8, 0xff}":
        "paper := color.RGBA{0xed, 0xf3, 0xfa, 0xff}",
    "clockScale, color.RGBA{0xe8, 0xdc, 0xc8, 0xff})":
        "clockScale, color.RGBA{0xed, 0xf3, 0xfa, 0xff})",
}

SPOT_COMPOSE = r'''
// composeSpot keeps every important element inside the Spot's round glass.
// Its square framebuffer is already upright, unlike the Show 5's portrait panel.
func composeSpot(w, h int, bg color.RGBA, title, lines string) (*image.RGBA, image.Rectangle, int, int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	amber := color.RGBA{0xff, 0x84, 0x30, 0xff}
	paper := color.RGBA{0xed, 0xf3, 0xfa, 0xff}
	muted := color.RGBA{0xa7, 0xb4, 0xc7, 0xff}
	cx, cy := w/2, h/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x-cx, y-cy
			distance := dx*dx + dy*dy
			if distance >= 221*221 && distance <= 223*223 {
				img.SetRGBA(x, y, color.RGBA{0x70, 0x3d, 0x22, 0xff})
			}
		}
	}
	eyebrow := "TATER LINUX"
	text(img, eyebrow, (w-7*len(eyebrow)*2)/2, 75, 2, amber)
	titleScale := 3
	text(img, title, (w-7*len(title)*titleScale)/2, 119, titleScale, amber)
	draw.Draw(img, image.Rect(101, 177, w-101, 179), image.NewUniform(color.RGBA{0x70, 0x3d, 0x22, 0xff}), image.Point{}, draw.Src)
	for index, row := range strings.Split(lines, "|") {
		if index >= 4 {
			break
		}
		row = strings.TrimSpace(row)
		if len(row) > 29 {
			row = row[:29]
		}
		if row != "" {
			text(img, row, (w-7*len(row)*2)/2, 200+index*36, 2, paper)
		}
	}
	clock := time.Now().Format("15:04:05")
	clockScale := 3
	clockX := (w-7*len(clock)*clockScale)/2
	clockY := 374
	text(img, clock, clockX, clockY, clockScale, muted)
	return img, image.Rect(clockX, clockY, clockX+7*len(clock)*clockScale+3, clockY+14*clockScale), clockY, clockScale
}

'''


def replace_once(source: str, before: str, after: str) -> str:
    if source.count(before) != 1:
        raise ValueError(f"pinned fbprobe marker changed: {before[:75]!r}")
    return source.replace(before, after, 1)


def rook_source(source: bytes) -> bytes:
    if hashlib.sha256(source).hexdigest() != SOURCE_SHA256:
        raise ValueError("fbprobe source is not the pinned TECHO5 revision")
    result = source.decode()
    for before, after in PALETTE.items():
        result = replace_once(result, before, after)
    result = replace_once(
        result,
        "draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)\n\n\tamber :=",
        "draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)\n"
        "\tif w == h && title != \"\" {\n\t\treturn composeSpot(w, h, bg, title, lines)\n\t}\n\n\tamber :=",
    )
    result = replace_once(result, "func main() {", SPOT_COMPOSE + "func main() {")
    result = replace_once(
        result,
        "\t\t\tpx, py := panelW-1-y, x\n",
        "\t\t\tpx, py := panelW-1-y, x\n"
        "\t\t\tif panelW == panelH {\n\t\t\t\tpx, py = x, y\n\t\t\t}\n",
    )
    result = replace_once(
        result,
        'text(img, time.Now().Format("15:04:05"), 40, clockY, clockScale,',
        'text(img, time.Now().Format("15:04:05"), clockAt.Min.X, clockY, clockScale,',
    )
    result = replace_once(
        result,
        'clockAt.Min.X, clockY, clockScale, color.RGBA{0xed, 0xf3, 0xfa, 0xff})',
        'clockAt.Min.X, clockY, clockScale, color.RGBA{0xa7, 0xb4, 0xc7, 0xff})',
    )
    return result.encode()


def build(checkout: Path, output: Path, goarch: str = "arm") -> None:
    if goarch not in {"arm", "amd64"}:
        raise ValueError("rescue renderer architecture must be arm or amd64")
    source = (checkout / "cmd/fbprobe/main.go").resolve(strict=True)
    themed = rook_source(source.read_bytes())
    output.parent.mkdir(parents=True, exist_ok=True)
    output = output.resolve()
    with tempfile.TemporaryDirectory(prefix="tater-rook-fbprobe-") as directory:
        substitute = Path(directory) / "main.go"
        substitute.write_bytes(themed)
        overlay = Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(substitute)}}))
        env = os.environ.copy()
        env.update({"GOOS": "linux", "GOARCH": goarch, "CGO_ENABLED": "0"})
        if goarch == "arm":
            env["GOARM"] = "7"
        subprocess.run(
            ["go", "build", "-trimpath", "-ldflags", "-s -w", "-overlay", str(overlay),
             "-o", str(output), "./cmd/fbprobe"],
            cwd=checkout, env=env, check=True,
        )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("checkout", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--goarch", choices=("arm", "amd64"), default="arm")
    args = parser.parse_args()
    build(args.checkout, args.output, args.goarch)
    print(f"Upright Tater Rook rescue renderer: {args.output}")
