"""
droidtv-remote Server
A local web server that hosts a remote control UI for Android TV.
Uses simple HTTP requests instead of WebSockets.
"""
import asyncio
import logging
import ssl  # Added ssl import here
from pathlib import Path
from typing import Optional, List, Callable
import yaml
from aiohttp import web
from androidtvremote2 import AndroidTVRemote, ConnectionClosed, CannotConnect, InvalidAuth
from androidtvremote2.remote import RemoteProtocol, RemoteMessage, Feature

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Global state
tv_remote: Optional['CustomAndroidTVRemote'] = None
config = {}
pairing_code_future: Optional[asyncio.Future] = None
pairing_in_progress = False
connecting = False

# Event queue for long-polling
server_events = []
server_event_futures: List[asyncio.Future] = []


def broadcast_event(event_type, data=None):
    """Broadcast an event to all waiting clients"""
    global server_event_futures
    event = {"type": event_type, "data": data}

    # Notify all waiting futures
    for future in server_event_futures:
        if not future.done():
            future.set_result(event)

    # Clear the list as they are single-use
    server_event_futures = []


class CustomRemoteProtocol(RemoteProtocol):
    """Custom protocol to intercept IME events"""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._on_ime_show_updated: Callable[[bool], None] = lambda x: None


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
                })

                # IMPORTANT: Send an acknowledgment back to claim the keyboard session
                # This should dismiss the "Use keyboard on mobile device" notification
                # We send an empty IME edit to acknowledge we've taken control
                from androidtvremote2.remotemessage_pb2 import RemoteImeBatchEdit, RemoteEditInfo, RemoteImeObject

                # Create a response acknowledging the IME session
                response = RemoteMessage()
                ime_object = RemoteImeObject(start=0, end=0, value="")
                edit_info = RemoteEditInfo(insert=0, text_field_status=ime_object)
                batch_edit = RemoteImeBatchEdit(
                    ime_counter=info.counter_field,
                    field_counter=info.counter_field,
                    edit_info=[edit_info],
                )
                response.remote_ime_batch_edit.CopyFrom(batch_edit)

                # Send the acknowledgment
                self._send_message(response)
                logger.info("Sent IME acknowledgment to TV")

        except Exception as e:
            logger.error(f"Error intercepting message: {e}")

        # Now let the parent class handle it normally
        super()._handle_message(raw_msg)


