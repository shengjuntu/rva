#!/usr/bin/env python3
"""Small HTTP adapter for an existing official IndexTTS-2.5 installation.

Run with that installation's Python/uv environment. Reuses existing weights;
upstream initialization may resolve missing auxiliary models. No environment
installation, Gradio automation, or shell commands are performed here.
"""
import argparse
import hmac
import json
import logging
import math
import os
import re
from pathlib import Path
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL_ID = "IndexTeam/IndexTTS-2.5"
MAX_AUDIO = 32 * 1024 * 1024
LANGUAGES = {"zh": "ZH", "en": "EN", "ja": "JA", "es": "ES", "ar": "AR"}


class SpeechServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, engine, voices, token="", model_id=MODEL_ID):
        self.engine = engine
        self.voices = voices
        self.token = token
        self.model_id = model_id
        self.gpu_slot = threading.Lock()
        super().__init__(address, Handler)


class Handler(BaseHTTPRequestHandler):
    # HTTP/1.0 closes connections; errors cannot leave unread request bytes for
    # another request. Do not log query strings, bodies, tokens or voice paths.
    server_version = "IndexTTS-VideoAdapter/0.8.0"

    def setup(self):
        super().setup()
        self.connection.settimeout(20)

    def log_message(self, fmt, *args):
        pass

    def respond(self, status, data, content_type="application/json"):
        body = json.dumps(data, ensure_ascii=False).encode() if isinstance(data, dict) else data
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def authorized(self):
        expected = "Bearer " + self.server.token
        if self.server.token and not hmac.compare_digest(
            self.headers.get("Authorization", "").encode(), expected.encode()
        ):
            self.respond(401, {"error": "Invalid API key"})
            return False
        return True

    def do_GET(self):
        if not self.authorized():
            return
        if self.path == "/health":
            self.respond(200, {"ready": True, "busy": self.server.gpu_slot.locked()})
        elif self.path == "/v1/models":
            self.respond(200, {"object": "list", "data": [{"id": self.server.model_id, "object": "model"}]})
        elif self.path == "/v1/audio/voices":
            self.respond(200, {"voices": sorted(self.server.voices)})
        else:
            self.respond(404, {"error": "Unknown endpoint"})

    def parse_request_data(self):
        if self.headers.get_content_type() != "application/json":
            raise ValueError("Content-Type must be application/json")
        if self.headers.get("Transfer-Encoding"):
            raise ValueError("Chunked request bodies are not supported")
        length = int(self.headers.get("Content-Length", "0"))
        if not 0 < length <= 65536:
            raise ValueError("Request must be 1..65536 bytes")
        data = self.rfile.read(length)
        if len(data) != length:
            raise ValueError("Incomplete request")
        req = json.loads(data)
        if not isinstance(req, dict):
            raise ValueError("JSON object required")
        if set(req) - {"model", "input", "voice", "speed", "response_format", "extra_params"}:
            raise ValueError("Unsupported speech options")
        if req.get("model") != self.server.model_id:
            raise ValueError("Unknown model")
        text = req.get("input")
        if not isinstance(text, str) or not text.strip() or len(text) > 2000:
            raise ValueError("input must contain 1..2000 characters")
        voice = req.get("voice")
        if not isinstance(voice, str) or voice not in self.server.voices:
            raise ValueError("Unknown voice; register it in voices.json")
        if req.get("response_format", "wav") != "wav":
            raise ValueError("Only WAV output is supported")
        speed = req.get("speed", 1.0)
        if isinstance(speed, bool) or not isinstance(speed, (int, float)) or not math.isfinite(speed) or not 0.5 <= speed <= 2:
            raise ValueError("speed must be 0.5..2.0")
        extra = req.get("extra_params", {})
        if not isinstance(extra, dict) or set(extra) - {"lang", "text_normalization"}:
            raise ValueError("Unsupported extra_params")
        lang = extra.get("lang", "zh")
        if not isinstance(lang, str) or lang not in LANGUAGES:
            raise ValueError("lang must be zh/en/ja/es/ar; zhen preprocessing requires vLLM-Omni")
        if extra.get("text_normalization", True) is not True:
            raise ValueError("The native adapter uses the upstream text normalization")
        return text, voice, speed, LANGUAGES[lang]

    def do_POST(self):
        if not self.authorized():
            return
        if self.path != "/v1/audio/speech":
            self.respond(404, {"error": "Unknown endpoint"})
            return
        try:
            text, voice, speed, lang = self.parse_request_data()
        except (ValueError, TypeError, UnicodeError) as error:
            self.respond(422, {"error": str(error)})
            return
        if not self.server.gpu_slot.acquire(blocking=False):
            self.respond(503, {"error": "GPU worker busy; retry after current inference completes"})
            return
        try:
            # A disconnected caller cannot reliably interrupt upstream CUDA
            # inference. Keep the slot until infer returns; never run concurrently.
            with tempfile.TemporaryDirectory(prefix="indextts-video-") as tmp:
                output = Path(tmp) / "speech.wav"
                self.server.engine.infer(
                    spk_audio_prompt=str(self.server.voices[voice]),
                    text=text, lang=lang, output_path=str(output),
                    duration_factor=1.0 / speed, use_random=False, verbose=False,
                )
                if not output.is_file() or not 44 <= output.stat().st_size <= MAX_AUDIO:
                    raise RuntimeError("Invalid audio output size")
                audio = output.read_bytes()
                if audio[:4] != b"RIFF" or audio[8:12] != b"WAVE":
                    raise RuntimeError("Model did not produce WAV")
            self.respond(200, audio, "audio/wav")
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            logging.info("Caller disconnected; completed output discarded")
        except Exception:
            logging.exception("IndexTTS inference failed")
            try:
                self.respond(500, {"error": "IndexTTS inference failed; check service log"})
            except OSError:
                pass
        finally:
            self.server.gpu_slot.release()


