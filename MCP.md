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

The five `adb_*` tools require the same administrator bearer token as the ADB REST API. Set `DROIDTV_ADB_ADMIN_TOKEN` only in the server environment, then send `Authorization: Bearer <token>` on the MCP HTTP request. Missing or wrong credentials return an MCP tool error with structured `error.code = "unauthorized"`. ADB status/results never include the token or secure pairing code.

## Uploaded icons

`add_app` and `update_app` accept `icon_base64` and `icon_content_type`. Supported types and limits match REST uploads: PNG, JPEG, WebP, or GIF, up to 2 MB, with file-signature validation.

## Security

MCP can control TVs and change persistent configuration. Keep it on a trusted LAN or protect it with an authenticated HTTPS reverse proxy. ADB tools have an additional bearer-token gate and their responses are non-cacheable at the REST surface. Do not expose pairing codes, administrator tokens, TV addresses, certificates, or keys.
