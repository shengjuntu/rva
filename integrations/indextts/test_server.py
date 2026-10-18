"""HTTP contract tests; fake engine only, no GPU/audio-quality claim."""
import io
import json
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
import wave

from server import MODEL_ID, SpeechServer, load_voices


class FakeEngine:
    def __init__(self):
        self.calls = []
        self.started = threading.Event()
        self.release = threading.Event()
        self.release.set()

    def infer(self, **options):
        self.calls.append(options)
        self.started.set()
        self.release.wait(5)
        with wave.open(options["output_path"], "wb") as output:
            output.setnchannels(1)
            output.setsampwidth(2)
            output.setframerate(22050)
            output.writeframes(b"\x00\x10" * 2205)


class ContractTests(unittest.TestCase):
    def setUp(self):
        self.engine = FakeEngine()
        self.server = SpeechServer(("127.0.0.1", 0), self.engine, {"news_zh": Path("reference.wav")}, "secret")
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base = "http://127.0.0.1:" + str(self.server.server_port)

    def tearDown(self):
        self.engine.release.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def request(self, path, body=None, token="secret"):
        req = urllib.request.Request(self.base + path, data=None if body is None else json.dumps(body).encode(), headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=5) as result:
                return result.status, result.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    def payload(self, **changes):
        data = {"model": MODEL_ID, "input": "这是一条新闻。", "voice": "news_zh", "speed": 1.25, "response_format": "wav", "extra_params": {"lang": "zh", "text_normalization": True}}
        data.update(changes)
        return data

    def test_models_auth_and_wav_contract(self):
        self.assertEqual(self.request("/v1/models", token="wrong")[0], 401)
        self.assertEqual(json.loads(self.request("/v1/models")[1])["data"][0]["id"], MODEL_ID)
        code, data = self.request("/v1/audio/speech", self.payload())
        self.assertEqual(code, 200)
        with wave.open(io.BytesIO(data)) as audio:
            self.assertEqual(audio.getframerate(), 22050)
        self.assertEqual(self.engine.calls[0]["lang"], "ZH")
        self.assertAlmostEqual(self.engine.calls[0]["duration_factor"], 0.8)
        self.assertFalse(Path(self.engine.calls[0]["output_path"]).exists())
        self.assertNotIn(b"reference.wav", self.request("/v1/audio/voices")[1])

    def test_invalid_inputs_never_reach_engine(self):
        for body in [[], self.payload(speed=True), self.payload(speed=float("nan")), self.payload(voice="/etc/passwd"), self.payload(ref_audio="file:///etc/passwd"), self.payload(response_format="mp3"), self.payload(extra_params={"lang":"zhen"}), self.payload(model="other"), self.payload(input="")]:
            self.assertEqual(self.request("/v1/audio/speech", body)[0], 422)
        self.assertFalse(self.engine.calls)

    def test_gpu_worker_serializes_requests(self):
        self.engine.release.clear()
        response = []
        worker = threading.Thread(target=lambda: response.append(self.request("/v1/audio/speech", self.payload())))
        worker.start()
        try:
            self.assertTrue(self.engine.started.wait(2))
            self.assertTrue(json.loads(self.request("/health")[1])["busy"])
            self.assertEqual(self.request("/v1/audio/speech", self.payload())[0], 503)
        finally:
            self.engine.release.set()
            worker.join(5)
        self.assertEqual(response[0][0], 200)
        self.assertEqual(len(self.engine.calls), 1)

    def test_voice_config_resolves_relative_to_json(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "reference.wav").write_bytes(b"test")
            config = root / "voices.json"
            config.write_text('{"news_zh":"reference.wav"}')
            self.assertEqual(load_voices(config)["news_zh"], root / "reference.wav")
            config.write_text('{"news_zh":"missing.wav"}')
            with self.assertRaises(ValueError):
                load_voices(config)


if __name__ == "__main__":
    unittest.main()
