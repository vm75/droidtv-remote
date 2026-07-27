import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent


class NginxConfigurationTests(unittest.TestCase):
    def test_remote_location_disables_browser_cache(self):
        nginx_config = (ROOT / "deploy/nginx_subfolder.example").read_text()
        remote_location = nginx_config.split(
            "location ^~ /remote/ {", 1
        )[1].split("# Service worker", 1)[0]
        self.assertIn(
            'add_header Cache-Control "no-cache, no-store, must-revalidate" always;',
            remote_location,
        )

    def test_subdomain_example_proxies_root_without_browser_cache(self):
        nginx_config = (ROOT / "deploy/nginx_subdomain.example").read_text()
        self.assertIn("server_name remote.example.com;", nginx_config)
        self.assertIn("location / {", nginx_config)
        self.assertIn("proxy_pass http://127.0.0.1:7503;", nginx_config)
        self.assertIn(
            'add_header Cache-Control "no-cache, no-store, must-revalidate" always;',
            nginx_config,
        )


