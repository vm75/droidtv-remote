"""
droidtv-remote Server
A local web server that hosts a remote control UI for Android TV.
Uses simple HTTP requests instead of WebSockets.
"""
import asyncio
from dataclasses import dataclass
import hashlib
import logging
import os
import re
import secrets
import shutil
import ssl
from pathlib import Path
from typing import Dict, List, Optional
import yaml
from aiohttp import web
from androidtvremote2 import AndroidTVRemote, ConnectionClosed, CannotConnect, InvalidAuth
from androidtvremote2.remote import RemoteProtocol, RemoteMessage, Feature

logging.basicConfig(level=logging.INFO)
logging.getLogger("aiohttp.access").setLevel(logging.WARNING)
logger = logging.getLogger(__name__)

# Global state
config = {}
TV_REGISTRY_PATH = Path(__file__).parent / "data" / "tvs.yaml"
TV_DATA_DIR = Path(__file__).parent / "data" / "tvs"
APP_REGISTRY_PATH = Path(__file__).parent / "data" / "apps.yaml"
ICON_DATA_DIR = Path(__file__).parent / "data" / "icons"
MAX_ICON_BYTES = 2 * 1024 * 1024
ALLOWED_ICON_TYPES = {
    "image/png": "png",
    "image/jpeg": "jpg",
    "image/webp": "webp",
    "image/gif": "gif",
}
tv_registry: Dict[str, dict] = {}
app_registry: Dict[str, dict] = {}


@dataclass
class TVState:
    """Runtime connection state for one configured TV."""

    remote: Optional['CustomAndroidTVRemote'] = None
    connecting: bool = False
    pairing_in_progress: bool = False
    pairing_code_future: Optional[asyncio.Future] = None
    connect_task: Optional[asyncio.Task] = None


tv_states: Dict[str, TVState] = {}

# Read version
try:
    with open(Path(__file__).parent / "VERSION", "r") as f:
        __version__ = f.read().strip()
except Exception:
    __version__ = "unknown"

# Event queues for long-polling, scoped to the TV selected by each browser.
server_events: Dict[str, list] = {}
server_event_futures: Dict[str, List[asyncio.Future]] = {}


def broadcast_event(event_type, data=None, tv_id=None):
    """Broadcast an event to clients waiting on the matching TV."""
    event_key = tv_id or ""
    event = {"type": event_type, "data": data, "tv_id": tv_id}
    waiting_futures = server_event_futures.get(event_key, [])
    if waiting_futures:
        for future in waiting_futures:
            if not future.done():
                future.set_result(event)
        server_event_futures[event_key] = []
    else:
        server_events[event_key] = [event]


class CustomRemoteProtocol(RemoteProtocol):
    """Custom protocol to intercept IME events"""

    def __init__(self, *args, tv_id=None, **kwargs):
        self.tv_id = tv_id
        super().__init__(*args, **kwargs)

    def _handle_message(self, raw_msg: bytes) -> None:
        """Handle a message from the server."""
        # Parse the message first to check for ime_show_request
        try:
            msg = RemoteMessage()
            msg.ParseFromString(raw_msg)

            if msg.HasField("remote_ime_show_request"):
                logger.info("IME Show Request detected!")
                info = msg.remote_ime_show_request.remote_text_field_status
                logger.debug(f"IME Info: value='{info.value}', label='{info.label}'")

                # Broadcast event to frontend
                broadcast_event("ime_show", {
                    "value": info.value,
                    "label": info.label,
                    "start": info.start,
                    "end": info.end
                }, tv_id=self.tv_id)

                # Claim the text-input session without changing the TV field.
                from androidtvremote2.remotemessage_pb2 import (
                    RemoteEditInfo,
                    RemoteImeBatchEdit,
                    RemoteImeObject,
                )

                response = RemoteMessage()
                response.remote_ime_batch_edit.CopyFrom(RemoteImeBatchEdit(
                    ime_counter=info.counter_field,
                    field_counter=info.counter_field,
                    edit_info=[RemoteEditInfo(
                        insert=0,
                        text_field_status=RemoteImeObject(
                            start=0,
                            end=0,
                            value="",
                        ),
                    )],
                ))
                self._send_message(response)
                logger.debug(
                    "Acknowledged IME session (counter=%s)",
                    info.counter_field,
                )

            elif (msg.HasField("remote_ime_key_inject") and
                  msg.remote_ime_key_inject.HasField("text_field_status")):
                # The TV also sends this message when text-field focus changes.
                info = msg.remote_ime_key_inject.text_field_status
                logger.debug(f"IME focus update: value='{info.value}', label='{info.label}'")
                broadcast_event("ime_focus", {
                    "value": info.value,
                    "label": info.label,
                    "start": info.start,
                    "end": info.end
                }, tv_id=self.tv_id)

        except Exception as e:
            logger.error(f"Error intercepting message: {e}")

        # Now let the parent class handle it normally
        super()._handle_message(raw_msg)