def load_voices(path):
    path = Path(path).resolve()
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict) or not data:
        raise ValueError("voices.json must map voice IDs to existing reference audio files")
    result = {}
    for name, value in data.items():
        if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.+\-]{0,127}", name):
            raise ValueError("Voice ID and path must be nonempty strings")
        reference = (path.parent / value).resolve()
        if not reference.is_file():
            raise ValueError("Missing reference audio for voice " + name)
        result[name] = reference
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True, help="Existing official index-tts checkout")
    parser.add_argument("--model-dir", required=True, help="Existing IndexTTS-2.5 checkpoints directory")
    parser.add_argument("--voices", required=True, help="JSON: voice ID -> reference audio path")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8092)
    parser.add_argument("--model-id", default=MODEL_ID)
    parser.add_argument("--fp32", action="store_true", help="Disable BF16")
    args = parser.parse_args()
    repo, models = Path(args.repo).resolve(), Path(args.model_dir).resolve()
    voices = load_voices(args.voices)
    if not (repo / "indextts/infer_v2_5.py").is_file():
        parser.error("--repo must contain indextts/infer_v2_5.py")
    if not (models / "config.yaml").is_file():
        parser.error("--model-dir must contain IndexTTS-2.5 config.yaml")
    token = os.environ.get("INDEXTTS_API_KEY", "")
    if args.host not in {"127.0.0.1", "localhost"} and not token:
        parser.error("Set INDEXTTS_API_KEY when listening on a non-loopback address")
    logging.basicConfig(level=logging.INFO)
    sys.path.insert(0, str(repo))
    os.chdir(repo)
    from indextts.infer_v2_5 import IndexTTS2
    engine = IndexTTS2(cfg_path=str(models / "config.yaml"), model_dir=str(models), use_bf16=not args.fp32)
    server = SpeechServer((args.host, args.port), engine, voices, token, args.model_id)
    logging.info("IndexTTS loaded; listening on %s:%d with %d voices", args.host, args.port, len(voices))
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
