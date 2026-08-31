const assert = require('node:assert/strict');
const fs = require('node:fs');

const appSource = fs.readFileSync(require.resolve('../client/app.js'), 'utf8');

// The installed PWA must keep parsing on older mobile WebViews that do not
// support optional chaining or nullish coalescing. A parse failure leaves the
// application shell completely empty.
assert.doesNotMatch(appSource, /\?\.|\?\?/);

const mountedCallbacks = [];
const intervalCallbacks = [];
const connectRequests = [];
const tvAppRequests = [];
const appSaveRequests = [];
const appReorderRequests = [];
const adbRequests = [];
const adbUploadRequests = [];
const packageAdminRequests = [];
const diagnosticRequests = [];
const diagnosticDownloads = [];
const confirmations = [];
const disabledAdminPackages = new Set(['tv.stream.beta']);
const uninstalledAdminPackages = new Set();
let confirmResult = true;
let holdPackageMutation = false;
let resolvePackageMutation = null;
let holdDiagnostic = false;
let resolveDiagnostic = null;
let failNextDiagnostic = false;
let nextADBUploadResponse = null;
let holdADBUpload = false;
let pendingADBUpload = null;
let failNextStatus = false;
let failNextDiscovery = false;
let deferredDiscoveryTv = '';
let resolveDeferredDiscovery = null;
let exposed;

const localValues = new Map();
localValues.set('droidtvRemote:selectedTv:/remote/', 'bedroom');
const storage = {
    getItem(key) { return localValues.get(key) || ''; },
    setItem(key, value) { localValues.set(key, value); },
    removeItem(key) { localValues.delete(key); }
};
const sessionValues = new Map();
const sessionStorage = {
    getItem(key) { return sessionValues.get(key) || ''; },
    setItem(key, value) { sessionValues.set(key, value); },
    removeItem(key) { sessionValues.delete(key); }
};

class FakeFormData {
    constructor() {
        this.values = new Map();
    }
    append(key, value) {
        this.values.set(key, value);
    }
    get(key) {
        return this.values.get(key);
    }
}

function completeADBUpload(request) {
    const tvId = decodeURIComponent(request.url.split('/')[2]);
    const configured = nextADBUploadResponse;
    nextADBUploadResponse = null;
    if (request.upload && request.upload.onprogress) {
        request.upload.onprogress({ lengthComputable: true, loaded: 50, total: 100 });
    }
    request.status = configured ? configured.status : 200;
    request.responseText = JSON.stringify(configured ? configured.body : {
        tv_id: tvId,
        status: 'success',
        operation: 'update',
        sha256: 'abc123',
        size_bytes: 4096,
        package: {
            package_id: 'tv.stream.alpha',
            version_code: '13'
        }
    });
    request.onload();
}

class FakeXMLHttpRequest {
    constructor() {
        this.upload = {};
        this.headers = {};
        this.status = 0;
        this.responseText = '';
    }
    open(method, url) {
        this.method = method;
        this.url = url;
    }
    setRequestHeader(name, value) {
        this.headers[name] = value;
    }
    send(body) {
        this.body = body;
        adbUploadRequests.push(this);
        if (holdADBUpload) {
            pendingADBUpload = this;
            return;
        }
        completeADBUpload(this);
    }
    abort() {
        if (this.onabort) this.onabort();
    }
}

global.FormData = FakeFormData;
global.XMLHttpRequest = FakeXMLHttpRequest;
const adbStates = {
    living: { state: 'unpaired', enabled: true, available: true, paired: false, serial: null, endpoint: null, pair_guid: null },
    bedroom: { state: 'unpaired', enabled: true, available: true, paired: false, serial: null, endpoint: null, pair_guid: null }
};

global.window = {
    location: { pathname: '/remote/', hostname: 'localhost' },
    URL: {
        createObjectURL: () => 'blob:diagnostic',
        revokeObjectURL() {}
    },
    localStorage: storage,
    sessionStorage,
    matchMedia: () => ({ matches: false }),
    addEventListener() {},
    removeEventListener() {},
    isSecureContext: true,
    navigator: null,
    confirm: message => {
        confirmations.push(message);
        return confirmResult;
    }
};
global.document = {
    cookie: '',
    hidden: false,
    body: {
        appendChild() {},
        removeChild() {}
    },
    createElement(tag) {
        if (tag !== 'a') return { style: {} };
        return {
            href: '',
            download: '',
            style: {},
            click() {
                diagnosticDownloads.push({ href: this.href, download: this.download });
            }
        };
    }
};
global.navigator = {
    userAgent: 'node-test',
    standalone: false,
    vibrate: null
};
window.navigator = navigator;
global.setInterval = (callback) => {
    intervalCallbacks.push(callback);
    return intervalCallbacks.length;
};
global.clearInterval = () => {};
global.setTimeout = () => 1;

