const CACHE_NAME = 'droidtv-remote-v__VERSION__';
const ASSETS = [
    './?v=__VERSION__',
    'index.html',
    'app.js?v=__VERSION__',
    'manifest.json?v=__VERSION__',
    'icon.png',
    'https://cdn.tailwindcss.com/',
    'https://cdn.jsdelivr.net/npm/@mdi/font@7.4.47/css/materialdesignicons.min.css',
    'https://unpkg.com/vue@3.4.15/dist/vue.global.prod.js'
];

// Install event - cache assets
self.addEventListener('install', (event) => {
    event.waitUntil(
        caches.open(CACHE_NAME).then((cache) => {
            return Promise.allSettled(
                ASSETS.map(async (url) => {
                    try {
                        const options = url.includes('tailwindcss.com') ? { mode: 'no-cors' } : {};
                        const response = await fetch(url, options);
                        if (response.type === 'opaque' || response.ok) {
                            await cache.put(url, response);
                            console.log('Successfully cached:', url);
                        } else {
                            console.error('Failed to cache (ok=false):', url);
                        }
                    } catch (error) {
                        console.error('Failed to cache (error):', url, error);
                    }
                })
            );
        })
    );
    self.skipWaiting();
});

// Activate event - clean up old caches
self.addEventListener('activate', (event) => {
    event.waitUntil(
        caches.keys().then((cacheNames) => {
            return Promise.all(
                cacheNames.map((cacheName) => {
                    if (cacheName !== CACHE_NAME) {
                        return caches.delete(cacheName);
                    }
                })
            );
        })
    );
    self.clients.claim();
});

// Fetch event - serve from cache or network
self.addEventListener('fetch', (event) => {
    const url = new URL(event.request.url);

    // Don't cache API calls
    if (url.pathname.includes('/api/')) {
        return;
    }

    event.respondWith(
        fetch(event.request, event.request.url.includes('tailwindcss.com') ? { mode: 'no-cors' } : {})
            .then(async (networkResponse) => {
                if (networkResponse && (networkResponse.type === 'opaque' || networkResponse.status === 200)) {
                    const cache = await caches.open(CACHE_NAME);
                    await cache.put(event.request, networkResponse.clone());
                }
                return networkResponse;
            })
            .catch(async () => {
                const cache = await caches.open(CACHE_NAME);
                return cache.match(event.request);
            })
    );
});

// Listen for message to skip waiting
self.addEventListener('message', (event) => {
    if (event.data && event.data.type === 'SKIP_WAITING') {
        self.skipWaiting();
    }
});
