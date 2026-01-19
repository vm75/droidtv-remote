# 📺 droidtv-remote

A simple PWA for controlling Android TV devices, with app shortcuts,  media controls and special keys.

## ✨ Features

- 🎮 **Full Remote Control**: D-Pad navigation, media controls, volume, and power
- 🚀 **Quick App Launcher**: One-tap access to your favorite streaming apps
- 🔐 **Secure Pairing**: Easy pairing with your Android TV
- 📱 **PWA Support**: Install as a native app on your phone
- 🎨 **Premium Design**: Glassmorphism UI with smooth animations
- 🔄 **Auto-Reconnect**: Automatic reconnection to server and TV

## 🛠️ Tech Stack

- **Backend**: Python 3.11+, aiohttp, androidtvremote2
- **Frontend**: Vue 3 (CDN), Tailwind CSS, Material Design Icons
- **Configuration**: YAML

## 📦 Installation

1. **Clone this repository**:
   ```bash
   git clone https://github.com/vm75/droidtv-remote.git
   cd droidtv-remote
   ```

2. **Install Python dependencies**:
   ```bash
   pip install -r requirements.txt
   ```

3. **Configure your TV**:
   - Edit `config.yaml`
   - Set your Android TV's IP address in the `tv_ip` field
   - Customize app shortcuts as needed

4. **Run the server**:
   ```bash
   python server.py
   ```

5. **Open in browser**:
   - Navigate to `http://localhost:7503`
   - Or use your computer's IP to access from other devices: `http://192.168.1.xxx:7503`

### 🐳 Docker

You can also run the remote using Docker. Images are automatically published to Docker Hub and GHCR.

**Docker Hub**:
```bash
docker pull vm75/droidtv-remote:latest
```

**GHCR**:
```bash
docker pull ghcr.io/vm75/droidtv-remote:latest
```

**Using Docker Compose**:
```bash
docker-compose up -d
```

## 🔧 Configuration

Edit `config.yaml` to customize:

```yaml
# Your Android TV's IP address
tv_ip: "192.168.1.100"
tv_name: "Living Room TV"

# Add your favorite apps
apps:
  - name: "Netflix"
    id: "com.netflix.ninja"
    icon: "mdi-netflix"

# Base path for reverse proxy (optional)
base_url_path: "/remote/"  # Useful for hosting at example.com/remote/
```

### Reverse Proxy & Subfolders
If you plan to host the app behind a reverse proxy (like Nginx) in a subfolder:
1. Set `base_url_path` in `config.yaml` (e.g., `base_url_path: "/remote/"`).
2. Use the provided `nginx_subfolder.example` as a template for your Nginx configuration.
3. The sample includes WebSocket support headers, which are recommended for future-proofing.

### Finding Your Android TV's IP Address

1. On your Android TV, go to **Settings** > **Network & Internet** > **Your network**
2. Look for the IP address (e.g., 192.168.1.100)

### Finding App Package IDs

Common app package IDs:
- Netflix: `com.netflix.ninja`
- YouTube: `com.google.android.youtube.tv`
- Disney+: `com.disney.disneyplus`
- Prime Video: `com.amazon.amazonvideo.livingroom`
- Plex: `com.plexapp.android`
- Spotify: `com.spotify.tv.android`

To find others, you can use ADB:
```bash
adb shell pm list packages
```

## 🎯 First Time Setup (Pairing)

1. Start the server and open the web UI
2. The UI will show "Connecting..."
3. A pairing code will appear on your TV screen
4. Enter the code in the modal that appears in the web UI
5. Click "Pair"
6. The certificates (`cert.pem` and `key.pem`) are saved for future connections

## 🎨 Features Overview

### Remote Controls
- **D-Pad**: Navigate menus with up/down/left/right
- **OK Button**: Select items
- **Back/Home**: Navigate system menus
- **Media Controls**: Play/pause, skip, previous
- **Volume**: Up, down, and mute
- **Power**: Turn TV on/off
- **Color Keys**: Red, Green, Yellow, Blue (for apps that use them)

### App Launcher
Quick access to your configured apps - just tap an icon to launch!

## 📱 Installing as PWA

On mobile devices, you can install this as a native app:

**iOS (Safari)**:
1. Tap the Share button
2. Tap "Add to Home Screen"

**Android (Chrome)**:
1. Tap the menu (⋮)
2. Tap "Add to Home screen"

## 🔒 Security

- Communication with Android TV uses SSL/TLS encryption
- Certificates are stored locally in `cert.pem` and `key.pem`
- Server only binds to local network (0.0.0.0:8080)

## 🐛 Troubleshooting

**Can't connect to TV**:
- Ensure your TV and computer are on the same network
- Check that the IP address in `config.yaml` is correct
- Make sure your TV's remote control service is enabled
- Try restarting both the server and your TV

**Connection failed**:
- Check that no firewall is blocking port 7503
- Try accessing via `http://localhost:7503` first

**Pairing modal doesn't appear**:
- Check the server console for errors
- Restart the server and try again
- Delete `cert.pem` and `key.pem` to force re-pairing

## 📄 License

MIT License - See LICENSE file for details

## 🙏 Acknowledgments

- [androidtvremote2](https://github.com/tronikos/androidtvremote2) - Python library for Android TV Remote Protocol v2
- [Vue 3](https://vuejs.org/) - Progressive JavaScript framework
- [Tailwind CSS](https://tailwindcss.com/) - Utility-first CSS framework
- [Material Design Icons](https://materialdesignicons.com/) - Icon library

---

Made with ❤️ for Android TV enthusiasts