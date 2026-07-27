import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


class VersioningTests(unittest.TestCase):
    def test_client_files_use_version_placeholder(self):
        """All client files must use __VERSION__ instead of a hard-coded version."""
        version = (ROOT / "VERSION").read_text().strip()
        files_with_placeholder = [
            "client/sw.js",
            "client/app.js",
            "client/index.html",
            "client/manifest.json",
            "client/reset.html",
        ]
        for rel_path in files_with_placeholder:
            source = (ROOT / rel_path).read_text()
            self.assertIn(
                "__VERSION__", source,
                f"{rel_path} must contain __VERSION__ placeholder"
            )
            self.assertNotIn(
                version, source,
                f"{rel_path} must not contain the hard-coded version '{version}'"
            )


if __name__ == "__main__":
    unittest.main()
