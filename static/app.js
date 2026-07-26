/**
 * droidtv-remote Vue Application
 * Uses HTTP requests instead of WebSockets for reliability
 */

const { createApp, ref, computed, nextTick, onMounted, onUnmounted } = Vue;

createApp({
    setup() {
        // Reactive state
        const connectionStatus = ref(false);
        const connecting = ref(false);
        const tvs = ref([]);
        const selectedTvId = ref('');
        const showTvMenu = ref(false);
        const showAddTvModal = ref(false);
        const newTvName = ref('');
        const newTvHost = ref('');
        const addingTv = ref(false);
        const showPairingModal = ref(false);
        const pairingCode = ref('');
        const pairingInProgress = ref(false);
        const apps = ref([]);
        const tvName = ref('Android TV');
        const version = ref('');
        const errorMessage = ref('');
        const keyboardText = ref('');
        const keyboardInput = ref(null);
        const lastSentText = ref('');
        const deferredPrompt = ref(null);
        const showInstallButton = ref(false);
        const updateAvailable = ref(false);
        const showPwaHelp = ref(false);
        const pwaHelpMessage = ref('');
        const autoEnter = ref(true);
        const currentView = ref('remote');
        const allApps = ref([]);
        const configuredTvId = ref('');
        const configuredAppIds = ref([]);
        const savingTvApps = ref(false);
        const showAppModal = ref(false);
        const editingAppId = ref('');
        const appFormName = ref('');
        const appFormPackageId = ref('');
        const appFormIconClass = ref('');
        const appIconFile = ref(null);
        const editingAppHasUploadedIcon = ref(false);
        const removeAppIcon = ref(false);
        const savingApp = ref(false);
        // Cookie helpers to avoid collisions on subpaths
        const getCookiePath = () => {
            let path = window.location.pathname;
            if (path.split('/').pop().includes('.')) {
                path = path.substring(0, path.lastIndexOf('/'));
            }
            return path.endsWith('/') ? path : path + '/';
        };

        const setCookie = (name, value, days = 365) => {
            const d = new Date();
            d.setTime(d.getTime() + (days * 24 * 60 * 60 * 1000));
            const expires = "expires=" + d.toUTCString();
            const path = getCookiePath();
            document.cookie = name + "=" + value + ";" + expires + ";path=" + path + ";SameSite=Lax";
        };

        const getCookie = (name) => {
            const nameEQ = name + "=";
            const ca = document.cookie.split(';');
            for (let i = 0; i < ca.length; i++) {
                let c = ca[i];
                while (c.charAt(0) == ' ') c = c.substring(1, c.length);
                if (c.indexOf(nameEQ) == 0) return c.substring(nameEQ.length, c.length);
            }
            return null;
        };

        const selectedTv = computed(() =>
            tvs.value.find(tv => tv.id === selectedTvId.value) || null
        );
        const connectionIcon = computed(() => {
            if (connectionStatus.value) return 'mdi-television-classic';
            if (connecting.value || pairingInProgress.value) return 'mdi-loading mdi-spin';
            return 'mdi-television-off';
        });
        const connectionIconClass = computed(() => {
            if (connectionStatus.value) return 'text-green-400';
            if (connecting.value || pairingInProgress.value) return 'text-orange-400';
            return 'text-gray-400';
        });
        const connectionLabel = computed(() => {
            if (connectionStatus.value) return 'Connected';
            if (pairingInProgress.value) return 'Pairing';
            if (connecting.value) return 'Connecting';
            return 'Disconnected';
        });
        const tvStorageKey = `droidtvRemote:selectedTv:${getCookiePath()}`;
        const getStoredTvId = () => {
            try {
                return window.localStorage.getItem(tvStorageKey) || '';
            } catch (error) {
                return '';
            }
        };
        const rememberSelectedTv = (tvId) => {
            try {
                if (tvId) {
                    window.localStorage.setItem(tvStorageKey, tvId);
                } else {
                    window.localStorage.removeItem(tvStorageKey);
                }
            } catch (error) {
                console.warn('Unable to remember selected TV:', error);
            }
        };

        // Initialize mute state from cookies (better than localStorage for subfolder scoping)
        const isMuted = ref(getCookie('tvMuted') === 'true');

        let statusCheckInterval = null;
        let statusPollDelay = 0;
        let serverWasUnavailable = false;
        const setStatusPolling = (delay) => {
            if (statusCheckInterval && statusPollDelay === delay) return;
            if (statusCheckInterval) clearInterval(statusCheckInterval);
            statusPollDelay = delay;
            statusCheckInterval = setInterval(checkStatus, delay);
        };

        /**
         * Refresh the managed TV list and restore this client's selection.
         */
        const refreshTvs = async () => {
            const response = await fetch('api/tvs');
            const data = await response.json();
            if (!response.ok) {
                throw new Error(data.error || 'Failed to load TVs');
            }
            tvs.value = data.tvs || [];
            const currentExists = tvs.value.some(tv => tv.id === selectedTvId.value);
            const storedTvId = getStoredTvId();
            const storedExists = tvs.value.some(tv => tv.id === storedTvId);
            if (!currentExists) {
                selectedTvId.value = storedExists ? storedTvId : (tvs.value[0]?.id || '');
            }
            rememberSelectedTv(selectedTvId.value);
            tvName.value = selectedTv.value?.name || 'No TV selected';
            const configuredTvExists = tvs.value.some(tv => tv.id === configuredTvId.value);
            if (!configuredTvExists) {
                configuredTvId.value = selectedTvId.value || tvs.value[0]?.id || '';
            }
            syncConfiguredApps();
        };

        /**
         * Check connection status for the selected TV.
         */
        const checkStatus = async () => {
            if (!selectedTvId.value) {
                connectionStatus.value = false;
                connecting.value = false;
                pairingInProgress.value = false;
                tvName.value = 'No TV selected';
                return;
            }
            try {
                const response = await fetch(`api/status?tv_id=${encodeURIComponent(selectedTvId.value)}`);
                const data = await response.json();
                const reconnectAfterServerRestart = serverWasUnavailable &&
                    !data.connected && !data.connecting && !data.pairing_in_progress;
                serverWasUnavailable = false;
                connectionStatus.value = Boolean(data.connected);
                connecting.value = Boolean(data.connecting);
                pairingInProgress.value = Boolean(data.pairing_in_progress);
                tvName.value = data.tv_name || selectedTv.value?.name || 'Android TV';
                apps.value = data.apps || [];
                version.value = data.version || version.value;

                const tv = tvs.value.find(item => item.id === selectedTvId.value);
                if (tv) {
                    tv.connected = connectionStatus.value;
                    tv.connecting = connecting.value;
                    tv.pairing_in_progress = pairingInProgress.value;
                }
                if (data.pairing_in_progress && !showPairingModal.value) {
                    showPairingModal.value = true;
                    pairingCode.value = '';
                }
                if (data.connected && showPairingModal.value) {
                    showPairingModal.value = false;
                    pairingCode.value = '';
                }
                setStatusPolling(
                    data.connecting || data.pairing_in_progress ? 500 : 2000
                );
                if (reconnectAfterServerRestart) connectToTV();
            } catch (error) {
                console.error('Error checking status:', error);
                connectionStatus.value = false;
                connecting.value = false;
                serverWasUnavailable = true;
            }
        };

        /**
         * Connect to the selected TV. Called automatically on open and change.
         */
        const connectToTV = async () => {
            if (!selectedTvId.value) {
                showAddTvModal.value = true;
                return;
            }
            connecting.value = true;
            try {
                const response = await fetch('api/connect', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ tv_id: selectedTvId.value })
                });
                const data = await response.json();
                if (!response.ok) {
                    throw new Error(data.error || 'Failed to connect');
                }
                setStatusPolling(500);
                await checkStatus();
            } catch (error) {
                console.error('Error connecting:', error);
                connecting.value = false;
                showError(error.message || 'Failed to connect to server');
            }
        };

        const selectTv = async (tvId) => {
            const changed = tvId !== selectedTvId.value;
            selectedTvId.value = tvId;
            rememberSelectedTv(tvId);
            showTvMenu.value = false;
            showPairingModal.value = false;
            connectionStatus.value = false;
            pairingInProgress.value = false;
            tvName.value = selectedTv.value?.name || 'Android TV';
            await checkStatus();
            if (changed || !connectionStatus.value) await connectToTV();
        };

        const toggleTvMenu = async () => {
            showTvMenu.value = !showTvMenu.value;
            if (showTvMenu.value) {
                try {
                    await refreshTvs();
                } catch (error) {
                    showError('Failed to refresh TV status');
                }
            }
        };

        const openAddTv = () => {
            showTvMenu.value = false;
            showAddTvModal.value = true;
        };

        const addTv = async () => {
            const name = newTvName.value.trim();
            const host = newTvHost.value.trim();
            if (!name || !host) {
                showError('Enter a name and IP address for the TV');
                return;
            }
            addingTv.value = true;
            try {
                const response = await fetch('api/tvs', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ name, host })
                });
                const data = await response.json();
                if (!response.ok) throw new Error(data.error || 'Failed to add TV');
                tvs.value.push(data.tv);
                newTvName.value = '';
                newTvHost.value = '';
                showAddTvModal.value = false;
                await selectTv(data.tv.id);
            } catch (error) {
                showError(error.message || 'Failed to add TV');
            } finally {
                addingTv.value = false;
            }
        };

        const forgetTv = async (tv) => {
            if (!window.confirm(`Forget ${tv.name}? You will need to pair with it again.`)) return;
            try {
                const response = await fetch(`api/tvs/${encodeURIComponent(tv.id)}`, {
                    method: 'DELETE'
                });
                const data = await response.json();
                if (!response.ok) throw new Error(data.error || 'Failed to forget TV');
                const forgotSelectedTv = tv.id === selectedTvId.value;
                if (forgotSelectedTv) selectedTvId.value = '';
                await refreshTvs();
                if (forgotSelectedTv && selectedTvId.value) {
                    await checkStatus();
                    await connectToTV();
                }
            } catch (error) {
                showError(error.message || 'Failed to forget TV');
            }
        };

        const syncConfiguredApps = () => {
            const tv = tvs.value.find(item => item.id === configuredTvId.value);
            configuredAppIds.value = tv ? [...(tv.app_ids || [])] : [];
        };

        const loadApps = async () => {
            const response = await fetch('api/apps');
            const data = await response.json();
            if (!response.ok) throw new Error(data.error || 'Failed to load app launchers');
            allApps.value = data.apps || [];
        };

        const openLauncherView = async () => {
            currentView.value = 'apps';
            showTvMenu.value = false;
            configuredTvId.value = selectedTvId.value || tvs.value[0]?.id || '';
            try {
                await Promise.all([loadApps(), refreshTvs()]);
                syncConfiguredApps();
            } catch (error) {
                showError(error.message || 'Failed to load app launchers');
            }
        };

        const openRemoteView = () => {
            currentView.value = 'remote';
            checkStatus();
        };

        const selectConfiguredTv = (tvId) => {
            configuredTvId.value = tvId;
            syncConfiguredApps();
        };

        const saveTvAppConfiguration = async () => {
            if (!configuredTvId.value) return;
            savingTvApps.value = true;
            try {
                const response = await fetch(
                    `api/tvs/${encodeURIComponent(configuredTvId.value)}/apps`,
                    {
                        method: 'PUT',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ app_ids: configuredAppIds.value })
                    }
                );
                const data = await response.json();
                if (!response.ok) throw new Error(data.error || 'Failed to save TV apps');
                const index = tvs.value.findIndex(tv => tv.id === data.tv.id);
                if (index !== -1) tvs.value[index] = data.tv;
                if (configuredTvId.value === selectedTvId.value) await checkStatus();
            } catch (error) {
                showError(error.message || 'Failed to save TV apps');
            } finally {
                savingTvApps.value = false;
            }
        };

        const resetAppForm = () => {
            editingAppId.value = '';
            appFormName.value = '';
            appFormPackageId.value = '';
            appFormIconClass.value = '';
            appIconFile.value = null;
            editingAppHasUploadedIcon.value = false;
            removeAppIcon.value = false;
        };

        const openAddApp = () => {
            resetAppForm();
            showAppModal.value = true;
        };

        const openEditApp = (app) => {
            editingAppId.value = app.id;
            appFormName.value = app.name;
            appFormPackageId.value = app.package_id;
            appFormIconClass.value = app.icon_class || (app.has_uploaded_icon ? '' : (app.icon || ''));
            appIconFile.value = null;
            editingAppHasUploadedIcon.value = Boolean(app.has_uploaded_icon);
            removeAppIcon.value = false;
            showAppModal.value = true;
        };

        const handleAppIconChange = (event) => {
            appIconFile.value = event.target.files?.[0] || null;
            if (appIconFile.value) removeAppIcon.value = false;
        };

        const saveApp = async () => {
            const name = appFormName.value.trim();
            const packageId = appFormPackageId.value.trim();
            if (!name || !packageId) {
                showError('Enter an app name and Android package ID');
                return;
            }
            savingApp.value = true;
            const formData = new FormData();
            formData.append('name', name);
            formData.append('package_id', packageId);
            formData.append('icon_class', appFormIconClass.value.trim());
            if (appIconFile.value) formData.append('icon_file', appIconFile.value);
            if (removeAppIcon.value && !appIconFile.value) {
                formData.append('remove_icon', 'true');
            }
            const editing = Boolean(editingAppId.value);
            const url = editing
                ? `api/apps/${encodeURIComponent(editingAppId.value)}`
                : 'api/apps';
            try {
                const response = await fetch(url, {
                    method: editing ? 'PUT' : 'POST',
                    body: formData
                });
                const data = await response.json();
                if (!response.ok) throw new Error(data.error || 'Failed to save app launcher');
                showAppModal.value = false;
                resetAppForm();
                await Promise.all([loadApps(), refreshTvs()]);
                syncConfiguredApps();
                if (selectedTvId.value) await checkStatus();
            } catch (error) {
                showError(error.message || 'Failed to save app launcher');
            } finally {
                savingApp.value = false;
            }
        };

        const deleteApp = async (app) => {
            if (!window.confirm(`Delete ${app.name} from every TV?`)) return;
            try {
                const response = await fetch(`api/apps/${encodeURIComponent(app.id)}`, {
                    method: 'DELETE'
                });
                const data = await response.json();
                if (!response.ok) throw new Error(data.error || 'Failed to delete app launcher');
                await Promise.all([loadApps(), refreshTvs()]);
                syncConfiguredApps();
                if (selectedTvId.value) await checkStatus();
            } catch (error) {
                showError(error.message || 'Failed to delete app launcher');
            }
        };

        /**
         * Send key press to Android TV
         */
        const sendKey = async (keyCode) => {
            if (!connectionStatus.value) {
                showError('Not connected to TV');
                return;
            }

            // Track mute state
            if (keyCode === 'KEYCODE_VOLUME_MUTE') {
                isMuted.value = !isMuted.value;
                setCookie('tvMuted', isMuted.value.toString());
                console.log('Mute state:', isMuted.value);
            }

            // Special handling for HOME button to prevent unmute side-effect
            if (keyCode === 'KEYCODE_HOME') {
                const wasMuted = isMuted.value;
                console.log('Sending HOME' + (wasMuted ? ' with mute restoration' : ''));
                try {
                    // Send HOME
                    const homeResponse = await fetch('api/send_key', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ key: 'KEYCODE_HOME', tv_id: selectedTvId.value })
                    });

                    if (!homeResponse.ok) {
                        const data = await homeResponse.json();
                        showError(data.error || 'Failed to send key');
                        return;
                    }

                    // Always restore mute if it was muted
                    if (wasMuted) {
                        // Wait a bit for home to process
                        await new Promise(resolve => setTimeout(resolve, 400));

                        // Restore mute state
                        await fetch('api/send_key', {
                            method: 'POST',
                            headers: { 'Content-Type': 'application/json' },
                            body: JSON.stringify({ key: 'KEYCODE_VOLUME_MUTE', tv_id: selectedTvId.value })
                        });
                        console.log('Mute restored after HOME');
                    }

                    if (navigator.vibrate) {
                        navigator.vibrate(50);
                    }
                    return;
                } catch (error) {
                    console.error('Error sending key:', error);
                    showError('Failed to send key');
                    return;
                }
            }

            console.log('Sending key:', keyCode);
            try {
                const response = await fetch('api/send_key', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ key: keyCode, tv_id: selectedTvId.value })
                });

                if (!response.ok) {
                    const data = await response.json();
                    showError(data.error || 'Failed to send key');
                }

                // Haptic feedback (if supported)
                if (navigator.vibrate) {
                    navigator.vibrate(50);
                }
            } catch (error) {
                console.error('Error sending key:', error);
                showError('Failed to send key');
            }
        };

        /**
         * Launch an app on Android TV
         */
        const launchApp = async (appId) => {
            if (!connectionStatus.value) {
                showError('Not connected to TV');
                return;
            }

            console.log('Launching app:', appId);
            try {
                const response = await fetch('api/launch_app', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ launcher_id: appId, tv_id: selectedTvId.value })
                });

                if (!response.ok) {
                    const data = await response.json();
                    showError(data.error || 'Failed to launch app');
                }

                // Haptic feedback
                if (navigator.vibrate) {
                    navigator.vibrate([50, 100, 50]);
                }
            } catch (error) {
                console.error('Error launching app:', error);
                showError('Failed to launch app');
            }
        };

        /**
         * Submit pairing code to server
         */
        const submitPairingCode = async () => {
            if (!pairingCode.value || pairingCode.value.length < 4) {
                showError('Please enter a valid pairing code');
                return;
            }

            console.log('Submitting pairing code:', pairingCode.value);
            pairingInProgress.value = true;

            try {
                const response = await fetch('api/pairing_code', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ code: pairingCode.value, tv_id: selectedTvId.value })
                });

                const data = await response.json();
                console.log('Pairing code response:', data);

                if (!response.ok) {
                    showError(data.error || 'Failed to submit pairing code');
                    pairingInProgress.value = false;
                } else {
                    // Keep checking status to see when pairing completes
                    setStatusPolling(500);
                }
            } catch (error) {
                console.error('Error submitting pairing code:', error);
                showError('Failed to submit pairing code');
                pairingInProgress.value = false;
            }
        };

        /**
         * Close pairing modal
         */
        const closePairingModal = () => {
            showPairingModal.value = false;
            pairingCode.value = '';
            pairingInProgress.value = false;
        };

        /**
         * Show error message
         */
        const showError = (message) => {
            errorMessage.value = message;

            // Auto-hide after 5 seconds
            setTimeout(() => {
                errorMessage.value = '';
            }, 5000);
        };



        /**
         * Robust keyboard input handling
         */
        const handleKeyboardInput = (event) => {
            const current = keyboardText.value;
            const previous = lastSentText.value;

            // Update lastSentText IMMEDIATELY to prevent double-processing if events fire fast
            lastSentText.value = current;

            if (current === previous) return;

            // Common case: typing characters at the end
            if (current.startsWith(previous)) {
                const added = current.slice(previous.length);
                if (added.length > 0) {
                    sendText(added);
                }
            }
            // Common case: backspacing at the end
            else if (previous.startsWith(current)) {
                const diff = previous.length - current.length;
                for (let i = 0; i < diff; i++) {
                    sendKey('KEYCODE_DEL');
                }
            }
            // Edge case: replacement, middle-typing, or pasting
            else {
                // Find shared prefix
                let i = 0;
                while (i < current.length && i < previous.length && current[i] === previous[i]) {
                    i++;
                }

                // Delete diverging part of previous
                const toDelete = previous.length - i;
                for (let d = 0; d < toDelete; d++) {
                    sendKey('KEYCODE_DEL');
                }

                // Add diverging part of current
                const toAdd = current.slice(i);
                if (toAdd.length > 0) {
                    sendText(toAdd);
                }
            }
        };

        /**
         * Global keyboard listener for D-pad and navigation
         */
        const handleGlobalKeyDown = (event) => {
            // If the user is typing in an input field, don't trigger global hotkeys
            const isInputFocus = event.target.tagName === 'INPUT' ||
                event.target.tagName === 'TEXTAREA' ||
                event.target.isContentEditable;

            if (isInputFocus) {
                return;
            }

            const keyMap = {
                'ArrowUp': 'KEYCODE_DPAD_UP',
                'ArrowDown': 'KEYCODE_DPAD_DOWN',
                'ArrowLeft': 'KEYCODE_DPAD_LEFT',
                'ArrowRight': 'KEYCODE_DPAD_RIGHT',
                'Enter': 'KEYCODE_DPAD_CENTER',
                'Backspace': 'KEYCODE_BACK',
                'Escape': 'KEYCODE_BACK',
                'h': 'KEYCODE_HOME',
                'Home': 'KEYCODE_HOME',
                ' ': 'KEYCODE_MEDIA_PLAY_PAUSE',
            };

            const keyCode = keyMap[event.key];
            if (keyCode) {
                event.preventDefault();
                sendKey(keyCode);
            }
        };

        /**
         * Send text to Android TV
         */
        const sendText = (text) => {
            if (!connectionStatus.value || !text) return;

            try {
                fetch('api/send_text', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ text: text, tv_id: selectedTvId.value })
                });

                if (navigator.vibrate) {
                    navigator.vibrate(10);
                }
            } catch (error) {
                console.error('Error sending text:', error);
            }
        };

        /**
         * Send the entire text block at once (Better for Google TV Search)
         */
        const submitFullText = async () => {
            if (!keyboardText.value || !connectionStatus.value) return;

            try {
                await fetch('api/send_text', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        text: keyboardText.value,
                        enter: autoEnter.value,
                        tv_id: selectedTvId.value
                    })
                });

                // Set lastSentText so characters aren't re-sent if typing continues
                lastSentText.value = keyboardText.value;

                if (navigator.vibrate) {
                    navigator.vibrate([50, 30, 50]);
                }
            } catch (error) {
                console.error('Error submitting full text:', error);
                showError('Failed to send text');
            }
        };

        /**
         * Handle keydown for special keys like Enter
         */
        const handleKeyDown = (event) => {
            if (event.key === 'Enter') {
                sendKey('KEYCODE_ENTER');
            } else if (event.key === 'Backspace' && keyboardText.value === '') {
                // If input is empty, still send backspace to TV
                sendKey('KEYCODE_DEL');
            }
        };

        /**
         * Send special key (backspace, enter, space)
         */
        const sendSpecialKey = (keyCode) => {
            sendKey(keyCode);
            // Refocus input after clicking a button
            if (keyboardInput.value) {
                keyboardInput.value.focus();
            }
        };

        /**
         * Clear keyboard text
         */
        const clearKeyboardText = () => {
            keyboardText.value = '';
            lastSentText.value = '';
            setTimeout(() => {
                if (keyboardInput.value) {
                    keyboardInput.value.focus();
                }
            }, 50);
        };

        /**
         * Install the app
         */
        const installApp = async () => {
            if (!deferredPrompt.value) return;

            // Show the install prompt
            deferredPrompt.value.prompt();

            // Wait for the user to respond to the prompt
            const { outcome } = await deferredPrompt.value.userChoice;
            console.log(`User response to the install prompt: ${outcome}`);

            // We've used the prompt, and can't use it again, throw it away
            deferredPrompt.value = null;
            showInstallButton.value = false;
        };

        /**
         * Refresh the page to apply updates
         */
        const refreshApp = () => {
            if ('serviceWorker' in navigator) {
                navigator.serviceWorker.getRegistration().then((registration) => {
                    if (registration && registration.waiting) {
                        registration.waiting.postMessage({ type: 'SKIP_WAITING' });
                    }
                    window.location.reload();
                });
            } else {
                window.location.reload();
            }
        };

        /**
         * Listen for server events (Long Polling)
         */
        const listenForEvents = async () => {
            if (!connectionStatus.value) {
                // effective waiting if not connected
                setTimeout(listenForEvents, 3000);
                return;
            }

            try {
                const eventTvId = selectedTvId.value;
                const response = await fetch(`api/events?tv_id=${encodeURIComponent(eventTvId)}`);
                if (response.ok) {
                    const event = await response.json();
                    if (eventTvId !== selectedTvId.value) return listenForEvents();

                    if (event.type === 'ime_show' || event.type === 'ime_focus') {
                        console.log('IME event received:', event.data);

                        // Update current text if provided
                        if (event.data && event.data.value !== undefined) {
                            keyboardText.value = event.data.value;
                            lastSentText.value = event.data.value; // Sync to avoid re-sending
                        }

                        // Focus the keyboard input and scroll it into view
                        await nextTick();
                        if (keyboardInput.value) {
                            keyboardInput.value.focus();
                            keyboardInput.value.scrollIntoView({ behavior: 'smooth', block: 'center' });

                            if (event.data) {
                                const textLength = keyboardText.value.length;
                                const start = Math.max(0, Math.min(event.data.start ?? textLength, textLength));
                                const end = Math.max(start, Math.min(event.data.end ?? start, textLength));
                                keyboardInput.value.setSelectionRange(start, end);
                            }

                            // Visual feedback
                            console.log("Keyboard auto-focused!");
                        }
                    }
                }
            } catch (e) {
                console.error("Error listening for events:", e);
                // Wait a bit before retrying on error
                await new Promise(r => setTimeout(r, 2000));
            }

            // Loop
            if (!document.hidden) {
                listenForEvents();
            } else {
                // precise backoff if tab hidden
                setTimeout(listenForEvents, 1000);
            }
        };

        /**
         * Lifecycle: Component mounted
         */
        onMounted(() => {
            console.log('Vue app mounted');

            // Add global keyboard listener
            window.addEventListener('keydown', handleGlobalKeyDown);

            // Detect standalone mode (already installed and running)
            const isStandalone = window.matchMedia('(display-mode: standalone)').matches || window.navigator.standalone === true;
            if (isStandalone) {
                console.log('App is already running in standalone mode');
                showInstallButton.value = false;
            }


            // Restore this client's last TV and connect without an extra tap.
            refreshTvs().then(async () => {
                await checkStatus();
                if (selectedTvId.value) {
                    await connectToTV();
                }
            }).catch((error) => {
                console.error('Failed to initialize TVs:', error);
                showError('Failed to load configured TVs');
            });

            // Keep status icons current after the initial connection attempt.
            setStatusPolling(2000);

            // Start listening for server events (long polling)
            listenForEvents();

            // Check for secure context
            if (!window.isSecureContext && window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1') {
                console.warn('PWA installation requires HTTPS or localhost. Current context is not secure.');
            }

            // Detect iOS
            const isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent) && !window.MSStream;
            if (isIOS && !isStandalone) {
                console.log('iOS detected: To install, tap Share and "Add to Home Screen"');
                pwaHelpMessage.value = 'On iOS: Tap Share icon then "Add to Home Screen"';
                showPwaHelp.value = true;
            }

            // Diagnostic timer to show why install button might be missing
            setTimeout(async () => {
                const currentIsStandalone = window.matchMedia('(display-mode: standalone)').matches || window.navigator.standalone === true;

                if (!showInstallButton.value && !currentIsStandalone && !isIOS) {
                    if (!window.isSecureContext && window.location.hostname !== 'localhost') {
                        pwaHelpMessage.value = 'PWA requires HTTPS (currently using insecure HTTP)';
                        showPwaHelp.value = true;
                    } else {
                        // Check if service worker is registered. If it is, and we're here,
                        // it's likely already installed or requirements are met but browser is waiting.
                        try {
                            const registrations = await navigator.serviceWorker.getRegistrations();
                            if (registrations.length === 0) {
                                pwaHelpMessage.value = 'PWA requirements not met';
                                showPwaHelp.value = true;
                            } else {
                                // Likely already installed, hide the help message
                                showPwaHelp.value = false;
                            }
                        } catch (e) {
                            pwaHelpMessage.value = 'PWA requirements not met';
                            showPwaHelp.value = true;
                        }
                    }
                }
            }, 5000);

            // Listen for beforeinstallprompt event
            window.addEventListener('beforeinstallprompt', (e) => {
                console.log('beforeinstallprompt event fired');
                // Prevent Chrome 67 and earlier from automatically showing the prompt
                e.preventDefault();
                // Stash the event so it can be triggered later.
                deferredPrompt.value = e;
                // Update UI notify the user they can install the PWA
                showInstallButton.value = true;
            });

            // Listen for appinstalled event
            window.addEventListener('appinstalled', (evt) => {
                console.log('App was installed');
                showInstallButton.value = false;
                deferredPrompt.value = null;
            });

            // Register service worker for PWA
            if ('serviceWorker' in navigator) {
                navigator.serviceWorker.register('sw.js?v=6').then((registration) => {
                    console.log('Service worker registered successfully with scope:', registration.scope);

                    // Check for updates
                    registration.onupdatefound = () => {
                        console.log('New update found, installing...');
                        const installingWorker = registration.installing;
                        installingWorker.onstatechange = () => {
                            console.log('Service worker state changed to:', installingWorker.state);
                            if (installingWorker.state === 'installed') {
                                if (navigator.serviceWorker.controller) {
                                    // New content is available; please refresh.
                                    console.log('New content is available; please refresh.');
                                    updateAvailable.value = true;
                                } else {
                                    // Content is cached for offline use.
                                    console.log('Content is cached for offline use.');
                                }
                            }
                        };
                    };
                }).catch((error) => {
                    console.error('Service worker registration failed:', error);
                });
            } else {
                console.warn('Service workers are not supported in this browser.');
            }
        });

        /**
         * Lifecycle: Component unmounted
         */
        onUnmounted(() => {
            console.log('Vue app unmounted');

            // Remove global keyboard listener
            window.removeEventListener('keydown', handleGlobalKeyDown);

            // Clean up interval
            if (statusCheckInterval) {
                clearInterval(statusCheckInterval);
            }
        });

        // Return reactive state and methods to template
        return {
            // State
            connectionStatus,
            connecting,
            tvs,
            selectedTv,
            selectedTvId,
            showTvMenu,
            showAddTvModal,
            newTvName,
            newTvHost,
            addingTv,
            connectionIcon,
            connectionIconClass,
            connectionLabel,
            showPairingModal,
            pairingCode,
            pairingInProgress,
            apps,
            tvName,
            version,
            errorMessage,
            keyboardText,
            keyboardInput,
            lastSentText,
            showInstallButton,
            updateAvailable,
            showPwaHelp,
            pwaHelpMessage,
            currentView,
            allApps,
            configuredTvId,
            configuredAppIds,
            savingTvApps,
            showAppModal,
            editingAppId,
            appFormName,
            appFormPackageId,
            appFormIconClass,
            appIconFile,
            editingAppHasUploadedIcon,
            removeAppIcon,
            savingApp,

            // Methods
            sendKey,
            launchApp,
            submitPairingCode,
            closePairingModal,
            connectToTV,
            selectTv,
            toggleTvMenu,
            openAddTv,
            addTv,
            forgetTv,
            openLauncherView,
            openRemoteView,
            selectConfiguredTv,
            saveTvAppConfiguration,
            openAddApp,
            openEditApp,
            handleAppIconChange,
            saveApp,
            deleteApp,
            handleKeyboardInput,
            handleKeyDown,
            sendSpecialKey,
            clearKeyboardText,
            submitFullText,
            autoEnter,
            installApp,
            refreshApp
        };
    }
}).mount('#app');