global.Vue = {
    ref: value => ({ value }),
    computed: getter => ({ get value() { return getter(); } }),
    nextTick: async () => {},
    onMounted: callback => mountedCallbacks.push(callback),
    onUnmounted() {},
    createApp: component => ({
        mount() {
            exposed = component.setup();
        }
    })
};

global.fetch = async (url, options = {}) => {
    if (url === 'api/tvs') {
        return response({
            tvs: [
                { id: 'living', name: 'Living Room', host: '192.168.1.10', app_ids: ['netflix'] },
                { id: 'bedroom', name: 'Bedroom', host: '192.168.1.11', app_ids: ['youtube'] }
            ]
        });
    }
    if (url === 'api/apps' && options.method === 'POST') {
        const packageId = options.body.get('package_id');
        const id = packageId === 'com.plexapp.android' ? 'plex' :
            (packageId === 'tv.stream.alpha' ? 'alpha' :
            (packageId === 'tv.stream.beta' ? 'beta' : 'imported'));
        appSaveRequests.push({
            name: options.body.get('name'),
            packageId
        });
        return response({
            app: {
                id,
                name: options.body.get('name'),
                package_id: packageId,
                icon: ''
            }
        }, 201);
    }
    if (url === 'api/apps/reorder' && options.method === 'PUT') {
        const payload = JSON.parse(options.body);
        appReorderRequests.push(payload);
        return response({
            apps: payload.app_ids.map(id => ({
                id,
                name: id === 'youtube' ? 'YouTube' : 'Netflix',
                package_id: id === 'youtube' ? 'com.google.android.youtube.tv' : 'com.netflix.ninja',
                icon: ''
            }))
        });
    }
    if (url === 'api/apps') {
        return response({
            apps: [
                { id: 'netflix', name: 'Netflix', package_id: 'com.netflix.ninja', icon: 'mdi-netflix' },
                { id: 'youtube', name: 'YouTube', package_id: 'com.google.android.youtube.tv', icon: 'mdi-youtube' }
            ]
        });
    }
    if (url.startsWith('api/tvs/') && url.endsWith('/apps') && options.method === 'PUT') {
        const tvId = url.split('/')[2];
        const payload = JSON.parse(options.body);
        tvAppRequests.push({ tvId, ...payload });
        return response({
            tv: {
                id: tvId,
                name: tvId === 'bedroom' ? 'Bedroom' : 'Living Room',
                host: tvId === 'bedroom' ? '192.168.1.11' : '192.168.1.10',
                app_ids: payload.app_ids
            }
        });
    }
    if (url.startsWith('api/status?')) {
        if (failNextStatus) {
            failNextStatus = false;
            throw new Error('Server unavailable');
        }
        const tvId = new URLSearchParams(url.split('?')[1]).get('tv_id');
        return response({
            tv_id: tvId,
            tv_name: tvId === 'bedroom' ? 'Bedroom' : 'Living Room',
            connected: false,
            connecting: false,
            pairing_in_progress: false,
            apps: [],
            version: 'test'
        });
    }
    if (url === 'api/connect') {
        connectRequests.push(JSON.parse(options.body));
        return response({ status: 'connecting' });
    }
    if (url.startsWith('api/tvs/') && url.includes('/adb/')) {
        const parts = url.split('/');
        const tvId = decodeURIComponent(parts[2]);
        const action = parts.slice(4).join('/');
        const auth = options.headers && options.headers.Authorization || '';
        adbRequests.push({
            url,
            tvId,
            action,
            auth,
            method: options.method || 'GET',
            body: options.body ? JSON.parse(options.body) : null
        });
        if (auth === 'Bearer bad-token' || !auth) {
            return response({ error: 'ADB administrator authorization required', code: 'unauthorized' }, 401);
        }
        if (action === 'screenshot' || action === 'logs') {
            const run = () => {
                diagnosticRequests.push({ tvId, action });
                if (failNextDiagnostic) {
                    failNextDiagnostic = false;
                    return response({ error: 'Capture exceeded safety limit', code: 'capture_too_large' }, 413);
                }
                if (action === 'screenshot') {
                    return response('png-bytes', 200, {
                        'Content-Type': 'image/png',
                        'Content-Disposition': 'attachment; filename="droidtv-remote-' + tvId + '-screenshot.png"'
                    });
                }
                return response('redacted finite logs', 200, {
                    'Content-Type': 'text/plain; charset=utf-8',
                    'Content-Disposition': 'attachment; filename="droidtv-remote-' + tvId + '-logs.txt"'
                });
            };
            if (holdDiagnostic) {
                return new Promise(resolve => {
                    resolveDiagnostic = () => resolve(run());
                });
            }
            return run();
        }
        if (action === 'reboot' && options.method === 'POST') {
            const body = JSON.parse(options.body);
            diagnosticRequests.push({ tvId, action, body });
            adbStates[tvId] = { ...adbStates[tvId], state: 'offline' };
            return response({
                tv_id: tvId,
                tv_name: tvId === 'living' ? 'Living Room' : 'Bedroom',
                status: 'accepted',
                command_sent: true,
                adb_state: 'offline',
                message: 'Reboot command was sent. The TV will disconnect while restarting.'
            }, 202);
        }
        if (action === 'status') {
            return response({
                tv_id: tvId,
                tv_name: tvId === 'bedroom' ? 'Bedroom' : 'Living Room',
                remote: { connected: false, connecting: false, pairing_in_progress: false },
                adb: { ...adbStates[tvId] }
            });
        }
        if (action.startsWith('packages/') && options.method === 'POST') {
            const packageAction = action.split('/')[1];
            const body = JSON.parse(options.body);
            const apply = () => {
                packageAdminRequests.push({ tvId, action: packageAction, body });
                if (packageAction === 'disable') disabledAdminPackages.add(body.package_id);
                if (packageAction === 'enable') disabledAdminPackages.delete(body.package_id);
                if (packageAction === 'uninstall') uninstalledAdminPackages.add(body.package_id);
                return response({
                    tv_id: tvId,
                    action: packageAction,
                    package_id: body.package_id,
                    current_user: 0,
                    installed: packageAction !== 'uninstall',
                    package: packageAction === 'uninstall' ? undefined : {
                        package_id: body.package_id,
                        classification: 'third_party',
                        enabled: !disabledAdminPackages.has(body.package_id)
                    },
                    launcher_availability_changed: packageAction === 'disable' || packageAction === 'uninstall'
                });
            };
            if (holdPackageMutation) {
                return new Promise(resolve => {
                    resolvePackageMutation = () => resolve(apply());
                });
            }
            return apply();
        }
        if (action === 'packages') {
            if (failNextDiscovery) {
                failNextDiscovery = false;
                return response({ error: 'The TV is offline', code: 'offline' }, 409);
            }
            const payload = tvId === 'bedroom'
                ? {
                    tv_id: tvId,
                    inventory: { current_user: 0, packages: [], warnings: [] }
                }
                : {
                    tv_id: tvId,
                    inventory: {
                        current_user: 0,
                        packages: [
                            {
                                package_id: 'com.netflix.ninja',
                                classification: 'third_party',
                                enabled: true,
                                version_code: '100',
                                tv_launchable: true,
                                component: 'com.netflix.ninja/.MainActivity'
                            },
                            {
                                package_id: 'tv.stream.alpha',
                                classification: 'third_party',
                                enabled: !disabledAdminPackages.has('tv.stream.alpha'),
                                version_code: '12',
                                tv_launchable: true,
                                component: 'tv.stream.alpha/.TvActivity'
                            },
                            {
                                package_id: 'tv.stream.beta',
                                classification: 'third_party',
                                enabled: !disabledAdminPackages.has('tv.stream.beta'),
                                version_code: '7',
                                tv_launchable: true,
                                component: 'tv.stream.beta/.TvActivity'
                            },
                            {
                                package_id: 'com.vendor.system',
                                classification: 'system',
                                protected: true,
                                enabled: true,
                                version_code: '55',
                                tv_launchable: false,
                                component: ''
                            }
                        ].filter(pkg => !uninstalledAdminPackages.has(pkg.package_id)),
                        warnings: ['Ignored 1 malformed package-list lines.']
                    }
                };
            if (deferredDiscoveryTv === tvId) {
                return new Promise(resolve => {
                    resolveDeferredDiscovery = () => resolve(response(payload));
                });
            }
            return response(payload);
        }
        if (action === 'pair') {
            adbStates[tvId] = {
                state: 'offline',
                enabled: true,
                available: true,
                paired: true,
                serial: null,
                endpoint: null,
                pair_guid: 'guid-' + tvId
            };
            return response({
                tv_id: tvId,
                paired: true,
                state: 'offline',
                pair_guid: 'guid-' + tvId,
                endpoint: JSON.parse(options.body).endpoint
            });
        }
        if (action === 'connect') {
            const endpoint = JSON.parse(options.body).endpoint;
            adbStates[tvId] = {
                state: 'connected',
                enabled: true,
                available: true,
                paired: true,
                serial: endpoint,
                endpoint,
                pair_guid: adbStates[tvId].pair_guid || null
            };
            return response({ tv_id: tvId, adb: { ...adbStates[tvId] } });
        }
        if (action === 'disconnect') {
            adbStates[tvId] = { ...adbStates[tvId], state: 'offline' };
            return response({ tv_id: tvId, adb: { ...adbStates[tvId] } });
        }
        if (action === 'forget') {
            adbStates[tvId] = {
                state: 'unpaired',
                enabled: true,
                available: true,
                paired: false,
                serial: null,
                endpoint: null,
                pair_guid: null
            };
            return response({
                tv_id: tvId,
                status: 'forgotten',
                state: 'unpaired',
                warning: 'The shared ADB host key is not revoked on the TV.'
            });
        }
    }
    throw new Error(`Unexpected fetch: ${url}`);
};

