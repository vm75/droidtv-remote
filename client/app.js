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

        // ADB administration state is deliberately separate from Remote v2.
        const adbTokenInput = ref('');
        const adbTokenConfigured = ref(false);
        const adbStatus = ref(null);
        const adbLoading = ref(false);
        const adbError = ref('');
        const adbMessage = ref('');
        const adbSetupMode = ref('legacy');
        const adbLegacyHost = ref('');
        const adbLegacyPort = ref('5555');
        const adbPairHost = ref('');
        const adbPairPort = ref('');
        const adbPairCode = ref('');
        const adbConnectHost = ref('');
        const adbConnectPort = ref('');
        const adbDiscoveryLoading = ref(false);
        const adbDiscoveryMode = ref('launchable');
        const adbDiscoveryPackages = ref([]);
        const adbDiscoveryWarnings = ref([]);
        const adbDiscoveryError = ref('');
        const adbDiscoveryCurrentUser = ref(null);
        const adbDiscoveryPreview = ref(false);
        const adbImporting = ref(false);
        const adbPackageMutating = ref('');
        const adbPackageError = ref('');
        const adbPackageMessage = ref('');
        const adbAPKFile = ref(null);
        const adbAPKUploading = ref(false);
        const adbAPKProgress = ref(null);
        const adbAPKError = ref('');
        const adbAPKResult = ref(null);
        const adbDiagnosticBusy = ref('');
        const adbDiagnosticError = ref('');
        const adbDiagnosticMessage = ref('');
        let adbStatusInterval = null;
        let adbRequestGeneration = 0;
        let adbDiscoveryGeneration = 0;
        let adbAPKGeneration = 0;
        let adbDiagnosticGeneration = 0;
        let adbAPKRequest = null;

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
        const adbDiscoveryVisiblePackages = computed(() => {
            if (adbDiscoveryMode.value === 'all') return adbDiscoveryPackages.value;
            return adbDiscoveryPackages.value.filter(item => item.tv_launchable);
        });
        const adbDiscoverySelected = computed(() =>
            adbDiscoveryPackages.value.filter(item =>
                item.tv_launchable && !item.existing_launcher_id && item.selected
            )
        );
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

        const adbTokenStorageKey = `droidtvRemote:adbToken:${getCookiePath()}`;
        const readADBToken = () => {
            try {
                return window.sessionStorage ? (window.sessionStorage.getItem(adbTokenStorageKey) || '') : '';
            } catch (error) {
                return '';
            }
        };
        const writeADBToken = (token) => {
            try {
                if (!window.sessionStorage) return;
                if (token) {
                    window.sessionStorage.setItem(adbTokenStorageKey, token);
                } else {
                    window.sessionStorage.removeItem(adbTokenStorageKey);
                }
            } catch (error) {
                // Session storage is optional; never log credentials or storage contents.
            }
        };
        adbTokenConfigured.value = Boolean(readADBToken());

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
                selectedTvId.value = storedExists ? storedTvId : (tvs.value[0] ? tvs.value[0].id : '');
            }
            rememberSelectedTv(selectedTvId.value);
            tvName.value = selectedTv.value ? selectedTv.value.name : 'No TV selected';
            const configuredTvExists = tvs.value.some(tv => tv.id === configuredTvId.value);
            if (!configuredTvExists) {
                configuredTvId.value = selectedTvId.value || (tvs.value[0] ? tvs.value[0].id : '');
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
                tvName.value = data.tv_name || (selectedTv.value ? selectedTv.value.name : 'Android TV');
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
            if (changed && adbAPKUploading.value) {
                adbAPKError.value = 'Cancel the APK installation before switching TVs.';
                return;
            }
            if (changed && adbPackageMutating.value) {
                adbPackageError.value = 'Wait for the package administration action to finish before switching TVs.';
                return;
            }
            if (changed && adbDiagnosticBusy.value) {
                adbDiagnosticError.value = 'Wait for the diagnostic or reboot action to finish before switching TVs.';
                return;
            }
            if (changed) {
                adbRequestGeneration++;
                adbDiscoveryGeneration++;
                adbStatus.value = null;
                adbError.value = '';
                adbMessage.value = '';
                adbPairCode.value = '';
                adbDiscoveryPackages.value = [];
                adbDiscoveryWarnings.value = [];
                adbDiscoveryError.value = '';
                adbDiscoveryCurrentUser.value = null;
                adbPackageError.value = '';
                adbPackageMessage.value = '';
                adbDiagnosticError.value = '';
                adbDiagnosticMessage.value = '';
                adbDiscoveryPreview.value = false;
            }
            selectedTvId.value = tvId;
            rememberSelectedTv(tvId);
            showTvMenu.value = false;
            showPairingModal.value = false;
            connectionStatus.value = false;
            pairingInProgress.value = false;
            tvName.value = selectedTv.value ? selectedTv.value.name : 'Android TV';
            await checkStatus();
            if (changed || !connectionStatus.value) await connectToTV();
            if (currentView.value === 'adb' && adbTokenConfigured.value) {
                await checkADBStatus();
            }
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

        const orderedTvApps = computed(() => {
            const enabledMap = new Map(allApps.value.map(app => [app.id, app]));
            const result = [];
            for (const id of configuredAppIds.value) {
                if (enabledMap.has(id)) {
                    result.push({ app: enabledMap.get(id), enabled: true });
                    enabledMap.delete(id);
                }
            }
            for (const app of allApps.value) {
                if (enabledMap.has(app.id)) {
                    result.push({ app, enabled: false });
                }
            }
            return result;
        });

        const loadApps = async () => {
            const response = await fetch('api/apps');
            const data = await response.json();
            if (!response.ok) throw new Error(data.error || 'Failed to load app launchers');
            allApps.value = data.apps || [];
        };

        const validADBHost = (host) => {
            host = (host || '').trim();
            return Boolean(host) && host.length <= 255 && !/[\s/]/.test(host);
        };

        const validADBPort = (port) => {
            const text = String(port || '').trim();
            if (!/^\d{1,5}$/.test(text)) return false;
            const value = Number(text);
            return value >= 1 && value <= 65535;
        };

        const adbEndpoint = (host, port) => {
            host = (host || '').trim();
            port = String(port || '').trim();
            if (!validADBHost(host) || !validADBPort(port)) {
                throw new Error('Enter a valid ADB host and port');
            }
            if (host.indexOf(':') !== -1 && host.charAt(0) !== '[') {
                return '[' + host + ']:' + port;
            }
            return host + ':' + port;
        };

        const adbErrorMessage = (data, status) => {
            const code = data && data.code ? data.code : '';
            if (status === 401 || code === 'unauthorized') return 'Administrator token was rejected. Enter it again.';
            if (code === 'disabled') return 'ADB is disabled on this server.';
            if (code === 'unavailable' || code === 'missing_admin_token') return 'ADB is unavailable on this server.';
            if (code === 'timeout') return 'The ADB operation timed out. Check the TV and network, then retry.';
            if (code === 'unauthorized_device') return 'Accept the debugging authorization prompt shown on the TV.';
            if (code === 'offline') return 'The TV is offline or unreachable over ADB.';
            if (code === 'invalid_endpoint') return 'Enter the explicit ADB host and port shown by the TV.';
            if (code === 'invalid_pairing_code') return 'Enter the six-digit wireless debugging pairing code.';
            if (code === 'upload_too_large') return 'The APK exceeds the server upload limit. Check the configured ADB APK size limit.';
            if (code === 'invalid_apk' || code === 'malformed_apk') return 'Android rejected this file as an invalid or malformed APK.';
            if (code === 'insufficient_storage') return 'The TV does not have enough storage for this APK.';
            if (code === 'incompatible_abi') return 'This APK does not support the TV CPU architecture.';
            if (code === 'incompatible_sdk') return 'This APK is not compatible with the TV Android SDK version.';
            if (code === 'signature_mismatch') return 'The installed app and this APK use incompatible signing identities.';
            if (code === 'version_downgrade') return 'Android blocked this APK because version downgrades are not allowed.';
            if (code === 'package_manager_failure') return 'Android Package Manager rejected this APK.';
            if (code === 'protected_package') return 'This package is protected from ADB administration.';
            if (code === 'package_not_found') return 'This package is no longer installed for the current Android user. Refresh discovery.';
            if (code === 'stale_package_state') return 'Package state changed since discovery. Refresh the package list and try again.';
            if (code === 'package_state_unavailable') return 'Package state could not be verified safely. Refresh and try again.';
            if (code === 'package_mutation_failed') return 'Android did not confirm the requested package state change.';
            if (code === 'partial_failure') return data && data.error ? data.error : 'The package action may have completed, but the resulting state could not be fully reconciled.';
            if (code === 'capture_too_large') return 'The TV diagnostic capture exceeded its configured safety limit.';
            if (code === 'malformed_capture') return 'The TV did not return a valid complete PNG screenshot.';
            if (code === 'stale_reboot_confirmation') return 'The selected TV or ADB connection state changed. Refresh status before rebooting.';
            if (code === 'invalid_reboot_confirmation') return 'Reboot confirmation is incomplete.';
            if (code === 'canceled') return 'ADB operation was canceled.';
            return data && data.error ? data.error : 'ADB request failed';
        };

        const adbFetch = async (tvId, action, options) => {
            const token = readADBToken();
            if (!token) {
                adbTokenConfigured.value = false;
                throw new Error('Enter the ADB administrator token for this browser session.');
            }
            const headers = { 'Authorization': 'Bearer ' + token };
            if (options && options.body) headers['Content-Type'] = 'application/json';
            const requestOptions = {
                method: options && options.method ? options.method : 'GET',
                headers: headers
            };
            if (options && options.body) requestOptions.body = JSON.stringify(options.body);
            const response = await fetch(
                'api/tvs/' + encodeURIComponent(tvId) + '/adb/' + action,
                requestOptions
            );
            const data = await response.json();
            if (!response.ok) {
                if (response.status === 401 || data.code === 'unauthorized') {
                    writeADBToken('');
                    adbTokenConfigured.value = false;
                }
                const error = new Error(adbErrorMessage(data, response.status));
                error.code = data.code || '';
                throw error;
            }
            return data;
        };

        const checkADBStatus = async () => {
            const tvId = selectedTvId.value;
            if (!tvId || !adbTokenConfigured.value) {
                adbStatus.value = null;
                return;
            }
            const generation = ++adbRequestGeneration;
            try {
                const data = await adbFetch(tvId, 'status');
                if (generation !== adbRequestGeneration || tvId !== selectedTvId.value) return;
                adbStatus.value = data;
                adbError.value = '';
            } catch (error) {
                if (generation !== adbRequestGeneration || tvId !== selectedTvId.value) return;
                adbStatus.value = null;
                adbError.value = error.message || 'Failed to load ADB status';
            }
        };

        const seedADBHosts = () => {
            const host = selectedTv.value ? selectedTv.value.host : '';
            if (!adbLegacyHost.value) adbLegacyHost.value = host;
            if (!adbPairHost.value) adbPairHost.value = host;
            if (!adbConnectHost.value) adbConnectHost.value = host;
        };

        const setADBToken = async () => {
            const token = adbTokenInput.value.trim();
            if (!token) {
                adbError.value = 'Enter the ADB administrator token.';
                return;
            }
            writeADBToken(token);
            adbTokenInput.value = '';
            adbTokenConfigured.value = true;
            adbError.value = '';
            adbMessage.value = '';
            await checkADBStatus();
        };

        const clearADBToken = () => {
            writeADBToken('');
            adbTokenInput.value = '';
            adbTokenConfigured.value = false;
            adbStatus.value = null;
            adbError.value = '';
            adbMessage.value = '';
            adbPairCode.value = '';
            adbRequestGeneration++;
        };

        const openADBView = async () => {
            currentView.value = 'adb';
            clearADBDiscovery();
            showTvMenu.value = false;
            seedADBHosts();
            adbTokenInput.value = '';
            adbPairCode.value = '';
            adbError.value = '';
            adbMessage.value = '';
            adbTokenConfigured.value = Boolean(readADBToken());
            if (adbStatusInterval) clearInterval(adbStatusInterval);
            adbStatusInterval = setInterval(() => {
                if (currentView.value === 'adb' && adbTokenConfigured.value) checkADBStatus();
            }, 2000);
            if (adbTokenConfigured.value) await checkADBStatus();
        };

        const closeADBView = () => {
            if (adbAPKUploading.value && adbAPKRequest && typeof adbAPKRequest.abort === 'function') {
                adbAPKGeneration++;
                adbAPKRequest.abort();
            }
            adbAPKRequest = null;
            adbAPKUploading.value = false;
            adbAPKProgress.value = null;
            adbAPKFile.value = null;
            adbDiagnosticGeneration++;
            adbDiagnosticBusy.value = '';
            adbDiagnosticError.value = '';
            adbDiagnosticMessage.value = '';
            clearADBDiscovery();
            adbTokenInput.value = '';
            adbPairCode.value = '';
            adbError.value = '';
            adbMessage.value = '';
            adbRequestGeneration++;
            if (adbStatusInterval) {
                clearInterval(adbStatusInterval);
                adbStatusInterval = null;
            }
            currentView.value = 'remote';
            checkStatus();
        };

        const connectADBEndpoint = async (endpoint) => {
            const tvId = selectedTvId.value;
            if (!tvId) {
                adbError.value = 'Select a TV first.';
                return;
            }
            adbLoading.value = true;
            adbError.value = '';
            adbMessage.value = 'Connecting with ADB…';
            try {
                await adbFetch(tvId, 'connect', {
                    method: 'POST',
                    body: { endpoint: endpoint }
                });
                adbMessage.value = 'ADB connection request completed.';
                await checkADBStatus();
            } catch (error) {
                adbError.value = error.message || 'ADB connection failed';
                adbMessage.value = '';
            } finally {
                adbLoading.value = false;
            }
        };

        const connectLegacyADB = async () => {
            try {
                await connectADBEndpoint(adbEndpoint(adbLegacyHost.value, adbLegacyPort.value));
            } catch (error) {
                adbError.value = error.message;
            }
        };

        const pairSecureADB = async () => {
            const tvId = selectedTvId.value;
            const code = adbPairCode.value.trim();
            if (!/^\d{6}$/.test(code)) {
                adbError.value = 'Enter the six-digit wireless debugging pairing code.';
                return;
            }
            let pairEndpoint;
            try {
                pairEndpoint = adbEndpoint(adbPairHost.value, adbPairPort.value);
            } catch (error) {
                adbError.value = error.message;
                return;
            }
            adbPairCode.value = '';
            adbLoading.value = true;
            adbError.value = '';
            adbMessage.value = 'Pairing with the TV…';
            try {
                await adbFetch(tvId, 'pair', {
                    method: 'POST',
                    body: { endpoint: pairEndpoint, code: code }
                });
                adbMessage.value = 'Pairing accepted. Connecting to the TV…';
                const connectEndpoint = adbEndpoint(adbConnectHost.value, adbConnectPort.value);
                await adbFetch(tvId, 'connect', {
                    method: 'POST',
                    body: { endpoint: connectEndpoint }
                });
                adbMessage.value = 'Secure wireless ADB is connected.';
                await checkADBStatus();
            } catch (error) {
                adbError.value = error.message || 'Secure ADB setup failed';
                adbMessage.value = '';
            } finally {
                adbPairCode.value = '';
                adbLoading.value = false;
            }
        };

        const retryADB = async () => {
            const stored = adbStatus.value && adbStatus.value.adb ? adbStatus.value.adb.endpoint : '';
            if (stored) {
                await connectADBEndpoint(stored);
                return;
            }
            if (adbSetupMode.value === 'secure') {
                try {
                    await connectADBEndpoint(adbEndpoint(adbConnectHost.value, adbConnectPort.value));
                } catch (error) {
                    adbError.value = error.message;
                }
            } else {
                await connectLegacyADB();
            }
        };

        const disconnectADB = async () => {
            const tvId = selectedTvId.value;
            if (!tvId) return;
            adbLoading.value = true;
            adbError.value = '';
            adbMessage.value = 'Disconnecting ADB…';
            try {
                await adbFetch(tvId, 'disconnect', { method: 'POST' });
                adbMessage.value = 'ADB disconnected.';
                await checkADBStatus();
            } catch (error) {
                adbError.value = error.message || 'Failed to disconnect ADB';
                adbMessage.value = '';
            } finally {
                adbLoading.value = false;
            }
        };

        const forgetADB = async () => {
            const tv = selectedTv.value;
            if (!tv) return;
            if (!window.confirm('Forget the local ADB association for ' + tv.name + '? This does not revoke the debugging host on the TV.')) return;
            adbLoading.value = true;
            adbError.value = '';
            adbMessage.value = '';
            try {
                const data = await adbFetch(tv.id, 'forget', { method: 'POST' });
                adbStatus.value = {
                    tv_id: tv.id,
                    tv_name: tv.name,
                    remote: {
                        connected: connectionStatus.value,
                        connecting: connecting.value,
                        pairing_in_progress: pairingInProgress.value
                    },
                    adb: {
                        state: 'unpaired',
                        enabled: true,
                        available: true,
                        paired: false,
                        serial: null,
                        endpoint: null,
                        pair_guid: null
                    }
                };
                adbMessage.value = data.warning || 'Local ADB association forgotten.';
            } catch (error) {
                adbError.value = error.message || 'Failed to forget ADB association';
            } finally {
                adbLoading.value = false;
            }
        };

        const packageNameSuggestion = (packageId) => {
            const parts = String(packageId || '').split('.');
            let base = parts.length ? parts[parts.length - 1] : '';
            if (!base && parts.length > 1) base = parts[parts.length - 2];
            base = base.replace(/[_-]+/g, ' ').trim();
            if (!base) return String(packageId || '');
            return base.split(/\s+/).map(word =>
                word.charAt(0).toUpperCase() + word.slice(1)
            ).join(' ');
        };

        const clearADBDiscovery = () => {
            adbDiscoveryGeneration++;
            adbDiscoveryPackages.value = [];
            adbDiscoveryWarnings.value = [];
            adbDiscoveryError.value = '';
            adbDiscoveryCurrentUser.value = null;
            adbPackageError.value = '';
            adbPackageMessage.value = '';
            adbDiscoveryPreview.value = false;
            adbDiscoveryLoading.value = false;
            adbImporting.value = false;
        };

        const discoverADBApps = async () => {
            const tvId = selectedTvId.value;
            if (!tvId || !adbTokenConfigured.value) {
                adbDiscoveryError.value = 'Select a TV and enter the ADB administrator token first.';
                return;
            }
            const generation = ++adbDiscoveryGeneration;
            adbDiscoveryLoading.value = true;
            adbDiscoveryError.value = '';
            adbDiscoveryPreview.value = false;
            try {
                const results = await Promise.all([
                    adbFetch(tvId, 'packages'),
                    fetch('api/apps').then(async response => {
                        const data = await response.json();
                        if (!response.ok) throw new Error(data.error || 'Failed to load shared launchers');
                        return data;
                    })
                ]);
                if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) return;

                const packageResult = results[0];
                const appResult = results[1];
                const inventory = packageResult && packageResult.inventory ? packageResult.inventory : {};
                const discovered = Array.isArray(inventory.packages) ? inventory.packages : [];
                const shared = Array.isArray(appResult.apps) ? appResult.apps : [];
                allApps.value = shared;
                adbDiscoveryCurrentUser.value = typeof inventory.current_user === 'number' ? inventory.current_user : null;

                adbDiscoveryPackages.value = discovered.map(pkg => {
                    const existing = shared.find(app => app.package_id === pkg.package_id);
                    return {
                        package_id: pkg.package_id || '',
                        component: pkg.component || '',
                        version_code: pkg.version_code || '',
                        classification: pkg.classification || 'unknown',
                        enabled: typeof pkg.enabled === 'boolean' ? pkg.enabled : null,
                        protected: Boolean(pkg.protected),
                        tv_launchable: Boolean(pkg.tv_launchable),
                        existing_launcher_id: existing ? existing.id : '',
                        existing_launcher_name: existing ? existing.name : '',
                        selected: false,
                        import_name: existing ? existing.name : packageNameSuggestion(pkg.package_id)
                    };
                });
                adbDiscoveryWarnings.value = Array.isArray(inventory.warnings) ? inventory.warnings : [];
            } catch (error) {
                if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) return;
                adbDiscoveryPackages.value = [];
                adbDiscoveryWarnings.value = [];
                adbDiscoveryCurrentUser.value = null;
                adbDiscoveryError.value = error.message || 'Failed to discover apps';
            } finally {
                if (generation === adbDiscoveryGeneration && tvId === selectedTvId.value) {
                    adbDiscoveryLoading.value = false;
                }
            }
        };

        const mutateADBPackage = async (pkg, action) => {
            if (adbPackageMutating.value) return;
            if (adbAPKUploading.value || adbDiagnosticBusy.value) {
                adbPackageError.value = 'Wait for the current ADB administration action to finish.';
                return;
            }
            const allowed = { clear: true, enable: true, disable: true, uninstall: true };
            if (!allowed[action]) return;
            const tv = selectedTv.value;
            const tvId = selectedTvId.value;
            const current = adbDiscoveryPackages.value.find(item => item.package_id === (pkg && pkg.package_id));
            adbPackageError.value = '';
            adbPackageMessage.value = '';
            if (!tv || !tvId || !current || adbDiscoveryCurrentUser.value === null) {
                adbPackageError.value = 'Refresh app discovery before administering a package.';
                return;
            }
            if (current.protected || current.classification !== 'third_party') {
                adbPackageError.value = 'This package is protected or is not classified as third-party.';
                return;
            }
            if (typeof current.enabled !== 'boolean') {
                adbPackageError.value = 'The current package enabled state is unavailable. Refresh discovery and try again.';
                return;
            }

            let prompt = '';
            if (action === 'clear') {
                prompt = 'Clear all app data for ' + current.package_id + ' on ' + tv.name +
                    '?\n\nThis deletes the app\'s local data and settings for the current Android user and may require signing in again.';
            } else if (action === 'uninstall') {
                prompt = 'Uninstall ' + current.package_id + ' from ' + tv.name +
                    ' for the current Android user?\n\nThe shared launcher record will be kept, but its availability on this TV will be removed.';
            } else if (action === 'disable') {
                prompt = 'Disable ' + current.package_id + ' on ' + tv.name + ' for the current Android user?';
            } else {
                prompt = 'Enable ' + current.package_id + ' on ' + tv.name + ' for the current Android user?';
            }
            if (!window.confirm(prompt)) return;

            const generation = adbDiscoveryGeneration;
            adbPackageMutating.value = current.package_id + ':' + action;
            try {
                const result = await adbFetch(tvId, 'packages/' + action, {
                    method: 'POST',
                    body: {
                        package_id: current.package_id,
                        confirmation: {
                            tv_id: tvId,
                            package_id: current.package_id,
                            action: action,
                            current_user: adbDiscoveryCurrentUser.value,
                            enabled: current.enabled
                        }
                    }
                });
                if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) return;
                if (!result || result.tv_id !== tvId || result.package_id !== current.package_id || result.action !== action) {
                    throw new Error('Package administration result did not match the selected TV and package.');
                }
                adbPackageError.value = '';
                adbPackageMessage.value = action.charAt(0).toUpperCase() + action.slice(1) +
                    ' completed for ' + current.package_id + '.';
                await refreshTvs();
                if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) return;
                await discoverADBApps();
                if (tvId === selectedTvId.value) await checkStatus();
            } catch (error) {
                if (generation === adbDiscoveryGeneration && tvId === selectedTvId.value) {
                    adbPackageError.value = error.message || 'Package administration failed';
                }
            } finally {
                if (tvId === selectedTvId.value) adbPackageMutating.value = '';
            }
        };

        const setADBDiscoveryMode = (mode) => {
            adbDiscoveryMode.value = mode === 'all' ? 'all' : 'launchable';
            adbDiscoveryPreview.value = false;
        };

        const toggleADBDiscoverySelection = (item) => {
            if (!item || !item.tv_launchable || item.existing_launcher_id) return;
            item.selected = !item.selected;
            adbDiscoveryPreview.value = false;
        };

        const reviewADBImport = () => {
            adbDiscoveryError.value = '';
            const selected = adbDiscoverySelected.value;
            if (!selected.length) {
                adbDiscoveryError.value = 'Select at least one unknown TV-launchable package to import.';
                adbDiscoveryPreview.value = false;
                return;
            }
            for (const item of selected) {
                item.import_name = String(item.import_name || '').trim();
                if (!item.import_name) {
                    adbDiscoveryError.value = 'Every selected package needs a display name before import.';
                    adbDiscoveryPreview.value = false;
                    return;
                }
            }
            const seen = {};
            for (const item of selected) {
                if (seen[item.package_id]) {
                    adbDiscoveryError.value = 'A package can only be imported once.';
                    adbDiscoveryPreview.value = false;
                    return;
                }
                seen[item.package_id] = true;
            }
            adbDiscoveryPreview.value = true;
        };

        const cancelADBImportReview = () => {
            adbDiscoveryPreview.value = false;
            adbDiscoveryError.value = '';
        };

        const importDiscoveredADBApps = async () => {
            if (!adbDiscoveryPreview.value || adbImporting.value) return;
            const tvId = selectedTvId.value;
            const generation = adbDiscoveryGeneration;
            const selected = adbDiscoverySelected.value.map(item => ({
                package_id: item.package_id,
                name: String(item.import_name || '').trim()
            }));
            if (!tvId || !selected.length || selected.some(item => !item.name)) {
                adbDiscoveryError.value = 'Review the selected packages and names before importing.';
                return;
            }

            adbImporting.value = true;
            adbDiscoveryError.value = '';
            try {
                const appsResponse = await fetch('api/apps');
                const appsData = await appsResponse.json();
                if (!appsResponse.ok) throw new Error(appsData.error || 'Failed to refresh shared launchers');
                if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) {
                    throw new Error('Selected TV changed before import. Run discovery again.');
                }

                let shared = Array.isArray(appsData.apps) ? appsData.apps.slice() : [];
                const createdIds = [];
                const availabilityIds = [];
                for (const item of selected) {
                    if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) {
                        throw new Error('Selected TV changed during import. Run discovery again.');
                    }
                    const existing = shared.find(app => app.package_id === item.package_id);
                    if (existing) {
                        if (availabilityIds.indexOf(existing.id) === -1) availabilityIds.push(existing.id);
                        continue;
                    }
                    const formData = new FormData();
                    formData.append('name', item.name);
                    formData.append('package_id', item.package_id);
                    formData.append('icon_class', '');
                    const response = await fetch('api/apps', { method: 'POST', body: formData });
                    const data = await response.json();
                    if (!response.ok) throw new Error(data.error || 'Failed to import ' + item.package_id);
                    if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) {
                        throw new Error('Selected TV changed during import. New shared launcher was saved, but TV availability was not changed.');
                    }
                    if (data.app) {
                        shared.push(data.app);
                        createdIds.push(data.app.id);
                        availabilityIds.push(data.app.id);
                    }
                }

                const tv = tvs.value.find(item => item.id === tvId);
                const existingIds = tv && Array.isArray(tv.app_ids) ? tv.app_ids.slice() : [];
                const nextIds = existingIds.slice();
                for (const id of availabilityIds) {
                    if (nextIds.indexOf(id) === -1) nextIds.push(id);
                }

                if (availabilityIds.some(id => existingIds.indexOf(id) === -1)) {
                    const response = await fetch('api/tvs/' + encodeURIComponent(tvId) + '/apps', {
                        method: 'PUT',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ app_ids: nextIds })
                    });
                    const data = await response.json();
                    if (!response.ok) throw new Error(data.error || 'Launchers were imported but TV availability could not be saved');
                    if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) return;
                    const tvIndex = tvs.value.findIndex(item => item.id === tvId);
                    if (tvIndex !== -1) tvs.value[tvIndex] = data.tv;
                }

                if (generation !== adbDiscoveryGeneration || tvId !== selectedTvId.value) return;
                allApps.value = shared;
                adbDiscoveryPreview.value = false;
                adbMessage.value = createdIds.length
                    ? 'Imported ' + createdIds.length + ' launcher' + (createdIds.length === 1 ? '' : 's') + ' for this TV.'
                    : 'No duplicate launcher was created; matching package IDs were reused for this TV.';
                adbImporting.value = false;
                await discoverADBApps();
                if (selectedTvId.value === tvId) await checkStatus();
            } catch (error) {
                if (generation === adbDiscoveryGeneration && tvId === selectedTvId.value) {
                    adbDiscoveryError.value = error.message || 'App import failed';
                }
            } finally {
                if (generation === adbDiscoveryGeneration && tvId === selectedTvId.value) {
                    adbImporting.value = false;
                }
            }
        };

        const diagnosticFilename = (response, fallback) => {
            const disposition = response && response.headers && response.headers.get
                ? response.headers.get('Content-Disposition') || ''
                : '';
            const match = disposition.match(/filename="([^"]+)"/i);
            return match && match[1] ? match[1] : fallback;
        };

        const downloadADBDiagnostic = async (kind) => {
            if (adbDiagnosticBusy.value || (kind !== 'screenshot' && kind !== 'logs')) return;
            if (adbAPKUploading.value || adbPackageMutating.value) {
                adbDiagnosticError.value = 'Wait for the current ADB administration action to finish.';
                return;
            }
            const tv = selectedTv.value;
            const tvId = selectedTvId.value;
            const token = readADBToken();
            adbDiagnosticError.value = '';
            adbDiagnosticMessage.value = '';
            if (!tv || !tvId || !token) {
                adbDiagnosticError.value = 'Select a TV and enter the ADB administrator token first.';
                return;
            }
            const generation = ++adbDiagnosticGeneration;
            adbDiagnosticBusy.value = kind;
            try {
                const response = await fetch('api/tvs/' + encodeURIComponent(tvId) + '/adb/' + kind, {
                    headers: { 'Authorization': 'Bearer ' + token }
                });
                if (!response.ok) {
                    let data = {};
                    try { data = await response.json(); } catch (error) {}
                    throw new Error(adbErrorMessage(data, response.status));
                }
                const blob = await response.blob();
                if (generation !== adbDiagnosticGeneration || tvId !== selectedTvId.value) return;
                const factory = window.URL || window.webkitURL;
                if (!factory || typeof factory.createObjectURL !== 'function') {
                    throw new Error('This browser cannot create a diagnostic download.');
                }
                const fallback = 'droidtv-remote-' + tvId + '-' + (kind === 'screenshot' ? 'screenshot.png' : 'logs.txt');
                const filename = diagnosticFilename(response, fallback);
                const url = factory.createObjectURL(blob);
                const link = document.createElement('a');
                link.href = url;
                link.download = filename;
                link.style.display = 'none';
                document.body.appendChild(link);
                link.click();
                document.body.removeChild(link);
                factory.revokeObjectURL(url);
                adbDiagnosticError.value = '';
                adbDiagnosticMessage.value = kind === 'screenshot'
                    ? 'Screenshot downloaded from ' + tv.name + '.'
                    : 'Finite device log snapshot downloaded. Treat it as sensitive.';
            } catch (error) {
                if (generation === adbDiagnosticGeneration && tvId === selectedTvId.value) {
                    adbDiagnosticError.value = error.message || 'Diagnostic download failed';
                }
            } finally {
                if (generation === adbDiagnosticGeneration) adbDiagnosticBusy.value = '';
            }
        };

        const rebootADBTV = async () => {
            if (adbDiagnosticBusy.value) return;
            if (adbAPKUploading.value || adbPackageMutating.value) {
                adbDiagnosticError.value = 'Wait for the current ADB administration action to finish.';
                return;
            }
            const tv = selectedTv.value;
            const tvId = selectedTvId.value;
            const state = adbStatus.value && adbStatus.value.adb ? adbStatus.value.adb.state : '';
            adbDiagnosticError.value = '';
            adbDiagnosticMessage.value = '';
            if (!tv || !tvId || state !== 'connected') {
                adbDiagnosticError.value = 'Refresh ADB status and connect the selected TV before rebooting.';
                return;
            }
            if (!window.confirm(
                'Reboot ' + tv.name + '?\n\nThe command only requests a normal reboot. The TV will disconnect while restarting, and this screen cannot confirm when boot has completed.'
            )) return;

            const generation = ++adbDiagnosticGeneration;
            adbDiagnosticBusy.value = 'reboot';
            try {
                const result = await adbFetch(tvId, 'reboot', {
                    method: 'POST',
                    body: {
                        confirmation: {
                            tv_id: tvId,
                            tv_name: tv.name,
                            state: 'connected'
                        }
                    }
                });
                if (generation !== adbDiagnosticGeneration || tvId !== selectedTvId.value) return;
                if (!result || result.tv_id !== tvId || result.command_sent !== true || result.status !== 'accepted') {
                    throw new Error('Reboot result did not match the selected TV.');
                }
                if (adbStatus.value && adbStatus.value.adb) adbStatus.value.adb.state = 'offline';
                adbDiagnosticMessage.value = result.message || 'Reboot command sent. The TV is expected to disconnect while restarting.';
            } catch (error) {
                if (generation === adbDiagnosticGeneration && tvId === selectedTvId.value) {
                    adbDiagnosticError.value = error.message || 'Failed to send reboot command';
                }
            } finally {
                if (generation === adbDiagnosticGeneration) adbDiagnosticBusy.value = '';
            }
        };

        const clearADBAPKSelection = () => {
            if (adbAPKUploading.value) return;
            adbAPKFile.value = null;
            adbAPKProgress.value = null;
            adbAPKError.value = '';
            adbAPKResult.value = null;
        };

        const handleADBAPKFile = (event) => {
            if (adbAPKUploading.value) return;
            const files = event && event.target && event.target.files ? event.target.files : [];
            const file = files && files.length ? files[0] : null;
            adbAPKError.value = '';
            adbAPKResult.value = null;
            adbAPKProgress.value = null;
            if (!file) {
                adbAPKFile.value = null;
                return;
            }
            const name = String(file.name || '');
            if (!/\.apk$/i.test(name)) {
                adbAPKFile.value = null;
                adbAPKError.value = 'Choose a single file with the .apk extension.';
                return;
            }
            if (!file.size) {
                adbAPKFile.value = null;
                adbAPKError.value = 'The selected APK is empty.';
                return;
            }
            adbAPKFile.value = file;
        };

        const parseADBUploadResponse = (request) => {
            if (!request || !request.responseText) return {};
            try {
                return JSON.parse(request.responseText);
            } catch (error) {
                return {};
            }
        };

        const finishADBAPKUpload = async (generation, tvId, request, transportError) => {
            if (generation !== adbAPKGeneration || tvId !== selectedTvId.value) return;
            adbAPKRequest = null;
            adbAPKUploading.value = false;
            if (transportError) {
                adbAPKProgress.value = null;
                adbAPKError.value = transportError;
                return;
            }
            const data = parseADBUploadResponse(request);
            if (request.status < 200 || request.status >= 300) {
                if (request.status === 401 || data.code === 'unauthorized') {
                    writeADBToken('');
                    adbTokenConfigured.value = false;
                }
                adbAPKProgress.value = null;
                adbAPKError.value = adbErrorMessage(data, request.status);
                return;
            }
            if (!data || data.tv_id !== tvId) {
                adbAPKProgress.value = null;
                adbAPKError.value = 'The install result did not match the selected TV.';
                return;
            }
            adbAPKProgress.value = 100;
            adbAPKResult.value = data;
            adbAPKError.value = '';
            adbMessage.value = data.operation === 'update' ? 'APK update completed.' : 'APK installation completed.';
            await discoverADBApps();
            if (generation === adbAPKGeneration && tvId === selectedTvId.value) {
                await checkADBStatus();
            }
        };

        const installADBAPK = async () => {
            const file = adbAPKFile.value;
            const tv = selectedTv.value;
            if (adbAPKUploading.value) return;
            if (adbPackageMutating.value || adbDiagnosticBusy.value) {
                adbAPKError.value = 'Wait for the current ADB administration action to finish.';
                return;
            }
            adbAPKError.value = '';
            adbAPKResult.value = null;
            if (!tv || !selectedTvId.value) {
                adbAPKError.value = 'Select a TV before installing an APK.';
                return;
            }
            if (!adbTokenConfigured.value || !readADBToken()) {
                adbAPKError.value = 'Enter the ADB administrator token first.';
                return;
            }
            if (!file) {
                adbAPKError.value = 'Choose one APK file first.';
                return;
            }
            if (!/\.apk$/i.test(String(file.name || '')) || !file.size) {
                adbAPKError.value = 'Choose a non-empty .apk file.';
                return;
            }
            const confirmed = window.confirm(
                'Install “' + file.name + '” on ' + tv.name + '?\n\n' +
                'If the same package and signing identity are already installed, Android may update it while preserving app data. ' +
                'Downgrades and signing mismatches are not bypassed.'
            );
            if (!confirmed) return;

            const token = readADBToken();
            const tvId = selectedTvId.value;
            const generation = ++adbAPKGeneration;
            const formData = new FormData();
            formData.append('apk', file, file.name);
            adbAPKUploading.value = true;
            adbAPKProgress.value = null;
            adbAPKError.value = '';
            adbMessage.value = 'Uploading APK to ' + tv.name + '…';

            if (typeof XMLHttpRequest !== 'undefined') {
                await new Promise(resolve => {
                    const request = new XMLHttpRequest();
                    adbAPKRequest = request;
                    request.open('POST', 'api/tvs/' + encodeURIComponent(tvId) + '/adb/install-apk', true);
                    request.setRequestHeader('Authorization', 'Bearer ' + token);
                    if (request.upload) {
                        request.upload.onprogress = event => {
                            if (generation !== adbAPKGeneration || tvId !== selectedTvId.value) return;
                            if (event && event.lengthComputable && event.total > 0) {
                                adbAPKProgress.value = Math.min(99, Math.round((event.loaded / event.total) * 100));
                            } else {
                                adbAPKProgress.value = null;
                            }
                        };
                    }
                    request.onload = async () => {
                        await finishADBAPKUpload(generation, tvId, request, '');
                        resolve();
                    };
                    request.onerror = async () => {
                        await finishADBAPKUpload(generation, tvId, request, 'Network connection was lost during APK upload.');
                        resolve();
                    };
                    request.onabort = async () => {
                        if (generation === adbAPKGeneration && tvId === selectedTvId.value) {
                            adbAPKRequest = null;
                            adbAPKUploading.value = false;
                            adbAPKProgress.value = null;
                            adbAPKError.value = 'APK installation canceled.';
                            adbMessage.value = '';
                        }
                        resolve();
                    };
                    request.send(formData);
                });
                return;
            }

            try {
                const response = await fetch('api/tvs/' + encodeURIComponent(tvId) + '/adb/install-apk', {
                    method: 'POST',
                    headers: { 'Authorization': 'Bearer ' + token },
                    body: formData
                });
                const data = await response.json();
                if (generation !== adbAPKGeneration || tvId !== selectedTvId.value) return;
                const request = {
                    status: response.status,
                    responseText: JSON.stringify(data)
                };
                await finishADBAPKUpload(generation, tvId, request, '');
            } catch (error) {
                if (generation === adbAPKGeneration && tvId === selectedTvId.value) {
                    adbAPKRequest = null;
                    adbAPKUploading.value = false;
                    adbAPKProgress.value = null;
                    adbAPKError.value = 'Network connection was lost during APK upload.';
                }
            }
        };

        const cancelADBAPKUpload = () => {
            if (!adbAPKUploading.value) return;
            adbAPKGeneration++;
            if (adbAPKRequest && typeof adbAPKRequest.abort === 'function') {
                adbAPKRequest.abort();
            }
            adbAPKRequest = null;
            adbAPKUploading.value = false;
            adbAPKProgress.value = null;
            adbAPKError.value = 'APK installation canceled.';
            adbMessage.value = '';
        };

        const selectADBSetupMode = (mode) => {
            adbSetupMode.value = mode === 'secure' ? 'secure' : 'legacy';
            adbPairCode.value = '';
            adbError.value = '';
            adbMessage.value = '';
            seedADBHosts();
        };

        const openLauncherView = async () => {
            currentView.value = 'apps';
            showTvMenu.value = false;
            configuredTvId.value = selectedTvId.value || (tvs.value[0] ? tvs.value[0].id : '');
            try {
                await Promise.all([loadApps(), refreshTvs()]);
                syncConfiguredApps();
            } catch (error) {
                showError(error.message || 'Failed to load app launchers');
            }
        };

        const openRemoteView = () => {
            if (adbStatusInterval) {
                clearInterval(adbStatusInterval);
                adbStatusInterval = null;
            }
            adbTokenInput.value = '';
            adbPairCode.value = '';
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
            appIconFile.value = event.target.files && event.target.files[0] || null;
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

        const moveTvAppUp = (appId) => {
            const index = configuredAppIds.value.indexOf(appId);
            if (index <= 0) return;
            const items = [...configuredAppIds.value];
            const temp = items[index];
            items[index] = items[index - 1];
            items[index - 1] = temp;
            configuredAppIds.value = items;
        };

        const moveTvAppDown = (appId) => {
            const index = configuredAppIds.value.indexOf(appId);
            if (index < 0 || index >= configuredAppIds.value.length - 1) return;
            const items = [...configuredAppIds.value];
            const temp = items[index];
            items[index] = items[index + 1];
            items[index + 1] = temp;
            configuredAppIds.value = items;
        };

        const saveAppOrder = async () => {
            try {
                const response = await fetch('api/apps/reorder', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ app_ids: allApps.value.map(a => a.id) })
                });
                const data = await response.json();
                if (!response.ok) throw new Error(data.error || 'Failed to reorder app launchers');
                allApps.value = data.apps || [];
                if (selectedTvId.value) await checkStatus();
            } catch (error) {
                showError(error.message || 'Failed to reorder app launchers');
            }
        };

        const moveAppUp = async (index) => {
            if (index <= 0 || index >= allApps.value.length) return;
            const items = [...allApps.value];
            const temp = items[index];
            items[index] = items[index - 1];
            items[index - 1] = temp;
            allApps.value = items;
            await saveAppOrder();
        };

        const moveAppDown = async (index) => {
            if (index < 0 || index >= allApps.value.length - 1) return;
            const items = [...allApps.value];
            const temp = items[index];
            items[index] = items[index + 1];
            items[index + 1] = temp;
            allApps.value = items;
            await saveAppOrder();
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
                                const eventStart = event.data.start == null ? textLength : event.data.start;
                                const start = Math.max(0, Math.min(eventStart, textLength));
                                const eventEnd = event.data.end == null ? start : event.data.end;
                                const end = Math.max(start, Math.min(eventEnd, textLength));
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
                navigator.serviceWorker.register('sw.js?v=__VERSION__').then((registration) => {
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
            if (adbStatusInterval) {
                clearInterval(adbStatusInterval);
            }
            adbTokenInput.value = '';
            adbPairCode.value = '';
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
            orderedTvApps,
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
            adbTokenInput,
            adbTokenConfigured,
            adbStatus,
            adbLoading,
            adbError,
            adbMessage,
            adbSetupMode,
            adbLegacyHost,
            adbLegacyPort,
            adbPairHost,
            adbPairPort,
            adbPairCode,
            adbConnectHost,
            adbConnectPort,
            adbDiscoveryLoading,
            adbDiscoveryMode,
            adbDiscoveryPackages,
            adbDiscoveryWarnings,
            adbDiscoveryError,
            adbDiscoveryCurrentUser,
            adbDiscoveryPreview,
            adbImporting,
            adbPackageMutating,
            adbPackageError,
            adbPackageMessage,
            adbAPKFile,
            adbAPKUploading,
            adbAPKProgress,
            adbAPKError,
            adbAPKResult,
            adbDiagnosticBusy,
            adbDiagnosticError,
            adbDiagnosticMessage,
            adbDiscoveryVisiblePackages,
            adbDiscoverySelected,

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
            openADBView,
            closeADBView,
            setADBToken,
            clearADBToken,
            checkADBStatus,
            selectADBSetupMode,
            connectLegacyADB,
            pairSecureADB,
            retryADB,
            disconnectADB,
            forgetADB,
            clearADBDiscovery,
            discoverADBApps,
            setADBDiscoveryMode,
            mutateADBPackage,
            toggleADBDiscoverySelection,
            reviewADBImport,
            cancelADBImportReview,
            importDiscoveredADBApps,
            handleADBAPKFile,
            clearADBAPKSelection,
            installADBAPK,
            cancelADBAPKUpload,
            downloadADBDiagnostic,
            rebootADBTV,
            selectConfiguredTv,
            saveTvAppConfiguration,
            openAddApp,
            openEditApp,
            handleAppIconChange,
            saveApp,
            deleteApp,
            moveTvAppUp,
            moveTvAppDown,
            moveAppUp,
            moveAppDown,
            saveAppOrder,
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