class CustomAndroidTVRemote(AndroidTVRemote):
    """Custom AndroidTVRemote that uses CustomRemoteProtocol"""

    def __init__(self, *args, tv_id=None, **kwargs):
        self.tv_id = tv_id
        super().__init__(*args, **kwargs)

    async def async_connect(self) -> None:
        """Connect to an Android TV."""
        # We need to override this to inject our CustomRemoteProtocol
        # This is a copy of AndroidTVRemote.async_connect but with CustomRemoteProtocol

        ssl_context = await self._create_ssl_context()
        self.on_con_lost = self._loop.create_future()
        on_remote_started = self._loop.create_future()

        try:
            (
                self._transport,
                self._remote_message_protocol,
            ) = await self._loop.create_connection(
                lambda: CustomRemoteProtocol(
                    self.on_con_lost,
                    on_remote_started,
                    self._on_is_on_updated,
                    self._on_current_app_updated,
                    self._on_volume_info_updated,
                    self._loop,
                    self._enable_ime,
                    self._enable_voice,
                    tv_id=self.tv_id,
                ),
                self.host,
                self._api_port,
                ssl=ssl_context,
            )
        except OSError as exc:
            logger.debug("Couldn't connect to %s:%s. Error: %s", self.host, self._api_port, exc)
            if isinstance(exc, ssl.SSLError):
                raise InvalidAuth("Need to pair") from exc
            raise CannotConnect(f"Couldn't connect to {self.host}:{self._api_port}") from exc

        await asyncio.wait((self.on_con_lost, on_remote_started), return_when=asyncio.FIRST_COMPLETED)
        if self.on_con_lost.done():
            con_lost_exc = self.on_con_lost.result()
            logger.debug(
                "Couldn't connect to %s:%s. Error: %s",
                self.host,
                self._api_port,
                con_lost_exc,
            )
            if isinstance(con_lost_exc, ssl.SSLError):
                raise InvalidAuth("Need to pair again") from con_lost_exc
            raise ConnectionClosed("Connection closed") from con_lost_exc


def load_config():
    """Load configuration from config.yaml"""
    global config
    config_path = Path(__file__).parent / "data" / "config.yaml"
    try:
        with open(config_path, 'r') as f:
            config = yaml.safe_load(f) or {}
        logger.debug(f"Configuration loaded: {config.get('tv_name', 'Unknown')} at {config.get('tv_ip', 'Unknown')}")
    except Exception as e:
        logger.error(f"Error loading config: {e}")
        config = {}


def save_app_registry():
    """Persist the common app launcher list atomically."""
    APP_REGISTRY_PATH.parent.mkdir(parents=True, exist_ok=True)
    temporary_path = APP_REGISTRY_PATH.with_suffix(".yaml.tmp")
    with open(temporary_path, "w") as registry_file:
        yaml.safe_dump(
            {"apps": list(app_registry.values())},
            registry_file,
            sort_keys=False,
        )
    temporary_path.replace(APP_REGISTRY_PATH)


def load_app_registry():
    """Load common launchers and migrate config.yaml launchers once."""
    global app_registry
    registry_exists = APP_REGISTRY_PATH.exists()
    loaded_apps = []
    if registry_exists:
        try:
            with open(APP_REGISTRY_PATH, "r") as registry_file:
                registry_data = yaml.safe_load(registry_file) or {}
            loaded_apps = registry_data.get("apps", [])
        except Exception as error:
            logger.error("Error loading app launcher registry: %s", error)

    app_registry = {}
    for app in loaded_apps:
        if not isinstance(app, dict):
            continue
        app_id = str(app.get("id", ""))
        name = str(app.get("name", "")).strip()
        package_id = str(app.get("package_id", "")).strip()
        icon = str(app.get("icon", "")).strip()
        icon_file = str(app.get("icon_file", "")).strip()
        safe_icon_file = (
            icon_file
            if icon_file
            and Path(icon_file).name == icon_file
            and re.fullmatch(r"[A-Za-z0-9_-]{1,64}\.(png|jpg|webp|gif)", icon_file)
            else ""
        )
        if re.fullmatch(r"[A-Za-z0-9_-]{1,64}", app_id) and name and package_id:
            app_registry[app_id] = {
                "id": app_id,
                "name": name,
                "package_id": package_id,
                "icon": icon,
                "icon_file": safe_icon_file,
            }

    if not registry_exists:
        for legacy_app in config.get("apps", []):
            if not isinstance(legacy_app, dict):
                continue
            package_id = str(legacy_app.get("id", "")).strip()
            name = str(legacy_app.get("name", "")).strip()
            if not package_id or not name:
                continue
            seed = package_id
            app_id = hashlib.sha256(seed.encode()).hexdigest()[:16]
            suffix = 1
            while app_id in app_registry:
                seed = f"{package_id}:{name}:{suffix}"
                app_id = hashlib.sha256(seed.encode()).hexdigest()[:16]
                suffix += 1
            app_registry[app_id] = {
                "id": app_id,
                "name": name,
                "package_id": package_id,
                "icon": str(legacy_app.get("icon", "")).strip(),
                "icon_file": "",
            }
        save_app_registry()
        if app_registry:
            logger.info("Migrated config.yaml app launchers to the managed registry")


