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
let failNextStatus = false;
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
const adbStates = {
    living: { state: 'unpaired', enabled: true, available: true, paired: false, serial: null, endpoint: null, pair_guid: null },
    bedroom: { state: 'unpaired', enabled: true, available: true, paired: false, serial: null, endpoint: null, pair_guid: null }
};

global.window = {
    location: { pathname: '/remote/', hostname: 'localhost' },
    localStorage: storage,
    sessionStorage,
    matchMedia: () => ({ matches: false }),
    addEventListener() {},
    removeEventListener() {},
    isSecureContext: true,
    navigator: null,
    confirm: () => true
};
global.document = { cookie: '', hidden: false };
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
        appSaveRequests.push({
            name: options.body.get('name'),
            packageId: options.body.get('package_id')
        });
        return response({
            app: {
                id: 'plex',
                name: options.body.get('name'),
                package_id: options.body.get('package_id'),
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
        const action = parts[4];
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
        if (action === 'status') {
            return response({
                tv_id: tvId,
                tv_name: tvId === 'bedroom' ? 'Bedroom' : 'Living Room',
                remote: { connected: false, connecting: false, pairing_in_progress: false },
                adb: { ...adbStates[tvId] }
            });
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

function response(body, status = 200) {
    return {
        ok: status >= 200 && status < 300,
        status,
        async json() { return body; }
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

    // Switching TVs resets prior ADB state and queries the newly selected TV.
    await exposed.selectTv('bedroom');
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