class CustomAndroidTVRemote(AndroidTVRemote):
    """Custom AndroidTVRemote that uses CustomRemoteProtocol"""

    async def async_connect(self) -> None:
        """Connect to an Android TV."""
        # We need to override this to inject our CustomRemoteProtocol
        # This is a copy of AndroidTVRemote.async_connect but with CustomRemoteProtocol

        ssl_context = await self._create_ssl_context()
        on_con_lost = self._loop.create_future()
        on_remote_started = self._loop.create_future()

        try:
            (
                self._transport,
                self._remote_message_protocol,
            ) = await self._loop.create_connection(
                lambda: CustomRemoteProtocol(
                    on_con_lost,
                    on_remote_started,
                    self._on_is_on_updated,
                    self._on_current_app_updated,
                    self._on_volume_info_updated,
                    self._loop,
                    self._enable_ime,
                    self._enable_voice,
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

        await asyncio.wait((on_con_lost, on_remote_started), return_when=asyncio.FIRST_COMPLETED)
        if on_con_lost.done():
            con_lost_exc = on_con_lost.result()
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
            config = yaml.safe_load(f)
        logger.debug(f"Configuration loaded: {config.get('tv_name', 'Unknown')} at {config.get('tv_ip', 'Unknown')}")
    except Exception as e:
        logger.error(f"Error loading config: {e}")
        config = {}


async def initialize_tv(force=False):
    """Initialize connection to Android TV"""
    global tv_remote, pairing_code_future, pairing_in_progress, connecting

    # Prevent multiple simultaneous connection attempts (unless forced)
    if not force and (connecting or pairing_in_progress):
        logger.info("Connection or pairing already in progress, skipping...")
        return {"status": "already_in_progress"}

    connecting = True
    pairing_in_progress = False  # Reset pairing flag

    try:
        cert_file = Path(__file__).parent / "data" / "cert.pem"
        key_file = Path(__file__).parent / "data" / "key.pem"

        # Create CustomAndroidTVRemote instance
        tv_remote = CustomAndroidTVRemote(
            client_name="droidtv-remote",
            certfile=str(cert_file),
            keyfile=str(key_file),
            host=config.get('tv_ip', '127.0.0.1'),
            enable_ime=False,  # Disable IME to prevent "Use keyboard on mobile device" notification
        )


        logger.info(f"Connecting to {config.get('tv_name', 'TV')} at {config.get('tv_ip')}...")

        # Generate certificates if they don't exist
        cert_generated = await tv_remote.async_generate_cert_if_missing()
        if cert_generated:
            logger.info("Generated new certificates")

        # Try to connect
        try:
            await tv_remote.async_connect()
            logger.info("Successfully connected to Android TV")
            connecting = False
            return {"status": "connected"}

        except InvalidAuth:
            # Need to pair first
            pairing_in_progress = True
            connecting = False

            logger.info("Pairing required, starting pairing process...")

            try:
                # Start pairing
                await tv_remote.async_start_pairing()
                logger.info("Pairing started, waiting for code...")

                # Wait for pairing code from client (with timeout)
                pairing_code_future = asyncio.Future()
                try:
                    logger.info("Waiting for pairing code from client...")
                    code = await asyncio.wait_for(pairing_code_future, timeout=120.0)
                    logger.info(f"Received pairing code from client: {code}")

                    # Finish pairing
                    logger.info("Calling async_finish_pairing...")
                    await tv_remote.async_finish_pairing(code)
                    logger.info("Pairing successful, attempting to connect...")

                    # Now connect again
                    await tv_remote.async_connect()
                    logger.info("Successfully connected to Android TV after pairing")
                    return {"status": "connected"}

                except asyncio.TimeoutError:
                    logger.error("Pairing code timeout - no code received within 120 seconds")
                    return {"status": "timeout", "error": "Pairing timeout"}

            except Exception as e:
                logger.error(f"Error during pairing: {e}")
                return {"status": "error", "error": str(e)}
            finally:
                pairing_in_progress = False

    except (ConnectionClosed, CannotConnect, InvalidAuth) as e:
        logger.error(f"Failed to connect to TV: {e}")
        connecting = False
        pairing_in_progress = False
        return {"status": "error", "error": str(e)}
    except Exception as e:
        logger.error(f"Unexpected error: {e}")
        connecting = False
        pairing_in_progress = False
        return {"status": "error", "error": str(e)}


# HTTP Handlers

async def status_handler(request):
    """Get connection status (reloads config for live updates)"""
    load_config()
    is_connected = False
    if tv_remote and hasattr(tv_remote, '_remote_message_protocol'):
        is_connected = tv_remote._remote_message_protocol is not None

    return web.json_response({
        "connected": is_connected,
        "pairing_in_progress": pairing_in_progress,
        "connecting": connecting,
        "tv_name": config.get('tv_name', 'Android TV'),
        "apps": config.get('apps', [])
    })


async def connect_handler(request):
    """Initiate connection to TV"""
    logger.info("Connect endpoint called")
    # Start connection in background
    asyncio.create_task(initialize_tv(force=True))
    return web.json_response({"status": "connecting"})


async def pairing_code_handler(request):
    """Submit pairing code"""
    global pairing_code_future

    data = await request.json()
    code = data.get('code', '')

    logger.info(f"Received pairing code via HTTP: {code}")

    if pairing_code_future and not pairing_code_future.done():
        logger.info(f"Setting pairing_code_future result to: {code}")
        pairing_code_future.set_result(code)
        return web.json_response({"status": "submitted"})
    else:
        logger.warning(f"Received pairing code but no future waiting")
        return web.json_response({"error": "Not waiting for pairing code"}, status=400)


async def send_key_handler(request):
    """Send key command to TV"""
    data = await request.json()
    key_code = data.get('key')

    if not tv_remote:
        return web.json_response({"error": "Not connected to TV"}, status=400)

    try:
        tv_remote.send_key_command(key_code)
        logger.debug(f"Sent key: {key_code}")
        return web.json_response({"status": "ok"})
    except ConnectionClosed:
        logger.error(f"Connection closed while sending key: {key_code}")
        return web.json_response({"error": "Not connected to TV"}, status=400)
    except Exception as e:
        logger.error(f"Error sending key: {e}")
        return web.json_response({"error": str(e)}, status=500)


async def launch_app_handler(request):
    """Launch app on TV"""
    data = await request.json()
    app_id = data.get('app_id')

    if not tv_remote:
        return web.json_response({"error": "Not connected to TV"}, status=400)

    try:
        tv_remote.send_launch_app_command(app_id)
        logger.info(f"Launched app: {app_id}")
        return web.json_response({"status": "ok"})
    except ConnectionClosed:
        logger.error(f"Connection closed while launching app: {app_id}")
        return web.json_response({"error": "Not connected to TV"}, status=400)
    except Exception as e:
        logger.error(f"Error launching app: {e}")
        return web.json_response({"error": str(e)}, status=500)


async def send_text_handler(request):
    """Send text input to TV"""
    data = await request.json()
    text = data.get('text', '')
    send_enter = data.get('enter', False)

    if not tv_remote:
        return web.json_response({"error": "Not connected to TV"}, status=400)

    # Check if connected
    if not hasattr(tv_remote, '_remote_message_protocol') or tv_remote._remote_message_protocol is None:
        logger.warning("Attempted to send text but connection appears closed")
        return web.json_response({"error": "Connection lost"}, status=400)

    if not text:
        return web.json_response({"error": "No text provided"}, status=400)

    try:
        # Use the native send_text method from the library
        logger.info(f"Sending text to TV (len: {len(text)}, enter: {send_enter})")
        tv_remote.send_text(text)

        if send_enter:
            await asyncio.sleep(0.5) # Wait for text to be processed
            logger.info("Sending trailing ENTER key")
            tv_remote.send_key_command('KEYCODE_ENTER')

        return web.json_response({"status": "ok"})
    except (ConnectionClosed, ConnectionError) as e:
        logger.error(f"Connection lost while sending text: {e}")
        return web.json_response({"error": "Connection lost"}, status=400)
    except Exception as e:
        logger.exception(f"Unexpected error sending text: {e}")
        return web.json_response({"error": str(e)}, status=500)


async def events_handler(request):
    """Long polling endpoint for server events"""
    global server_event_futures

    # Create a future for this request
    future = asyncio.Future()
    server_event_futures.append(future)

    logger.debug(f"Client waiting for events. Total waiters: {len(server_event_futures)}")

    try:
        # Wait for an event or timeout (30s)
        event = await asyncio.wait_for(future, timeout=30.0)
        return web.json_response(event)
    except asyncio.TimeoutError:
        # Remove future if it's still there (it should be cleared by broadcast_event but just in case)
        if future in server_event_futures:
            server_event_futures.remove(future)
        return web.json_response({"type": "keepalive"})
    except Exception as e:
        logger.error(f"Error in events handler: {e}")
        if future in server_event_futures:
            server_event_futures.remove(future)
        return web.json_response({"error": str(e)}, status=500)


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
    # Config is already loaded in main, but we can refresh it here
    load_config()
    logger.info("Server started and configuration loaded")


async def on_cleanup(app):
    """Application cleanup handler"""
    global tv_remote
    if tv_remote:
        tv_remote.disconnect()
    logger.info("Server shutdown complete")


async def index_handler(request):
    """Serve the index.html file"""
    index_file = Path(__file__).parent / 'static' / 'index.html'
    return web.FileResponse(index_file)


def create_app():
    """Create and configure the aiohttp application"""
    # Initialize app with our smart middlewares
    app = web.Application(middlewares=[error_middleware, subfolder_middleware])

    # Setup routes at the ROOT
    # These routes are now prefix-agnostic thanks to the middleware
    app.router.add_get('/', index_handler)
    app.router.add_get('/api/status', status_handler)
    app.router.add_post('/api/connect', connect_handler)
    app.router.add_post('/api/pairing_code', pairing_code_handler)
    app.router.add_post('/api/send_key', send_key_handler)
    app.router.add_post('/api/send_text', send_text_handler)
    app.router.add_post('/api/launch_app', launch_app_handler)
    app.router.add_get('/api/events', events_handler)

    # Static files at the ROOT
    app.router.add_static('/icons/', (Path(__file__).parent / 'data' / 'icons').resolve(), name='icons', show_index=True)
    app.router.add_static('/', (Path(__file__).parent / 'static').resolve(), name='static', show_index=True)

    # Setup event handlers
    app.on_startup.append(on_startup)
    app.on_cleanup.append(on_cleanup)

    return app


if __name__ == '__main__':
    load_config()
    app = create_app()
    port = config.get('server_port', 7503)
    logger.info(f"Starting droidtv-remote server on http://0.0.0.0:{port}")
    web.run_app(app, host='0.0.0.0', port=port)