def serialize_app(app):
    """Return the browser-safe launcher representation."""
    icon_file = app.get("icon_file", "")
    return {
        "id": app["id"],
        "name": app["name"],
        "package_id": app["package_id"],
        "icon": f"icons/{icon_file}" if icon_file else app.get("icon", ""),
        "icon_class": app.get("icon", ""),
        "has_uploaded_icon": bool(icon_file),
    }


def apps_for_tv(tv_id):
    """Return common launchers enabled for one TV, preserving common order."""
    if tv_id not in tv_registry:
        return []
    enabled_ids = set(tv_registry[tv_id].get("app_ids", []))
    return [
        serialize_app(app)
        for app_id, app in app_registry.items()
        if app_id in enabled_ids
    ]


def save_tv_registry():
    """Persist the PWA-managed TV list atomically."""
    TV_REGISTRY_PATH.parent.mkdir(parents=True, exist_ok=True)
    temporary_path = TV_REGISTRY_PATH.with_suffix(".yaml.tmp")
    with open(temporary_path, "w") as registry_file:
        yaml.safe_dump(
            {"tvs": list(tv_registry.values())},
            registry_file,
            sort_keys=False,
        )
    temporary_path.replace(TV_REGISTRY_PATH)


def load_tv_registry():
    """Load TVs and migrate legacy TV/app availability once."""
    global tv_registry
    registry_exists = TV_REGISTRY_PATH.exists()
    loaded_tvs = []
    if registry_exists:
        try:
            with open(TV_REGISTRY_PATH, "r") as registry_file:
                registry_data = yaml.safe_load(registry_file) or {}
            loaded_tvs = registry_data.get("tvs", [])
        except Exception as error:
            logger.error("Error loading TV registry: %s", error)

    tv_registry = {}
    registry_changed = False
    default_app_ids = list(app_registry)
    for tv in loaded_tvs:
        if not isinstance(tv, dict):
            continue
        tv_id = str(tv.get("id", ""))
        name = str(tv.get("name", "")).strip()
        host = str(tv.get("host", "")).strip()
        if not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", tv_id) or not name or not host:
            continue

        configured_app_ids = tv.get("app_ids")
        if configured_app_ids is None:
            enabled_app_ids = default_app_ids.copy()
            registry_changed = True
        elif isinstance(configured_app_ids, list):
            enabled_app_ids = []
            for app_id in configured_app_ids:
                app_id = str(app_id)
                if app_id in app_registry and app_id not in enabled_app_ids:
                    enabled_app_ids.append(app_id)
            if enabled_app_ids != configured_app_ids:
                registry_changed = True
        else:
            enabled_app_ids = []
            registry_changed = True

        tv_registry[tv_id] = {
            "id": tv_id,
            "name": name,
            "host": host,
            "app_ids": enabled_app_ids,
        }

    legacy_host = str(config.get("tv_ip", "")).strip()
    if not registry_exists and legacy_host:
        legacy_id = hashlib.sha256(legacy_host.encode()).hexdigest()[:16]
        tv_registry[legacy_id] = {
            "id": legacy_id,
            "name": str(config.get("tv_name", "Android TV")).strip() or "Android TV",
            "host": legacy_host,
            "app_ids": default_app_ids.copy(),
        }
        tv_directory = TV_DATA_DIR / legacy_id
        tv_directory.mkdir(parents=True, exist_ok=True)
        for filename in ("cert.pem", "key.pem"):
            legacy_path = TV_REGISTRY_PATH.parent / filename
            target_path = tv_directory / filename
            if legacy_path.exists() and not target_path.exists():
                shutil.copy2(legacy_path, target_path)
        save_tv_registry()
        logger.info("Migrated legacy TV configuration to the managed TV list")
    elif registry_exists and registry_changed:
        save_tv_registry()


def get_tv_state(tv_id):
    """Return the runtime state for a TV, creating it when needed."""
    return tv_states.setdefault(tv_id, TVState())


def is_remote_connected(remote):
    """Check whether a remote still has an active protocol."""
    return bool(
        remote
        and hasattr(remote, "_remote_message_protocol")
        and remote._remote_message_protocol is not None
    )


def resolve_tv_id(tv_id=None):
    """Resolve a requested TV, with compatibility for single-TV clients."""
    if tv_id in tv_registry:
        return tv_id
    if not tv_id and len(tv_registry) == 1:
        return next(iter(tv_registry))
    return None


def tv_status(tv_id):
    """Serialize one TV and its connection state for the PWA."""
    tv = tv_registry[tv_id]
    state = get_tv_state(tv_id)
    return {
        **tv,
        "connected": is_remote_connected(state.remote),
        "connecting": state.connecting,
        "pairing_in_progress": state.pairing_in_progress,
    }


def disconnect_tv(tv_id):
    """Stop connection and pairing work for one TV."""
    state = tv_states.get(tv_id)
    if not state:
        return
    current_task = asyncio.current_task()
    if state.connect_task and state.connect_task is not current_task:
        state.connect_task.cancel()
    if state.pairing_code_future and not state.pairing_code_future.done():
        state.pairing_code_future.cancel()
    if state.remote:
        try:
            state.remote.disconnect()
        except Exception as error:
            logger.debug("Error disconnecting %s: %s", tv_id, error)
    state.remote = None
    state.connecting = False
    state.pairing_in_progress = False
    state.pairing_code_future = None
    state.connect_task = None


