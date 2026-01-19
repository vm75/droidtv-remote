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
        const showKeyboardModal = ref(false);
        const keyboardText = ref('');
        const keyboardInput = ref(null);
        const lastSentText = ref('');
        const deferredPrompt = ref(null);
        const showInstallButton = ref(false);
        const updateAvailable = ref(false);
        const showPwaHelp = ref(false);
        const pwaHelpMessage = ref('');

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
         * Open keyboard input modal
         */
        const openKeyboard = () => {
            if (!connectionStatus.value) {
                showError('Not connected to TV');
                return;
            }

            showKeyboardModal.value = true;
            keyboardText.value = '';
            lastSentText.value = '';

            // Focus the input after modal opens
            setTimeout(() => {
                if (keyboardInput.value) {
                    keyboardInput.value.focus();
                }
            }, 100);
        };

        /**
         * Close keyboard input modal
         */
        const closeKeyboard = () => {
            showKeyboardModal.value = false;
            keyboardText.value = '';
            lastSentText.value = '';
        };

        /**
         * Handle real-time keyboard input
         */
        const handleKeyboardInput = async () => {
            const current = keyboardText.value;
            const previous = lastSentText.value;

            if (current.length > previous.length) {
                // Character added
                const added = current.slice(previous.length);
                for (const char of added) {
                    await sendTextChar(char);
                }
            } else if (current.length < previous.length) {
                // Character removed
                const diff = previous.length - current.length;
                for (let i = 0; i < diff; i++) {
                    await sendKey('KEYCODE_DEL');
                }
            }
            lastSentText.value = current;
        };

        /**
         * Send a single character as text
         */
        const sendTextChar = async (char) => {
            if (!connectionStatus.value) return;

            try {
                await fetch('api/send_text', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ text: char })
                });

                if (navigator.vibrate) {
                    navigator.vibrate(10);
                }
            } catch (error) {
                console.error('Error sending character:', error);
            }
        };

        /**
         * Handle keydown for special keys like Enter
         */
        const handleKeyDown = (event) => {
            if (event.key === 'Enter') {
                sendKey('KEYCODE_ENTER');
            }
        };

        /**
         * Send special key (backspace, enter, space)
         */
        const sendSpecialKey = (keyCode) => {
            sendKey(keyCode);
        };

        /**
         * Clear keyboard text
         */
        const clearKeyboardText = () => {
            keyboardText.value = '';
            lastSentText.value = '';
            if (keyboardInput.value) {
                keyboardInput.value.focus();
            }
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
         * Lifecycle: Component mounted
         */
        onMounted(() => {
            console.log('Vue app mounted');

            // Hide install button if already in standalone mode
            if (window.matchMedia('(display-mode: standalone)').matches || window.navigator.standalone === true) {
                console.log('App is already running in standalone mode');
                showInstallButton.value = false;
            }

            // Check status immediately
            checkStatus();

            // Check status every 2 seconds
            statusCheckInterval = setInterval(checkStatus, 2000);

            // Check for secure context
            if (!window.isSecureContext && window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1') {
                console.warn('PWA installation requires HTTPS or localhost. Current context is not secure.');
            }

            // Detect iOS
            const isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent) && !window.MSStream;
            if (isIOS && !window.navigator.standalone) {
                console.log('iOS detected: To install, tap Share and "Add to Home Screen"');
                pwaHelpMessage.value = 'On iOS: Tap Share icon then "Add to Home Screen"';
                showPwaHelp.value = true;
            }

            // Diagnostic timer
            setTimeout(() => {
                if (!showInstallButton.value && !window.navigator.standalone && !isIOS) {
                    if (!window.isSecureContext && window.location.hostname !== 'localhost') {
                        pwaHelpMessage.value = 'PWA requires HTTPS (currently using insecure HTTP)';
                    } else {
                        pwaHelpMessage.value = 'PWA requirements not met or already installed';
                    }
                    showPwaHelp.value = true;
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
            showKeyboardModal,
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
            openKeyboard,
            closeKeyboard,
            handleKeyboardInput,
            handleKeyDown,
            sendSpecialKey,
            clearKeyboardText,
            installApp,
            refreshApp
        };
    }
}).mount('#app');
