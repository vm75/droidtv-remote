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
let failNextStatus = false;
let exposed;

const storage = {
    getItem() { return 'bedroom'; },
    setItem() {},
    removeItem() {}
};

global.window = {
    location: { pathname: '/remote/', hostname: 'localhost' },
    localStorage: storage,
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

    const requestCountBeforeRestart = connectRequests.length;
    failNextStatus = true;
    const originalConsoleError = console.error;
    console.error = () => {};
    await intervalCallbacks.at(-1)();
    console.error = originalConsoleError;
    await intervalCallbacks.at(-1)();
    await flushPromises();
    assert.equal(connectRequests.length, requestCountBeforeRestart + 1);
    assert.deepEqual(connectRequests.at(-1), { tv_id: 'living' });
    console.log('PWA selection, launcher management, and automatic connection tests passed');
})().catch(error => {
    console.error(error);
    process.exitCode = 1;
});