def start_tv_connection(tv_id):
    """Start a connection without duplicating in-flight work."""
    state = get_tv_state(tv_id)
    if is_remote_connected(state.remote):
        return "connected"
    if state.connecting or state.pairing_in_progress:
        return "already_in_progress"
    if state.connect_task and not state.connect_task.done():
        return "already_in_progress"
    state.connect_task = asyncio.create_task(initialize_tv(tv_id))

    def clear_finished_task(task):
        if state.connect_task is task:
            state.connect_task = None

    state.connect_task.add_done_callback(clear_finished_task)
    return "connecting"


async def monitor_connection(tv_id, remote_instance):
    """Monitor one TV connection and reconnect it after a drop."""
    logger.info("Starting connection monitor for %s", tv_id)
    try:
        if hasattr(remote_instance, "on_con_lost"):
            await remote_instance.on_con_lost
            logger.warning("Connection to %s was lost", tv_id)
        else:
            logger.warning("Remote %s has no connection-lost future", tv_id)
            return
    except Exception as error:
        logger.error("Error monitoring %s: %s", tv_id, error)

    state = tv_states.get(tv_id)
    if not state or state.remote is not remote_instance or tv_id not in tv_registry:
        logger.info("Connection monitor finished for superseded TV %s", tv_id)
        return
    state.remote = None
    await asyncio.sleep(5)
    retry_count = 0
    while tv_id in tv_registry:
        state = tv_states.get(tv_id)
        if not state or is_remote_connected(state.remote):
            break
        result = await initialize_tv(tv_id)
        if result.get("status") == "connected":
            logger.info("Auto-reconnect to %s succeeded", tv_id)
            break
        retry_count += 1
        wait_time = min(30, 5 + retry_count * 2)
        logger.info(
            "Reconnect to %s failed (%s); retrying in %ss",
            tv_id,
            result.get("error", "unknown"),
            wait_time,
        )
        await asyncio.sleep(wait_time)


async def initialize_tv(tv_id):
    """Initialize a connection (and pairing when needed) for one TV."""
    if tv_id not in tv_registry:
        return {"status": "error", "error": "Unknown TV"}
    state = get_tv_state(tv_id)
    if state.connecting or state.pairing_in_progress:
        return {"status": "already_in_progress"}
    state.connecting = True
    state.pairing_in_progress = False
    tv = tv_registry[tv_id]

    try:
        tv_directory = TV_DATA_DIR / tv_id
        tv_directory.mkdir(parents=True, exist_ok=True)
        cert_file = tv_directory / "cert.pem"
        key_file = tv_directory / "key.pem"
        remote = CustomAndroidTVRemote(
            client_name="droidtv-remote",
            certfile=str(cert_file),
            keyfile=str(key_file),
            host=tv["host"],
            enable_ime=True,
            tv_id=tv_id,
        )
        state.remote = remote
        logger.info("Connecting to %s at %s...", tv["name"], tv["host"])
        cert_generated = await remote.async_generate_cert_if_missing()
        if cert_generated:
            logger.info("Generated certificates for %s", tv["name"])

        try:
            await remote.async_connect()
            state.connecting = False
            asyncio.create_task(monitor_connection(tv_id, remote))
            logger.info("Connected to %s", tv["name"])
            return {"status": "connected"}
        except InvalidAuth:
            state.pairing_in_progress = True
            state.connecting = False
            logger.info("Pairing required for %s", tv["name"])
            try:
                await remote.async_start_pairing()
                state.pairing_code_future = asyncio.get_running_loop().create_future()
                code = await asyncio.wait_for(
                    state.pairing_code_future,
                    timeout=120.0,
                )
                await remote.async_finish_pairing(code)
                await remote.async_connect()
                state.pairing_in_progress = False
                asyncio.create_task(monitor_connection(tv_id, remote))
                logger.info("Paired with and connected to %s", tv["name"])
                return {"status": "connected"}
            except asyncio.TimeoutError:
                state.remote = None
                return {"status": "timeout", "error": "Pairing timeout"}
            except Exception as error:
                logger.error("Error pairing with %s: %s", tv["name"], error)
                state.remote = None
                return {"status": "error", "error": str(error)}
            finally:
                state.pairing_in_progress = False
                state.pairing_code_future = None
    except (ConnectionClosed, CannotConnect, InvalidAuth) as error:
        logger.error("Failed to connect to %s: %s", tv["name"], error)
        state.remote = None
        return {"status": "error", "error": str(error)}
    except Exception as error:
        logger.error("Unexpected error connecting to %s: %s", tv["name"], error)
        state.remote = None
        return {"status": "error", "error": str(error)}
    finally:
        state.connecting = False


# HTTP Handlers


