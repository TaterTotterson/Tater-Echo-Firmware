import hashlib
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import build_boot
import build_rescue_fbprobe


class RookBootScreenTests(unittest.TestCase):
    def test_rescue_init_starts_usb_before_upright_install_screen(self):
        original = b"#!/bin/sh\nMODE=android\n" + build_boot.OLD_SCREEN + b"\n" + build_boot.OLD_CONSOLE
        with patch.object(build_boot, "SPOT_INIT_SHA256", hashlib.sha256(original).hexdigest()):
            branded = build_boot.branded_init(original)
        self.assertLess(branded.index(b"t5_usb_acm"), branded.index(b"/usr/local/bin/fbprobe -hold"))
        self.assertIn(b"INSTALLING TATER", branded)
        self.assertIn(b"TATER RECOVERY", branded)
        self.assertNotIn(build_boot.OLD_SCREEN, branded)
        with tempfile.TemporaryDirectory() as directory:
            script = Path(directory) / "init"
            script.write_bytes(branded)
            subprocess.run(["sh", "-n", str(script)], check=True)

    def test_rescue_init_rejects_changed_upstream(self):
        with self.assertRaises(SystemExit):
            build_boot.branded_init(b"different init")

    def test_fbprobe_source_has_round_layout_and_direct_square_blit(self):
        original = (
            "bg := color.RGBA{0x1c, 0x15, 0x11, 0xff}\n"
            "draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)\n\n"
            "\tamber := color.RGBA{0xe9, 0xa2, 0x3b, 0xff}\n"
            "paper := color.RGBA{0xe8, 0xdc, 0xc8, 0xff}\n"
            "func main() {\n"
            "\t\t\tpx, py := panelW-1-y, x\n"
            'text(img, time.Now().Format("15:04:05"), 40, clockY, clockScale, '
            "color.RGBA{0xe8, 0xdc, 0xc8, 0xff})\n"
        ).encode()
        with patch.object(build_rescue_fbprobe, "SOURCE_SHA256", hashlib.sha256(original).hexdigest()):
            themed = build_rescue_fbprobe.rook_source(original).decode()
        self.assertIn("func composeSpot(", themed)
        self.assertIn('if w == h && title != ""', themed)
        self.assertIn("if panelW == panelH {\n\t\t\t\tpx, py = x, y", themed)
        self.assertIn("TATER LINUX", themed)
        self.assertIn("clockAt.Min.X", themed)


if __name__ == "__main__":
    unittest.main()
