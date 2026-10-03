#!/usr/bin/env python3
"""Serve deterministic, all-zero WAV streams for non-audible device tests.

Endpoints:
  /short.wav       0.5 seconds of silence
  /background.wav  Full-length silence without a network stall
  /stall.wav       Silence with a mid-stream stall that forces an underrun
  /healthz         Plain-text readiness response

The PCM payload contains only zero samples. This makes the diagnostic safe to
run without audible device output even before the client-side volume is set to
zero.
"""

from __future__ import annotations

import argparse
import io
import time
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


SAMPLE_RATE = 48_000


def silent_wav(seconds: float) -> bytes:
    frames = max(1, int(round(seconds * SAMPLE_RATE)))
    output = io.BytesIO()
    with wave.open(output, "wb") as wav_file:
        wav_file.setnchannels(1)
        wav_file.setsampwidth(2)
        wav_file.setframerate(SAMPLE_RATE)
        wav_file.writeframes(b"\x00\x00" * frames)
    return output.getvalue()


class SilentStreamHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "TaterSilentStream/1.0"

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self._send(b"ok\n", "text/plain; charset=utf-8")
            return
        if path == "/short.wav":
            self._send(self.server.short_wav, "audio/wav")
            return
        if path == "/background.wav":
            self._send(self.server.full_wav, "audio/wav")
            return
        if path != "/stall.wav":
            self.send_error(404)
            return

        body = self.server.full_wav
        # The canonical 44-byte PCM WAV header is followed by mono S16 frames.
        split = min(len(body), 44 + int(self.server.prefill_seconds * SAMPLE_RATE) * 2)
        self.send_response(200)
        self.send_header("Content-Type", "audio/wav")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        try:
            self.wfile.write(body[:split])
            self.wfile.flush()
            print(
                f"stall.wav: sent {self.server.prefill_seconds:.2f}s, "
                f"stalling {self.server.stall_seconds:.2f}s",
                flush=True,
            )
            time.sleep(self.server.stall_seconds)
            self.wfile.write(body[split:])
            self.wfile.flush()
            print("stall.wav: stream resumed and completed", flush=True)
        except (BrokenPipeError, ConnectionResetError):
            print("stall.wav: client disconnected", flush=True)

    def _send(self, body: bytes, content_type: str) -> None:
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, message: str, *args: object) -> None:
        print(f"{self.client_address[0]} - {message % args}", flush=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bind", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8765)
    parser.add_argument("--duration", type=float, default=30.0)
    parser.add_argument("--prefill", type=float, default=2.2)
    parser.add_argument("--stall", type=float, default=8.0)
    args = parser.parse_args()

    server = ThreadingHTTPServer((args.bind, args.port), SilentStreamHandler)
    server.short_wav = silent_wav(0.5)
    server.full_wav = silent_wav(args.duration)
    server.prefill_seconds = max(0.1, args.prefill)
    server.stall_seconds = max(0.0, args.stall)
    print(
        f"silent stream server listening on http://{args.bind}:{args.port} "
        f"({args.duration:.1f}s stream, {args.prefill:.1f}s prefill, {args.stall:.1f}s stall)",
        flush=True,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