def validate_icon_bytes(content_type, content):
    """Validate uploaded image type, size, and basic file signature."""
    extension = ALLOWED_ICON_TYPES.get(content_type)
    if not extension:
        raise ValueError("Icon must be PNG, JPEG, WebP, or GIF")
    if not content:
        raise ValueError("Uploaded icon is empty")
    if len(content) > MAX_ICON_BYTES:
        raise ValueError("Icon must be 2 MB or smaller")

    valid_signature = {
        "png": content.startswith(b"\x89PNG\r\n\x1a\n"),
        "jpg": content.startswith(b"\xff\xd8\xff"),
        "gif": content.startswith((b"GIF87a", b"GIF89a")),
        "webp": content.startswith(b"RIFF") and content[8:12] == b"WEBP",
    }[extension]
    if not valid_signature:
        raise ValueError("Uploaded icon content does not match its image type")
    return extension


async def read_app_form(request):
    """Read launcher fields and an optional bounded image upload."""
    if request.content_type == "application/json":
        data = await request.json()
        return {key: str(value) for key, value in data.items()}, None
    if not request.content_type.startswith("multipart/"):
        data = await request.post()
        return {key: str(value) for key, value in data.items()}, None

    fields = {}
    icon_upload = None
    reader = await request.multipart()
    async for field in reader:
        if field.name == "icon_file" and field.filename:
            content = bytearray()
            while True:
                chunk = await field.read_chunk()
                if not chunk:
                    break
                content.extend(chunk)
                if len(content) > MAX_ICON_BYTES:
                    raise ValueError("Icon must be 2 MB or smaller")
            content_type = field.headers.get("Content-Type", "").split(";", 1)[0].lower()
            extension = validate_icon_bytes(content_type, bytes(content))
            icon_upload = (extension, bytes(content))
        elif field.name:
            fields[field.name] = (await field.text()).strip()
    return fields, icon_upload


def validate_app_details(name, package_id, icon_class=""):
    """Return a validation error for launcher details, if any."""
    if not name or len(name) > 100:
        return "App name is required (100 characters maximum)"
    if not package_id or len(package_id) > 255 or any(char.isspace() for char in package_id):
        return "A valid Android package ID is required"
    if icon_class and not re.fullmatch(r"mdi-[A-Za-z0-9-]{1,90}", icon_class):
        return "Material icon class must start with mdi- and contain only letters, numbers, or dashes"
    return None


def delete_uploaded_icon(app):
    """Remove one managed icon file without touching unrelated files."""
    icon_file = app.get("icon_file", "")
    if icon_file and Path(icon_file).name == icon_file:
        (ICON_DATA_DIR / icon_file).unlink(missing_ok=True)


def write_uploaded_icon(app_id, extension, content):
    """Write an uploaded icon atomically and return its managed filename."""
    ICON_DATA_DIR.mkdir(parents=True, exist_ok=True)
    filename = f"{app_id}-{secrets.token_hex(4)}.{extension}"
    target_path = ICON_DATA_DIR / filename
    temporary_path = ICON_DATA_DIR / f"{filename}.tmp"
    with open(temporary_path, "wb") as icon_file:
        icon_file.write(content)
    temporary_path.replace(target_path)
    return filename


async def apps_handler(request):
    """List all common app launchers."""
    return web.json_response({
        "apps": [serialize_app(app) for app in app_registry.values()]
    })


async def add_app_handler(request):
    """Create a common launcher with an optional uploaded icon."""
    try:
        fields, icon_upload = await read_app_form(request)
    except ValueError as error:
        return web.json_response({"error": str(error)}, status=400)

    name = fields.get("name", "").strip()
    package_id = fields.get("package_id", "").strip()
    icon_class = fields.get("icon_class", "").strip()
    validation_error = validate_app_details(name, package_id, icon_class)
    if validation_error:
        return web.json_response({"error": validation_error}, status=400)
    if any(app["package_id"] == package_id for app in app_registry.values()):
        return web.json_response({"error": "That package ID already has a launcher"}, status=409)

    app_id = secrets.token_hex(8)
    app = {
        "id": app_id,
        "name": name,
        "package_id": package_id,
        "icon": icon_class,
        "icon_file": "",
    }
    if icon_upload:
        extension, content = icon_upload
        app["icon_file"] = write_uploaded_icon(app_id, extension, content)
    app_registry[app_id] = app
    save_app_registry()
    logger.info("Added app launcher %s", name)
    return web.json_response({"app": serialize_app(app)}, status=201)


