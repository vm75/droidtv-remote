/**
 * droidtv-remote Vue Application
 * Uses HTTP requests instead of WebSockets for reliability
 */

const { createApp, ref, onMounted, onUnmounted } = Vue;

createApp({
    setup() {
        // Reactive state
        const connectionStatus = ref(false);
        const showPairingModal = ref(false);
        const pairingCode = ref('');
        const pairingInProgress = ref(false);
        const apps = ref([]);
        const tvName = ref('Android TV');
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
        // Initialize mute state from localStorage (assume muted by default for safety)
        const isMuted = ref(localStorage.getItem('tvMuted') === 'true');

        let statusCheckInterval = null;

        /**
         * Check connection status
         */
        const checkStatus = async () => {
            try {
                const response = await fetch('api/status');
                const data = await response.json();

                connectionStatus.value = data.connected;
                tvName.value = data.tv_name || 'Android TV';
                apps.value = data.apps || [];

                // Show pairing modal if pairing is in progress
                if (data.pairing_in_progress && !showPairingModal.value) {
                    showPairingModal.value = true;
                    pairingCode.value = '';
                    pairingInProgress.value = false;
                }

                // Close pairing modal if connected
                if (data.connected && showPairingModal.value) {
                    showPairingModal.value = false;
                    pairingInProgress.value = false;
                }
            } catch (error) {
                console.error('Error checking status:', error);
            }
        };

        /**
         * Connect to TV
         */
        const connectToTV = async () => {
            console.log('Connecting to TV...');
            try {
                const response = await fetch('api/connect', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' }
                });
                const data = await response.json();
                console.log('Connect response:', data);

                // Start checking status more frequently
                if (statusCheckInterval) {
                    clearInterval(statusCheckInterval);
                }
                statusCheckInterval = setInterval(checkStatus, 500);
            } catch (error) {
                console.error('Error connecting:', error);
                showError('Failed to connect to server');
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
                localStorage.setItem('tvMuted', isMuted.value.toString());
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
                        body: JSON.stringify({ key: 'KEYCODE_HOME' })
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
                            body: JSON.stringify({ key: 'KEYCODE_VOLUME_MUTE' })
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
                    body: JSON.stringify({ key: keyCode })
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
                    body: JSON.stringify({ app_id: appId })
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
                    body: JSON.stringify({ code: pairingCode.value })
                });

                const data = await response.json();
                console.log('Pairing code response:', data);

                if (!response.ok) {
                    showError(data.error || 'Failed to submit pairing code');
                    pairingInProgress.value = false;
                } else {
                    // Keep checking status to see when pairing completes
                    if (statusCheckInterval) {
                        clearInterval(statusCheckInterval);
                    }
                    statusCheckInterval = setInterval(checkStatus, 500);
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
                    body: JSON.stringify({ text: text })
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
                        enter: autoEnter.value
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
                const response = await fetch('api/events');
                if (response.ok) {
                    const event = await response.json();

                    if (event.type === 'ime_show') {
                        console.log('IME Show event received:', event.data);

                        // Update current text if provided
                        if (event.data && event.data.value !== undefined) {
                            keyboardText.value = event.data.value;
                            lastSentText.value = event.data.value; // Sync to avoid re-sending
                        }

                        // Focus the keyboard input and scroll it into view
                        if (keyboardInput.value) {
                            keyboardInput.value.focus();
                            keyboardInput.value.scrollIntoView({ behavior: 'smooth', block: 'center' });

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


            // Check status immediately
            checkStatus();

            // Check status every 2 seconds
            statusCheckInterval = setInterval(checkStatus, 2000);

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
                navigator.serviceWorker.register('sw.js?v=3').then((registration) => {
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
            showPairingModal,
            pairingCode,
            pairingInProgress,
            apps,
            tvName,
            errorMessage,
            keyboardText,
            keyboardInput,
            lastSentText,
            showInstallButton,
            updateAvailable,
            showPwaHelp,
            pwaHelpMessage,

            // Methods
            sendKey,
            launchApp,
            submitPairingCode,
            closePairingModal,
            connectToTV,
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
