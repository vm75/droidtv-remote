const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const listeners = {};
const staleResponse = { body: 'stale app.js' };
const freshResponse = {
    body: 'fresh app.js',
    status: 200,
    type: 'basic',
    clone() { return this; }
};
let responsePromise;

const context = {
    URL,
    Promise,
    console,
    fetch: async () => freshResponse,
    caches: {
        match: async () => staleResponse,
        open: async () => ({ put: async () => {} }),
        keys: async () => []
    },
    self: {
        addEventListener(type, listener) {
            listeners[type] = listener;
        },
        skipWaiting() {},
        clients: { claim() {} }
    }
};

vm.runInNewContext(fs.readFileSync(require.resolve('../client/sw.js'), 'utf8'), context);

listeners.fetch({
    request: { url: 'https://remote.example/app.js' },
    respondWith(promise) {
        responsePromise = promise;
    }
});

(async () => {
    const response = await responsePromise;
    assert.equal(
        response,
        freshResponse,
        'a new deployment must replace a persisted stale PWA asset'
    );
    console.log('PWA stale-cache update test passed');
})().catch(error => {
    console.error(error);
    process.exitCode = 1;
});
