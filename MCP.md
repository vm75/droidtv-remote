# MCP endpoint

`droidtv-remote` exposes MCP Streamable HTTP at `/mcp` from the same listener as the PWA and REST API.

## Client endpoint

```text
http://<server-ip>:7503/mcp
```

A retained reverse-proxy prefix also works, for example `https://example.com/remote/mcp`.

The endpoint is stateless. It accepts JSON-RPC requests over HTTP `POST`, MCP notifications with no response body, `OPTIONS` for browser clients, and `DELETE` as a no-op session close.

## Tools

| Tool | REST-equivalent behavior |
| --- | --- |
| `status` | Get selected TV status and enabled apps |
| `list_tvs` | List TVs and live states |
| `add_tv` | Add a persistent TV record |
| `forget_tv` | Remove a TV and its generated credentials |
| `set_tv_apps` | Set ordered launcher availability for a TV |
| `list_apps` | List shared launchers |
| `add_app` | Create a launcher, optionally with a base64 icon |
| `update_app` | Edit a launcher or its icon |
| `reorder_apps` | Reorder all shared launchers |
| `delete_app` | Delete a launcher everywhere |
| `connect_tv` | Start connection or pairing |
| `submit_pairing_code` | Submit the TV pairing code |
| `send_key` | Send an Android key code |
| `send_text` | Send IME text and optional Enter |
| `launch_app` | Launch an enabled package or app link |
| `next_event` | Long-poll the next TV-scoped IME event |
| `adb_status` | Authenticated per-TV ADB status, reported separately from Remote v2 |
| `adb_pair` | Secure Wi-Fi ADB pair using explicit endpoint + six-digit code |
| `adb_connect` | Connect ADB to an explicit per-TV endpoint |
| `adb_disconnect` | Disconnect using the selected TV's stored ADB serial |
| `adb_forget` | Forget only the selected TV's local ADB association |
| `adb_device_info` | Read allowlisted device properties and current user |
| `adb_packages` | Read bounded installed-package inventory for the current user |
| `adb_launchables` | Read Leanback launcher components for the current user |
| `install_apk` | Authenticated single-APK install/update using the same bounded backend as REST; MCP base64 is limited to 8 MiB decoded |

## Example initialization

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": {"name": "example", "version": "1.0"}
  }
}
```

## Example tool call

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "send_key",
    "arguments": {
      "tv_id": "<tv-id>",
      "key": "KEYCODE_HOME"
    }
  }
}
```

Tool results include both JSON text content and `structuredContent`. Operational errors are returned as MCP tool errors rather than HTTP errors.

All `adb_*` tools and `install_apk` require the same administrator bearer token as the ADB REST API. Set `DROIDTV_ADB_ADMIN_TOKEN` only in the server environment, then send `Authorization: Bearer <token>` on the MCP HTTP request. Missing or wrong credentials return an MCP tool error with structured `error.code = "unauthorized"`. ADB status/results never include the token or secure pairing code.

## APK installation

`install_apk` accepts `tv_id`, a simple `.apk` `filename`, and `apk_base64`. The decoded APK must be 8 MiB or smaller, while the complete MCP HTTP request is capped at 16 MiB. The decoded bytes are streamed through the same generated mode-`0600` temporary-file path used by REST and are deleted after the call; they are not stored in shared state or retained as an APK repository.

The tool targets only the selected TV's stored ADB serial and uses update semantics equivalent to `adb install -r`. It does not accept arbitrary paths, URLs, ADB flags, package names, or shell fragments, and does not enable downgrade, permission-grant, low-target-SDK, or signing-bypass options. Successful structured results contain SHA-256 and refreshed package/version information when Android's inventory makes the affected package uniquely identifiable. For APKs larger than 8 MiB, use the authenticated REST multipart endpoint, whose default limit is 128 MiB.

## Uploaded icons

`add_app` and `update_app` accept `icon_base64` and `icon_content_type`. Supported types and limits match REST uploads: PNG, JPEG, WebP, or GIF, up to 2 MB, with file-signature validation.

## Security

MCP can control TVs and change persistent configuration. Keep it on a trusted LAN or protect it with an authenticated HTTPS reverse proxy. ADB tools have an additional bearer-token gate and their responses are non-cacheable at the REST surface. Do not expose pairing codes, administrator tokens, TV addresses, certificates, or keys.