function response(body, status = 200, headerValues = {}) {
    const headers = {};
    for (const [key, value] of Object.entries(headerValues)) headers[key.toLowerCase()] = value;
    return {
        ok: status >= 200 && status < 300,
        status,
        headers: {
            get(name) { return headers[String(name).toLowerCase()] || null; }
        },
        async json() { return body; },
        async blob() { return { body, size: typeof body === 'string' ? body.length : 0 }; }
    };
}

async function flushPromises() {
    for (let index = 0; index < 8; index++) {
        await Promise.resolve();
    }
}

require('../client/app.js');
assert.equal(mountedCallbacks.length, 1);
mountedCallbacks[0]();

(async () => {
    await flushPromises();
    assert.equal(exposed.selectedTvId.value, 'bedroom');
    assert.deepEqual(connectRequests[0], { tv_id: 'bedroom' });

    await exposed.selectTv('living');
    assert.equal(exposed.selectedTvId.value, 'living');
    assert.deepEqual(connectRequests.at(-1), { tv_id: 'living' });

    await exposed.openLauncherView();
    assert.equal(exposed.currentView.value, 'apps');
    assert.equal(exposed.allApps.value.length, 2);
    assert.equal(exposed.configuredTvId.value, 'living');
    exposed.selectConfiguredTv('bedroom');
    assert.deepEqual(exposed.configuredAppIds.value, ['youtube']);
    exposed.configuredAppIds.value = ['netflix'];
    await exposed.saveTvAppConfiguration();
    assert.deepEqual(tvAppRequests.at(-1), { tvId: 'bedroom', app_ids: ['netflix'] });

    exposed.openAddApp();
    exposed.appFormName.value = 'Plex';
    exposed.appFormPackageId.value = 'com.plexapp.android';
    await exposed.saveApp();
    assert.deepEqual(appSaveRequests.at(-1), {
        name: 'Plex',
        packageId: 'com.plexapp.android'
    });

    // Test TV app reordering (moving apps)
    exposed.configuredAppIds.value = ['netflix', 'youtube'];
    exposed.moveTvAppDown('netflix');
    assert.deepEqual(exposed.configuredAppIds.value, ['youtube', 'netflix']);
    exposed.moveTvAppUp('netflix');
    assert.deepEqual(exposed.configuredAppIds.value, ['netflix', 'youtube']);

    // Test Shared launcher reordering
    await exposed.moveAppDown(0);
    assert.deepEqual(appReorderRequests.at(-1), { app_ids: ['youtube', 'netflix'] });

    exposed.openRemoteView();
    assert.equal(exposed.currentView.value, 'remote');

    // ADB administration keeps credentials session-scoped and separate from Remote v2.
    await exposed.openADBView();
    assert.equal(exposed.currentView.value, 'adb');
    assert.equal(exposed.adbTokenConfigured.value, false);

    exposed.adbTokenInput.value = 'test-admin-token';
    await exposed.setADBToken();
    assert.equal(exposed.adbTokenInput.value, '');
    assert.equal(exposed.adbTokenConfigured.value, true);
    assert.equal(sessionValues.get('droidtvRemote:adbToken:/remote/'), 'test-admin-token');
    assert.equal([...localValues.values()].includes('test-admin-token'), false);
    assert.equal(exposed.adbStatus.value.adb.state, 'unpaired');
    assert.equal(adbRequests.at(-1).auth, 'Bearer test-admin-token');

    // Legacy TCP uses an explicit validated endpoint and bearer token.
    exposed.selectADBSetupMode('legacy');
    exposed.adbLegacyHost.value = '192.168.1.10';
    exposed.adbLegacyPort.value = '5555';
    await exposed.connectLegacyADB();
    const legacyConnect = adbRequests.filter(item => item.action === 'connect').at(-1);
    assert.equal(legacyConnect.tvId, 'living');
    assert.deepEqual(legacyConnect.body, { endpoint: '192.168.1.10:5555' });
    assert.equal(exposed.adbStatus.value.adb.state, 'connected');

    // APK workflow validates locally, confirms the target TV, uploads one multipart APK,
    // reports progress/results, and refreshes inventory without changing launcher records.
    exposed.handleADBAPKFile({ target: { files: [{ name: 'not-an-apk.zip', size: 20 }] } });
    assert.match(exposed.adbAPKError.value, /\.apk/i);
    assert.equal(exposed.adbAPKFile.value, null);

    const apkFile = { name: 'alpha-update.apk', size: 4096 };
    exposed.handleADBAPKFile({ target: { files: [apkFile] } });
    assert.equal(exposed.adbAPKFile.value, apkFile);
    const launcherWritesBeforeInstall = appSaveRequests.length;
    await exposed.installADBAPK();
    assert.match(confirmations.at(-1), /Living Room/);
    assert.match(confirmations.at(-1), /preserving app data/i);
    assert.equal(adbUploadRequests.at(-1).url, 'api/tvs/living/adb/install-apk');
    assert.equal(adbUploadRequests.at(-1).headers.Authorization, 'Bearer test-admin-token');
    assert.equal(adbUploadRequests.at(-1).body.get('apk'), apkFile);
    assert.equal(exposed.adbAPKUploading.value, false);
    assert.equal(exposed.adbAPKProgress.value, 100);
    assert.equal(exposed.adbAPKResult.value.tv_id, 'living');
    assert.equal(exposed.adbAPKResult.value.package.package_id, 'tv.stream.alpha');
    assert.equal(appSaveRequests.length, launcherWritesBeforeInstall);

    // Stable backend failures are rendered with actionable UI text.
    exposed.handleADBAPKFile({ target: { files: [{ name: 'signed-wrong.apk', size: 1024 }] } });
    nextADBUploadResponse = {
        status: 409,
        body: { error: 'signatures do not match', code: 'signature_mismatch' }
    };
    await exposed.installADBAPK();
    assert.match(exposed.adbAPKError.value, /signing identities/i);
    assert.equal(exposed.adbAPKResult.value, null);

    // Duplicate submission is ignored and TV switching is blocked until cancellation.
    exposed.handleADBAPKFile({ target: { files: [{ name: 'slow.apk', size: 2048 }] } });
    holdADBUpload = true;
    const pendingInstall = exposed.installADBAPK();
    assert.equal(exposed.adbAPKUploading.value, true);
    const uploadCount = adbUploadRequests.length;
    exposed.installADBAPK();
    assert.equal(adbUploadRequests.length, uploadCount);
    await exposed.selectTv('bedroom');
    assert.equal(exposed.selectedTvId.value, 'living');
    assert.match(exposed.adbAPKError.value, /cancel.*before switching/i);
    exposed.cancelADBAPKUpload();
    await pendingInstall;
    holdADBUpload = false;
    pendingADBUpload = null;
    assert.equal(exposed.adbAPKUploading.value, false);
    assert.match(exposed.adbAPKError.value, /canceled/i);

    await exposed.disconnectADB();
    assert.equal(exposed.adbStatus.value.adb.state, 'offline');

    // Secure Wi-Fi pairing has distinct pairing/connect ports and clears the code immediately.
    exposed.selectADBSetupMode('secure');
    exposed.adbPairHost.value = '192.168.1.10';
    exposed.adbPairPort.value = '37123';
    exposed.adbPairCode.value = '123456';
    exposed.adbConnectHost.value = '192.168.1.10';
    exposed.adbConnectPort.value = '42123';
    await exposed.pairSecureADB();
    assert.equal(exposed.adbPairCode.value, '');
    const pairRequest = adbRequests.filter(item => item.action === 'pair').at(-1);
    assert.deepEqual(pairRequest.body, { endpoint: '192.168.1.10:37123', code: '123456' });
    const secureConnect = adbRequests.filter(item => item.action === 'connect').at(-1);
    assert.deepEqual(secureConnect.body, { endpoint: '192.168.1.10:42123' });
    assert.equal(exposed.adbStatus.value.adb.state, 'connected');

    // One-shot diagnostics download with auth, suppress duplicates, lock the TV target,
    // surface bounded-capture errors, and use server-provided safe filenames.
    holdDiagnostic = true;
    const screenshotRequestsBefore = adbRequests.filter(item => item.action === 'screenshot').length;
    const pendingScreenshot = exposed.downloadADBDiagnostic('screenshot');
    await flushPromises();
    assert.equal(exposed.adbDiagnosticBusy.value, 'screenshot');
    exposed.downloadADBDiagnostic('screenshot');
    assert.equal(adbRequests.filter(item => item.action === 'screenshot').length, screenshotRequestsBefore + 1);
    await exposed.selectTv('bedroom');
    assert.equal(exposed.selectedTvId.value, 'living');
    assert.match(exposed.adbDiagnosticError.value, /finish before switching/i);
    resolveDiagnostic();
    await pendingScreenshot;
    holdDiagnostic = false;
    resolveDiagnostic = null;
    assert.equal(exposed.adbDiagnosticBusy.value, '');
    assert.equal(diagnosticDownloads.at(-1).download, 'droidtv-remote-living-screenshot.png');
    assert.match(exposed.adbDiagnosticMessage.value, /Screenshot downloaded/i);

    await exposed.downloadADBDiagnostic('logs');
    assert.equal(diagnosticDownloads.at(-1).download, 'droidtv-remote-living-logs.txt');
    assert.match(exposed.adbDiagnosticMessage.value, /sensitive/i);

    failNextDiagnostic = true;
    const downloadsBeforeFailure = diagnosticDownloads.length;
    await exposed.downloadADBDiagnostic('screenshot');
    assert.match(exposed.adbDiagnosticError.value, /safety limit|configured safety limit/i);
    assert.equal(diagnosticDownloads.length, downloadsBeforeFailure);

    // Reboot can be canceled, then requires confirmation tied to TV name/id/connected state.
    confirmResult = false;
    const rebootRequestsBefore = diagnosticRequests.filter(item => item.action === 'reboot').length;
    await exposed.rebootADBTV();
    assert.match(confirmations.at(-1), /cannot confirm when boot has completed/i);
    assert.equal(diagnosticRequests.filter(item => item.action === 'reboot').length, rebootRequestsBefore);

    confirmResult = true;
    await exposed.rebootADBTV();
    const rebootRequest = diagnosticRequests.filter(item => item.action === 'reboot').at(-1);
    assert.equal(rebootRequest.tvId, 'living');
    assert.deepEqual(rebootRequest.body.confirmation, {
        tv_id: 'living',
        tv_name: 'Living Room',
        state: 'connected'
    });
    assert.equal(exposed.adbStatus.value.adb.state, 'offline');
    assert.match(exposed.adbDiagnosticMessage.value, /disconnect while restarting/i);
    // Restore the fake TV for the remaining inventory tests.
    adbStates.living = { ...adbStates.living, state: 'connected' };
    await exposed.checkADBStatus();

    // Discovery is launchable-first, exact-package aware, and read-only until confirmed.
    const savesBeforeDiscovery = appSaveRequests.length;
    const tvWritesBeforeDiscovery = tvAppRequests.length;
    await exposed.discoverADBApps();
    assert.equal(exposed.adbDiscoveryPackages.value.length, 4);
    assert.equal(exposed.adbDiscoveryVisiblePackages.value.length, 3);
    assert.equal(exposed.adbDiscoveryWarnings.value.length, 1);
    const netflix = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'com.netflix.ninja');
    const alpha = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'tv.stream.alpha');
    const beta = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'tv.stream.beta');
    const systemPkg = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'com.vendor.system');
    assert.equal(netflix.existing_launcher_id, 'netflix');
    exposed.toggleADBDiscoverySelection(netflix);
    assert.equal(netflix.selected, false);
    assert.equal(systemPkg.tv_launchable, false);
    assert.equal(systemPkg.protected, true);
    assert.equal(exposed.adbDiscoveryCurrentUser.value, 0);

    // Package administration is available only from discovered third-party rows.
    // Protected rows are blocked locally, destructive actions can be canceled,
    // and the confirmation payload is tied to the fresh TV/package/user/enabled state.
    const packageWritesBefore = packageAdminRequests.length;
    await exposed.mutateADBPackage(systemPkg, 'clear');
    assert.match(exposed.adbPackageError.value, /protected|third-party/i);
    assert.equal(packageAdminRequests.length, packageWritesBefore);

    confirmResult = false;
    await exposed.mutateADBPackage(alpha, 'uninstall');
    assert.match(confirmations.at(-1), /current Android user/i);
    assert.match(confirmations.at(-1), /shared launcher record/i);
    assert.equal(packageAdminRequests.length, packageWritesBefore);
    confirmResult = true;

    holdPackageMutation = true;
    const pendingPackageMutation = exposed.mutateADBPackage(alpha, 'clear');
    await flushPromises();
    assert.match(exposed.adbPackageMutating.value, /tv\.stream\.alpha:clear/);
    assert.match(confirmations.at(-1), /Living Room/);
    assert.match(confirmations.at(-1), /local data and settings/i);
    await exposed.selectTv('bedroom');
    assert.equal(exposed.selectedTvId.value, 'living');
    assert.match(exposed.adbPackageError.value, /finish before switching/i);
    resolvePackageMutation();
    await pendingPackageMutation;
    holdPackageMutation = false;
    resolvePackageMutation = null;
    assert.equal(exposed.adbPackageMutating.value, '');
    assert.equal(packageAdminRequests.at(-1).action, 'clear');
    assert.deepEqual(packageAdminRequests.at(-1).body.confirmation, {
        tv_id: 'living',
        package_id: 'tv.stream.alpha',
        action: 'clear',
        current_user: 0,
        enabled: true
    });

    const betaAfterClear = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'tv.stream.beta');
    assert.equal(betaAfterClear.enabled, false);
    await exposed.mutateADBPackage(betaAfterClear, 'enable');
    assert.equal(packageAdminRequests.at(-1).action, 'enable');
    assert.deepEqual(packageAdminRequests.at(-1).body.confirmation, {
        tv_id: 'living',
        package_id: 'tv.stream.beta',
        action: 'enable',
        current_user: 0,
        enabled: false
    });
    const betaAfterEnable = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'tv.stream.beta');
    assert.equal(betaAfterEnable.enabled, true);
    assert.match(exposed.adbPackageMessage.value, /Enable completed/i);
    assert.equal(appSaveRequests.length, savesBeforeDiscovery);
    assert.equal(tvAppRequests.length, tvWritesBeforeDiscovery);

    exposed.setADBDiscoveryMode('all');
    assert.equal(exposed.adbDiscoveryVisiblePackages.value.length, 4);
    exposed.setADBDiscoveryMode('launchable');

    exposed.toggleADBDiscoverySelection(alpha);
    alpha.import_name = '   ';
    exposed.reviewADBImport();
    assert.match(exposed.adbDiscoveryError.value, /display name/i);
    assert.equal(exposed.adbDiscoveryPreview.value, false);
    alpha.import_name = 'Alpha TV';
    exposed.reviewADBImport();
    assert.equal(exposed.adbDiscoveryPreview.value, true);
    exposed.cancelADBImportReview();
    assert.equal(exposed.adbDiscoveryPreview.value, false);
    assert.equal(appSaveRequests.length, savesBeforeDiscovery);
    assert.equal(tvAppRequests.length, tvWritesBeforeDiscovery);

    // Refreshing/canceling discovery never writes persistent data.
    exposed.clearADBDiscovery();
    assert.equal(exposed.adbDiscoveryPackages.value.length, 0);
    await exposed.discoverADBApps();
    assert.equal(appSaveRequests.length, savesBeforeDiscovery);
    assert.equal(tvAppRequests.length, tvWritesBeforeDiscovery);

    // Selective import appends only the chosen launcher and preserves existing TV order.
    const alpha2 = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'tv.stream.alpha');
    const beta2 = exposed.adbDiscoveryPackages.value.find(item => item.package_id === 'tv.stream.beta');
    exposed.toggleADBDiscoverySelection(alpha2);
    alpha2.import_name = 'Alpha TV';
    assert.equal(beta2.selected, false);
    exposed.reviewADBImport();
    await exposed.importDiscoveredADBApps();
    assert.deepEqual(appSaveRequests.at(-1), { name: 'Alpha TV', packageId: 'tv.stream.alpha' });
    assert.deepEqual(tvAppRequests.at(-1), { tvId: 'living', app_ids: ['netflix', 'alpha'] });
    assert.equal(appSaveRequests.filter(item => item.packageId === 'tv.stream.beta').length, 0);

    // Discovery errors are visible and do not mutate launchers.
    failNextDiscovery = true;
    const savesBeforeError = appSaveRequests.length;
    await exposed.discoverADBApps();
    assert.match(exposed.adbDiscoveryError.value, /offline/i);
    assert.equal(appSaveRequests.length, savesBeforeError);

    // Stale discovery results cannot overwrite state after switching TVs.
    deferredDiscoveryTv = 'living';
    const staleDiscovery = exposed.discoverADBApps();
    await Promise.resolve();
    await exposed.selectTv('bedroom');
    assert.equal(exposed.adbDiscoveryPackages.value.length, 0);
    resolveDeferredDiscovery();
    await staleDiscovery;
    assert.equal(exposed.selectedTvId.value, 'bedroom');
    assert.equal(exposed.adbDiscoveryPackages.value.length, 0);
    deferredDiscoveryTv = '';
    resolveDeferredDiscovery = null;

    // Empty discovery results are explicit and harmless.
    await exposed.discoverADBApps();
    assert.equal(exposed.adbDiscoveryPackages.value.length, 0);
    assert.equal(appSaveRequests.length, savesBeforeError);

    // Switching TVs resets prior ADB state and queries the newly selected TV.
    assert.equal(exposed.selectedTvId.value, 'bedroom');
    assert.equal(exposed.adbStatus.value.tv_id, 'bedroom');
    assert.equal(adbRequests.at(-1).tvId, 'bedroom');

    // Wrong token is removed from session storage and prompts for a new one.
    sessionValues.set('droidtvRemote:adbToken:/remote/', 'bad-token');
    exposed.adbTokenConfigured.value = true;
    await exposed.checkADBStatus();
    assert.equal(exposed.adbTokenConfigured.value, false);
    assert.equal(sessionValues.has('droidtvRemote:adbToken:/remote/'), false);

    // Cancellation clears unsubmitted secrets from reactive state.
    exposed.adbTokenInput.value = 'temporary-secret';
    exposed.adbPairCode.value = '654321';
    exposed.closeADBView();
    assert.equal(exposed.adbTokenInput.value, '');
    assert.equal(exposed.adbPairCode.value, '');
    assert.equal(exposed.currentView.value, 'remote');

    // Re-enter with a valid session token and forget only the selected TV association.
    sessionValues.set('droidtvRemote:adbToken:/remote/', 'test-admin-token');
    await exposed.openADBView();
    exposed.adbTokenConfigured.value = true;
    await exposed.checkADBStatus();
    adbStates.bedroom = {
        state: 'connected',
        enabled: true,
        available: true,
        paired: true,
        serial: '192.168.1.11:5555',
        endpoint: '192.168.1.11:5555',
        pair_guid: null
    };
    await exposed.checkADBStatus();
    await exposed.forgetADB();
    assert.equal(exposed.adbStatus.value.adb.state, 'unpaired');
    assert.match(exposed.adbMessage.value, /not revoked/i);
    assert.equal(adbStates.living.state, 'connected');

    exposed.clearADBToken();
    assert.equal(exposed.adbTokenConfigured.value, false);
    assert.equal(sessionValues.has('droidtvRemote:adbToken:/remote/'), false);
    exposed.closeADBView();

    const requestCountBeforeRestart = connectRequests.length;
    failNextStatus = true;
    const originalConsoleError = console.error;
    console.error = () => {};
    await intervalCallbacks[0]();
    console.error = originalConsoleError;
    await intervalCallbacks[0]();
    await flushPromises();
    assert.equal(connectRequests.length, requestCountBeforeRestart + 1);
    assert.deepEqual(connectRequests.at(-1), { tv_id: 'bedroom' });
    console.log('PWA selection, launcher management, ADB administration, and automatic connection tests passed');
})().catch(error => {
    console.error(error);
    process.exitCode = 1;
});