async def update_app_handler(request):
    """Edit a common launcher and optionally replace/remove its icon."""
    app_id = request.match_info["app_id"]
    app = app_registry.get(app_id)
    if not app:
        return web.json_response({"error": "Unknown app launcher"}, status=404)
    try:
        fields, icon_upload = await read_app_form(request)
    except ValueError as error:
        return web.json_response({"error": str(error)}, status=400)

    name = fields.get("name", app["name"]).strip()
    package_id = fields.get("package_id", app["package_id"]).strip()
    icon_class = fields.get("icon_class", app.get("icon", "")).strip()
    validation_error = validate_app_details(name, package_id, icon_class)
    if validation_error:
        return web.json_response({"error": validation_error}, status=400)
    if any(
        other_id != app_id and other["package_id"] == package_id
        for other_id, other in app_registry.items()
    ):
        return web.json_response({"error": "That package ID already has a launcher"}, status=409)

    old_icon_file = app.get("icon_file", "")
    app["name"] = name
    app["package_id"] = package_id
    if "icon_class" in fields:
        app["icon"] = fields["icon_class"].strip()
    if icon_upload:
        extension, content = icon_upload
        app["icon_file"] = write_uploaded_icon(app_id, extension, content)
        if old_icon_file and old_icon_file != app["icon_file"]:
            (ICON_DATA_DIR / old_icon_file).unlink(missing_ok=True)
    elif fields.get("remove_icon", "").lower() == "true":
        delete_uploaded_icon(app)
        app["icon_file"] = ""

    save_app_registry()
    logger.info("Updated app launcher %s", name)
    return web.json_response({"app": serialize_app(app)})


async def delete_app_handler(request):
    """Delete a common launcher and remove it from every TV."""
    app_id = request.match_info["app_id"]
    app = app_registry.get(app_id)
    if not app:
        return web.json_response({"error": "Unknown app launcher"}, status=404)

    delete_uploaded_icon(app)
    app_registry.pop(app_id)
    save_app_registry()
    tvs_changed = False
    for tv in tv_registry.values():
        if app_id in tv.get("app_ids", []):
            tv["app_ids"].remove(app_id)
            tvs_changed = True
    if tvs_changed:
        save_tv_registry()
    logger.info("Deleted app launcher %s", app["name"])
    return web.json_response({"status": "deleted", "app_id": app_id})


async def tv_apps_handler(request):
    """Set which common launchers are available on one TV."""
    tv_id = request.match_info["tv_id"]
    if tv_id not in tv_registry:
        return web.json_response({"error": "Unknown TV"}, status=404)
    data = await request.json()
    requested_ids = data.get("app_ids")
    if not isinstance(requested_ids, list):
        return web.json_response({"error": "app_ids must be a list"}, status=400)

    enabled_ids = []
    for app_id in requested_ids:
        app_id = str(app_id)
        if app_id not in app_registry:
            return web.json_response({"error": f"Unknown app launcher: {app_id}"}, status=400)
        if app_id not in enabled_ids:
            enabled_ids.append(app_id)
    tv_registry[tv_id]["app_ids"] = enabled_ids
    save_tv_registry()
    return web.json_response({"tv": tv_status(tv_id)})


async def status_handler(request):
    """Get the selected TV connection status and shared app settings."""
    load_config()
    tv_id = resolve_tv_id(request.query.get("tv_id"))
    selected_status = tv_status(tv_id) if tv_id else None
    return web.json_response({
        "tv_id": tv_id,
        "connected": selected_status["connected"] if selected_status else False,
        "pairing_in_progress": selected_status["pairing_in_progress"] if selected_status else False,
        "connecting": selected_status["connecting"] if selected_status else False,
        "tv_name": selected_status["name"] if selected_status else "No TV selected",
        "apps": apps_for_tv(tv_id),
        "version": __version__,
    })


async def tvs_handler(request):
    """List configured TVs and their current connection status."""
    return web.json_response({"tvs": [tv_status(tv_id) for tv_id in tv_registry]})


async def add_tv_handler(request):
    """Add a TV that can then be paired by the requesting PWA."""
    data = await request.json()
    name = str(data.get("name", "")).strip()
    host = str(data.get("host", "")).strip()
    if not name or len(name) > 100:
        return web.json_response(
            {"error": "TV name is required (100 characters maximum)"}, status=400
        )
    if not host or len(host) > 255 or any(character.isspace() for character in host):
        return web.json_response(
            {"error": "A valid TV IP address or host name is required"}, status=400
        )
    if any(tv["host"].casefold() == host.casefold() for tv in tv_registry.values()):
        return web.json_response(
            {"error": "That TV address is already configured"}, status=409
        )
    tv_id = secrets.token_hex(8)
    tv_registry[tv_id] = {
        "id": tv_id,
        "name": name,
        "host": host,
        "app_ids": list(app_registry),
    }
    save_tv_registry()
    logger.info("Added TV %s at %s", name, host)
    return web.json_response({"tv": tv_status(tv_id)}, status=201)


async def forget_tv_handler(request):
    """Forget a TV and remove only its generated pairing credentials."""
    tv_id = request.match_info["tv_id"]
    if tv_id not in tv_registry:
        return web.json_response({"error": "Unknown TV"}, status=404)
    tv_name = tv_registry[tv_id]["name"]
    disconnect_tv(tv_id)
    tv_states.pop(tv_id, None)
    tv_registry.pop(tv_id)
    save_tv_registry()
    tv_directory = TV_DATA_DIR / tv_id
    for filename in ("cert.pem", "key.pem"):
        (tv_directory / filename).unlink(missing_ok=True)
    try:
        tv_directory.rmdir()
    except (FileNotFoundError, OSError):
        pass
    logger.info("Forgot TV %s", tv_name)
    return web.json_response({"status": "forgotten", "tv_id": tv_id})


