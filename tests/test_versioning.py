import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


class VersioningTests(unittest.TestCase):
    def test_service_worker_cache_matches_project_version(self):
        version = (ROOT / "VERSION").read_text().strip()
        service_worker = (ROOT / "static/sw.js").read_text()
        cache_line = next(line for line in service_worker.splitlines() if line.startswith("const CACHE_NAME"))
        cache_name = cache_line.split("=", 1)[1].strip().rstrip(";").strip(chr(39) + chr(34))
        self.assertEqual(cache_name, f"droidtv-remote-v{version}")


if __name__ == "__main__":
    unittest.main()
