import asyncio
import base64
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import AsyncMock, patch

from aiohttp import FormData, web
from aiohttp.test_utils import TestClient, TestServer

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "server"))

import server


class FakeRequest:
    def __init__(self, payload=None, query=None, match_info=None):
        self.payload = payload or {}
        self.query = query or {}
        self.match_info = match_info or {}

    async def json(self):
        return self.payload


class FakeRemote:
    def __init__(self):
        self._remote_message_protocol = object()
        self.keys = []
        self.launched_apps = []

    def send_key_command(self, key):
        self.keys.append(key)

    def send_launch_app_command(self, package_id):
        self.launched_apps.append(package_id)

    def disconnect(self):
        self._remote_message_protocol = None


class MultiTvServerTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.original_registry_path = server.TV_REGISTRY_PATH
        self.original_data_dir = server.TV_DATA_DIR
        self.original_app_registry_path = server.APP_REGISTRY_PATH
        self.original_icon_data_dir = server.ICON_DATA_DIR
        data_directory = Path(self.temporary_directory.name) / "data"
        server.TV_REGISTRY_PATH = data_directory / "tvs.yaml"
        server.TV_DATA_DIR = data_directory / "tvs"
        server.APP_REGISTRY_PATH = data_directory / "apps.yaml"
        server.ICON_DATA_DIR = data_directory / "icons"
        server.config = {}
        server.tv_registry = {}
        server.app_registry = {}
        server.tv_states = {}
        server.server_events = {}
        server.server_event_futures = {}

    def tearDown(self):
        server.TV_REGISTRY_PATH = self.original_registry_path
        server.TV_DATA_DIR = self.original_data_dir
        server.APP_REGISTRY_PATH = self.original_app_registry_path
        server.ICON_DATA_DIR = self.original_icon_data_dir
        server.tv_registry = {}
        server.app_registry = {}
        server.tv_states = {}
        self.temporary_directory.cleanup()

    def response_json(self, response):
        return json.loads(response.text)

    def test_legacy_launchers_migrate_and_existing_tvs_enable_them(self):
        server.config = {
            "apps": [
                {"name": "Netflix", "id": "com.netflix.ninja", "icon": "mdi-netflix"},
                {"name": "YouTube", "id": "com.google.android.youtube.tv", "icon": "mdi-youtube"},
            ]
        }
        server.load_app_registry()
        app_ids = list(server.app_registry)
        self.assertEqual(len(app_ids), 2)
        self.assertTrue(server.APP_REGISTRY_PATH.exists())

        server.TV_REGISTRY_PATH.parent.mkdir(parents=True, exist_ok=True)
        server.TV_REGISTRY_PATH.write_text(
            "tvs:\n  - id: living\n    name: Living Room\n    host: 192.168.1.10\n"
        )
        server.load_tv_registry()
        self.assertEqual(server.tv_registry["living"]["app_ids"], app_ids)

        server.app_registry = {}
        server.save_app_registry()
        server.load_app_registry()
        self.assertEqual(server.app_registry, {})

    def test_icon_upload_validation_rejects_unsafe_or_disguised_files(self):
        with self.assertRaisesRegex(ValueError, "PNG, JPEG, WebP, or GIF"):
            server.validate_icon_bytes("image/svg+xml", b"<svg></svg>")
        with self.assertRaisesRegex(ValueError, "does not match"):
            server.validate_icon_bytes("image/png", b"not-a-png")
        with self.assertRaisesRegex(ValueError, "2 MB or smaller"):
            server.validate_icon_bytes("image/png", b"\x89PNG\r\n\x1a\n" + b"x" * server.MAX_ICON_BYTES)

    async def test_launcher_crud_icon_upload_and_tv_availability(self):
        app = web.Application(client_max_size=server.MAX_ICON_BYTES + 65536)
        app.router.add_put('/api/tvs/{tv_id}/apps', server.tv_apps_handler)
        app.router.add_get('/api/apps', server.apps_handler)
        app.router.add_post('/api/apps', server.add_app_handler)
        app.router.add_put('/api/apps/reorder', server.reorder_apps_handler)
        app.router.add_put('/api/apps/{app_id}', server.update_app_handler)
        app.router.add_delete('/api/apps/{app_id}', server.delete_app_handler)
        client = TestClient(TestServer(app))
        await client.start_server()
        try:
            png = base64.b64decode(
                "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
            )
            create_form = FormData()
            create_form.add_field('name', 'Netflix')
            create_form.add_field('package_id', 'com.netflix.ninja')
            create_form.add_field('icon_class', 'mdi-netflix')
            create_form.add_field(
                'icon_file', png, filename='netflix.png', content_type='image/png'
            )
            create_response = await client.post('/api/apps', data=create_form)
            self.assertEqual(create_response.status, 201)
            created = (await create_response.json())["app"]
            app_id = created["id"]
            self.assertTrue(created["has_uploaded_icon"])
            self.assertEqual(created["icon_class"], 'mdi-netflix')
            icon_path = server.ICON_DATA_DIR / server.app_registry[app_id]["icon_file"]
            self.assertTrue(icon_path.exists())

            update_form = FormData()
            update_form.add_field('name', 'Netflix TV')
            update_form.add_field('package_id', 'com.netflix.ninja')
            gif = base64.b64decode(
                "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="
            )
            update_form.add_field(
                'icon_file', gif, filename='netflix.gif', content_type='image/gif'
            )
            update_response = await client.put(f'/api/apps/{app_id}', data=update_form)
            self.assertEqual(update_response.status, 200)
            updated = (await update_response.json())["app"]
            self.assertEqual(updated["name"], 'Netflix TV')
            self.assertEqual(updated["icon_class"], 'mdi-netflix')
            replacement_icon_path = (
                server.ICON_DATA_DIR / server.app_registry[app_id]["icon_file"]
            )
            self.assertFalse(icon_path.exists())
            self.assertTrue(replacement_icon_path.exists())

            create_form2 = FormData()
            create_form2.add_field('name', 'YouTube')
            create_form2.add_field('package_id', 'com.google.android.youtube.tv')
            create_response2 = await client.post('/api/apps', data=create_form2)
            self.assertEqual(create_response2.status, 201)
            app_id2 = (await create_response2.json())["app"]["id"]

            reorder_response = await client.put(
                '/api/apps/reorder', json={"app_ids": [app_id2, app_id]}
            )
            self.assertEqual(reorder_response.status, 200)
            self.assertEqual(list(server.app_registry.keys()), [app_id2, app_id])

            server.tv_registry = {
                "living": {
                    "id": "living",
                    "name": "Living Room",
                    "host": "192.168.1.10",
                    "app_ids": [],
                }
            }
            availability_response = await client.put(
                '/api/tvs/living/apps', json={"app_ids": [app_id, app_id2]}
            )
            self.assertEqual(availability_response.status, 200)
            self.assertEqual(server.tv_registry["living"]["app_ids"], [app_id, app_id2])
            self.assertEqual([app["id"] for app in server.apps_for_tv("living")], [app_id, app_id2])

            delete_response = await client.delete(f'/api/apps/{app_id}')
            self.assertEqual(delete_response.status, 200)
            self.assertFalse(replacement_icon_path.exists())
            self.assertEqual(server.tv_registry["living"]["app_ids"], [app_id2])
            await client.delete(f'/api/apps/{app_id2}')
        finally:
            await client.close()

    async def test_launch_requires_launcher_to_be_enabled_for_selected_tv(self):
        server.app_registry = {
            "netflix": {
                "id": "netflix",
                "name": "Netflix",
                "package_id": "com.netflix.ninja",
                "icon": "",
                "icon_file": "",
            },
            "youtube": {
                "id": "youtube",
                "name": "YouTube",
                "package_id": "com.google.android.youtube.tv",
                "icon": "",
                "icon_file": "",
            },
        }
        server.tv_registry = {
            "living": {
                "id": "living",
                "name": "Living Room",
                "host": "192.168.1.10",
                "app_ids": ["netflix"],
            }
        }
        remote = FakeRemote()
        server.tv_states = {"living": server.TVState(remote=remote)}

        denied = await server.launch_app_handler(FakeRequest({
            "tv_id": "living", "launcher_id": "youtube"
        }))
        allowed = await server.launch_app_handler(FakeRequest({
            "tv_id": "living", "launcher_id": "netflix"
        }))

        self.assertEqual(denied.status, 403)
        self.assertEqual(allowed.status, 200)
        self.assertEqual(remote.launched_apps, ["com.netflix.ninja"])

    def test_legacy_tv_and_certificates_are_migrated_only_once(self):
        data_directory = server.TV_REGISTRY_PATH.parent
        data_directory.mkdir(parents=True)
        (data_directory / "cert.pem").write_text("certificate")
        (data_directory / "key.pem").write_text("key")
        server.config = {"tv_ip": "192.168.1.10", "tv_name": "Living Room"}

        server.load_tv_registry()

        self.assertEqual(len(server.tv_registry), 1)
        tv_id = next(iter(server.tv_registry))
        self.assertEqual(server.tv_registry[tv_id]["name"], "Living Room")
        self.assertEqual((server.TV_DATA_DIR / tv_id / "cert.pem").read_text(), "certificate")

        server.tv_registry = {}
        server.save_tv_registry()
        server.load_tv_registry()
        self.assertEqual(server.tv_registry, {})

    async def test_add_list_and_forget_tvs(self):
        first_response = await server.add_tv_handler(FakeRequest({
            "name": "Living Room",
            "host": "192.168.1.10",
        }))
        second_response = await server.add_tv_handler(FakeRequest({
            "name": "Bedroom",
            "host": "192.168.1.11",
        }))
        self.assertEqual(first_response.status, 201)
        self.assertEqual(second_response.status, 201)

        list_response = await server.tvs_handler(FakeRequest())
        listed_tvs = self.response_json(list_response)["tvs"]
        self.assertEqual([tv["name"] for tv in listed_tvs], ["Living Room", "Bedroom"])

        tv_id = self.response_json(first_response)["tv"]["id"]
        credential_directory = server.TV_DATA_DIR / tv_id
        credential_directory.mkdir(parents=True)
        (credential_directory / "cert.pem").write_text("certificate")
        (credential_directory / "key.pem").write_text("key")
        active_remote = FakeRemote()
        server.tv_states[tv_id] = server.TVState(remote=active_remote)
        forget_response = await server.forget_tv_handler(
            FakeRequest(match_info={"tv_id": tv_id})
        )
        self.assertEqual(forget_response.status, 200)
        self.assertNotIn(tv_id, server.tv_registry)
        self.assertNotIn(tv_id, server.tv_states)
        self.assertIsNone(active_remote._remote_message_protocol)
        self.assertFalse(credential_directory.exists())

    async def test_commands_are_routed_to_the_selected_tv(self):
        server.tv_registry = {
            "living": {"id": "living", "name": "Living", "host": "192.168.1.10"},
            "bedroom": {"id": "bedroom", "name": "Bedroom", "host": "192.168.1.11"},
        }
        living_remote = FakeRemote()
        bedroom_remote = FakeRemote()
        server.tv_states = {
            "living": server.TVState(remote=living_remote),
            "bedroom": server.TVState(remote=bedroom_remote),
        }

        response = await server.send_key_handler(FakeRequest({
            "tv_id": "bedroom",
            "key": "KEYCODE_HOME",
        }))

        self.assertEqual(response.status, 200)
        self.assertEqual(living_remote.keys, [])
        self.assertEqual(bedroom_remote.keys, ["KEYCODE_HOME"])

    async def test_two_tvs_pair_concurrently_with_separate_credentials(self):
        server.tv_registry = {
            "living": {"id": "living", "name": "Living", "host": "192.168.1.10"},
            "bedroom": {"id": "bedroom", "name": "Bedroom", "host": "192.168.1.11"},
        }

        class PairingRemote:
            instances = []

            def __init__(self, **kwargs):
                self.kwargs = kwargs
                self.connect_attempts = 0
                self.pairing_code = None
                self._remote_message_protocol = None
                self.__class__.instances.append(self)

            async def async_generate_cert_if_missing(self):
                return False

            async def async_connect(self):
                self.connect_attempts += 1
                if self.connect_attempts == 1:
                    raise server.InvalidAuth("Pairing required")
                self._remote_message_protocol = object()

            async def async_start_pairing(self):
                return None

            async def async_finish_pairing(self, code):
                self.pairing_code = code

        with patch.object(server, "CustomAndroidTVRemote", PairingRemote), \
                patch.object(server, "monitor_connection", AsyncMock()):
            living_task = asyncio.create_task(server.initialize_tv("living"))
            bedroom_task = asyncio.create_task(server.initialize_tv("bedroom"))
            for _ in range(20):
                if all(
                    server.tv_states.get(tv_id)
                    and server.tv_states[tv_id].pairing_code_future
                    for tv_id in ("living", "bedroom")
                ):
                    break
                await asyncio.sleep(0)

            await server.pairing_code_handler(FakeRequest({
                "tv_id": "living", "code": "LIVE01"
            }))
            await server.pairing_code_handler(FakeRequest({
                "tv_id": "bedroom", "code": "BED002"
            }))
            results = await asyncio.gather(living_task, bedroom_task)
            await asyncio.sleep(0)

        self.assertEqual(results, [{"status": "connected"}, {"status": "connected"}])
        remotes = {remote.kwargs["tv_id"]: remote for remote in PairingRemote.instances}
        self.assertEqual(remotes["living"].pairing_code, "LIVE01")
        self.assertEqual(remotes["bedroom"].pairing_code, "BED002")
        self.assertEqual(
            Path(remotes["living"].kwargs["certfile"]).parent,
            server.TV_DATA_DIR / "living",
        )
        self.assertEqual(
            Path(remotes["bedroom"].kwargs["certfile"]).parent,
            server.TV_DATA_DIR / "bedroom",
        )

    async def test_pairing_codes_are_scoped_to_the_selected_tv(self):
        server.tv_registry = {
            "living": {"id": "living", "name": "Living", "host": "192.168.1.10"},
            "bedroom": {"id": "bedroom", "name": "Bedroom", "host": "192.168.1.11"},
        }
        loop = asyncio.get_running_loop()
        living_code = loop.create_future()
        bedroom_code = loop.create_future()
        server.tv_states = {
            "living": server.TVState(pairing_in_progress=True, pairing_code_future=living_code),
            "bedroom": server.TVState(pairing_in_progress=True, pairing_code_future=bedroom_code),
        }

        response = await server.pairing_code_handler(FakeRequest({
            "tv_id": "bedroom",
            "code": "ABC123",
        }))

        self.assertEqual(response.status, 200)
        self.assertFalse(living_code.done())
        self.assertEqual(bedroom_code.result(), "ABC123")
        living_code.cancel()

    async def test_connect_endpoint_starts_selected_tv_automatically(self):
        server.tv_registry = {
            "living": {"id": "living", "name": "Living", "host": "192.168.1.10"},
        }
        connect_mock = AsyncMock(return_value={"status": "connected"})
        with patch.object(server, "initialize_tv", connect_mock):
            response = await server.connect_handler(FakeRequest({"tv_id": "living"}))
            await asyncio.sleep(0)

        self.assertEqual(response.status, 200)
        self.assertEqual(self.response_json(response)["status"], "connecting")
        connect_mock.assert_awaited_once_with("living")

    def test_get_server_port_priority(self):
        # Default with empty config and no env vars
        with patch.dict("os.environ", {}, clear=True):
            server.config = {}
            self.assertEqual(server.get_server_port(), 7503)

        # Config file port
        with patch.dict("os.environ", {}, clear=True):
            server.config = {"server_port": 8080}
            self.assertEqual(server.get_server_port(), 8080)

        # Environment variable overrides config
        with patch.dict("os.environ", {"SERVER_PORT": "9000"}, clear=True):
            server.config = {"server_port": 8080}
            self.assertEqual(server.get_server_port(), 9000)

        # PORT environment variable fallback
        with patch.dict("os.environ", {"PORT": "9001"}, clear=True):
            server.config = {"server_port": 8080}
            self.assertEqual(server.get_server_port(), 9001)

        # Fallback to default when invalid port provided
        with patch.dict("os.environ", {"SERVER_PORT": "invalid"}, clear=True):
            server.config = {}
            self.assertEqual(server.get_server_port(), 7503)

    async def test_pwa_entry_assets_disable_http_cache(self):
        app = web.Application()
        app.router.add_get('/', server.index_handler)
        for path in ('/app.js', '/sw.js', '/manifest.json', '/reset.html'):
            app.router.add_get(path, server.pwa_asset_handler)
        client = TestClient(TestServer(app))
        await client.start_server()
        try:
            for path in ('/', '/app.js', '/sw.js', '/manifest.json', '/reset.html'):
                response = await client.get(path)
                self.assertEqual(response.status, 200)
                self.assertEqual(response.headers['Cache-Control'], 'no-cache, no-store, must-revalidate')
        finally:
            await client.close()


if __name__ == "__main__":
    unittest.main()