async def connect_handler(request):
    """Initiate a connection to the selected TV."""
    data = await request.json()
    tv_id = resolve_tv_id(data.get("tv_id"))
    if not tv_id:
        return web.json_response({"error": "Unknown TV"}, status=404)
    logger.info("Connect endpoint called for %s", tv_id)
    return web.json_response({"status": start_tv_connection(tv_id), "tv_id": tv_id})


async def pairing_code_handler(request):
    """Submit a pairing code for the selected TV."""
    data = await request.json()
    tv_id = resolve_tv_id(data.get("tv_id"))
    state = tv_states.get(tv_id) if tv_id else None
    code = str(data.get("code", "")).strip()
    if state and state.pairing_code_future and not state.pairing_code_future.done():
        state.pairing_code_future.set_result(code)
        return web.json_response({"status": "submitted"})
    logger.warning("Received a pairing code with no waiter for %s", tv_id)
    return web.json_response({"error": "Not waiting for pairing code"}, status=400)


def remote_from_data(data):
    """Resolve the selected TV runtime state from a command payload."""
    tv_id = resolve_tv_id(data.get("tv_id"))
    state = tv_states.get(tv_id) if tv_id else None
    remote = state.remote if state else None
    return tv_id, state, remote


async def send_key_handler(request):
    """Send a key command to the selected TV."""
    data = await request.json()
    tv_id, state, remote = remote_from_data(data)
    key_code = data.get("key")
    if not is_remote_connected(remote):
        return web.json_response({"error": "Not connected to TV"}, status=400)
    try:
        remote.send_key_command(key_code)
        logger.debug("Sent key %s to %s", key_code, tv_id)
        return web.json_response({"status": "ok"})
    except ConnectionClosed:
        state.remote = None
        return web.json_response({"error": "Not connected to TV"}, status=400)
    except Exception as error:
        logger.error("Error sending key: %s", error)
        return web.json_response({"error": str(error)}, status=500)


async def launch_app_handler(request):
    """Launch an enabled common app on the selected TV."""
    data = await request.json()
    tv_id, state, remote = remote_from_data(data)
    requested_id = data.get("launcher_id") or data.get("app_id")
    launcher_id = requested_id if requested_id in app_registry else None
    if not launcher_id:
        launcher_id = next(
            (
                app_id
                for app_id, app in app_registry.items()
                if app["package_id"] == requested_id
            ),
            None,
        )
    if not launcher_id:
        return web.json_response({"error": "Unknown app launcher"}, status=404)
    if not tv_id or launcher_id not in tv_registry[tv_id].get("app_ids", []):
        return web.json_response({"error": "App is not enabled for this TV"}, status=403)
    if not is_remote_connected(remote):
        return web.json_response({"error": "Not connected to TV"}, status=400)

    package_id = app_registry[launcher_id]["package_id"]
    try:
        remote.send_launch_app_command(package_id)
        logger.info("Launched %s on %s", package_id, tv_id)
        return web.json_response({"status": "ok"})
    except ConnectionClosed:
        state.remote = None
        return web.json_response({"error": "Not connected to TV"}, status=400)
    except Exception as error:
        logger.error("Error launching app: %s", error)
        return web.json_response({"error": str(error)}, status=500)


async def send_text_handler(request):
    """Send text input to the selected TV."""
    data = await request.json()
    tv_id, state, remote = remote_from_data(data)
    text = data.get("text", "")
    send_enter = data.get("enter", False)
    if not is_remote_connected(remote):
        return web.json_response({"error": "Not connected to TV"}, status=400)
    if not text:
        return web.json_response({"error": "No text provided"}, status=400)
    try:
        protocol = remote._remote_message_protocol
        protocol.ime_counter = 0
        protocol.ime_field_counter = 0
        logger.info("Sending text to %s (length %s)", tv_id, len(text))
        remote.send_text(text)
        if send_enter:
            await asyncio.sleep(0.5)
            remote.send_key_command("KEYCODE_ENTER")
        return web.json_response({"status": "ok"})
    except (ConnectionClosed, ConnectionError) as error:
        logger.error("Connection lost while sending text: %s", error)
        state.remote = None
        return web.json_response({"error": "Connection lost"}, status=400)
    except Exception as error:
        logger.exception("Unexpected error sending text: %s", error)
        return web.json_response({"error": str(error)}, status=500)


async def events_handler(request):
    """Long-poll events for the TV selected by this browser."""
    tv_id = resolve_tv_id(request.query.get("tv_id"))
    event_key = tv_id or ""
    pending_events = server_events.setdefault(event_key, [])
    waiting_futures = server_event_futures.setdefault(event_key, [])
    if pending_events:
        return web.json_response(pending_events.pop(0))
    future = asyncio.get_running_loop().create_future()
    waiting_futures.append(future)
    try:
        event = await asyncio.wait_for(future, timeout=30.0)
        return web.json_response(event)
    except asyncio.TimeoutError:
        if future in waiting_futures:
            waiting_futures.remove(future)
        return web.json_response({"type": "keepalive", "tv_id": tv_id})
    except Exception as error:
        if future in waiting_futures:
            waiting_futures.remove(future)
        logger.error("Error in events handler: %s", error)
        return web.json_response({"error": str(error)}, status=500)


@web.middleware
async def error_middleware(request, handler):
    try:
        return await handler(request)
    except web.HTTPException:
        raise
    except Exception as e:
        logger.exception(f"Unhandled exception processing {request.method} {request.path}")
        return web.json_response({"error": str(e), "type": "internal_error"}, status=500)


@web.middleware
async def subfolder_middleware(request, handler):
    """
    Middleware that allows the app to work behind a subfolder proxy
    without needing to know the prefix.
    """
    try:
        # Try normal handling first
        return await handler(request)
    except web.HTTPNotFound:
        path = request.path
        if path == '/' or not path:
            raise

        parts = [p for p in path.split('/') if p]
        if not parts:
            raise

        # Try stripping segments from left to right until we find a match
        # e.g. /remote/api/status -> /api/status
        for i in range(len(parts)):
            new_path = '/' + '/'.join(parts[i+1:]) # if i=0, we strip 'remote'
            if not new_path: new_path = '/'

            logger.debug(f"Subfolder check: {path} -> {new_path}")

            # Create a new request for the sub-path
            new_request = request.clone(rel_url=new_path)

            # Resolve the route manually
            match_info = await request.app.router.resolve(new_request)

            if match_info.http_exception is None:
                # Found a valid route! Serve it by calling its handler
                logger.debug(f"Subfolder match found! Serving {new_path} for {path}")
                try:
                    return await match_info.handler(new_request)
                except Exception as e:
                    logger.exception(f"Error calling sub-handler for {new_path}")
                    raise

        # If it's a directory-like path that we didn't match, serve index.html
        if path.endswith('/') or '.' not in path.split('/')[-1]:
            logger.debug(f"Serving index.html as fallback for directory-like path: {path}")
            return await index_handler(request)

        raise


async def on_startup(app):
    """Application startup handler"""
    load_config()
    load_app_registry()
    load_tv_registry()
    logger.info("Server started and configuration loaded")


async def on_shutdown(app):
    """Application shutdown handler"""
    logger.info("Server shutting down, cancelling pending events...")
    for waiting_futures in server_event_futures.values():
        for future in waiting_futures:
            if not future.done():
                future.cancel()
    server_event_futures.clear()


async def on_cleanup(app):
    """Application cleanup handler"""
    for tv_id in list(tv_states):
        disconnect_tv(tv_id)
    logger.info("Server shutdown complete")


async def index_handler(request):
    """Serve the index.html file"""
    index_file = Path(__file__).parent / 'static' / 'index.html'
    return web.FileResponse(index_file)


def create_app():
    """Create and configure the aiohttp application"""
    # Initialize app with our smart middlewares
    app = web.Application(
        middlewares=[error_middleware, subfolder_middleware],
        client_max_size=MAX_ICON_BYTES + 64 * 1024,
    )

    # Setup routes at the ROOT
    # These routes are now prefix-agnostic thanks to the middleware
    app.router.add_get('/', index_handler)
    app.router.add_get('/api/status', status_handler)
    app.router.add_get('/api/tvs', tvs_handler)
    app.router.add_post('/api/tvs', add_tv_handler)
    app.router.add_delete('/api/tvs/{tv_id}', forget_tv_handler)
    app.router.add_put('/api/tvs/{tv_id}/apps', tv_apps_handler)
    app.router.add_get('/api/apps', apps_handler)
    app.router.add_post('/api/apps', add_app_handler)
    app.router.add_put('/api/apps/{app_id}', update_app_handler)
    app.router.add_delete('/api/apps/{app_id}', delete_app_handler)
    app.router.add_post('/api/connect', connect_handler)
    app.router.add_post('/api/pairing_code', pairing_code_handler)
    app.router.add_post('/api/send_key', send_key_handler)
    app.router.add_post('/api/send_text', send_text_handler)
    app.router.add_post('/api/launch_app', launch_app_handler)
    app.router.add_get('/api/events', events_handler)

    # Static files at the ROOT
    ICON_DATA_DIR.mkdir(parents=True, exist_ok=True)
    app.router.add_static('/icons/', ICON_DATA_DIR.resolve(), name='icons', show_index=True)
    app.router.add_static('/', (Path(__file__).parent / 'static').resolve(), name='static', show_index=True)

    # Setup event handlers
    app.on_startup.append(on_startup)
    app.on_shutdown.append(on_shutdown)
    app.on_cleanup.append(on_cleanup)

    return app


def get_server_port() -> int:
    """Get server port from SERVER_PORT or PORT env var, config, or default to 7503."""
    env_port = os.environ.get('SERVER_PORT') or os.environ.get('PORT')
    if env_port:
        try:
            return int(env_port)
        except ValueError:
            pass
    try:
        return int(config.get('server_port', 7503))
    except (ValueError, TypeError):
        return 7503


if __name__ == '__main__':
    load_config()
    app = create_app()
    port = get_server_port()
    logger.info(f"Starting droidtv-remote server on http://0.0.0.0:{port}")
    web.run_app(app, host='0.0.0.0', port=port, shutdown_timeout=1.0)

