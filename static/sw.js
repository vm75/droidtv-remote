const CACHE_NAME = 'droidtv-remote-v1';
const ASSETS = [
    './',
    'index.html',
    'app.js',
    'manifest.json',
    'icon.svg',
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
        caches.match(event.request).then((cachedResponse) => {
            const fetchOptions = event.request.url.includes('tailwindcss.com') ? { mode: 'no-cors' } : {};

            const fetchPromise = fetch(event.request, fetchOptions).then((networkResponse) => {
                if (networkResponse && (networkResponse.type === 'opaque' || networkResponse.status === 200)) {
                    caches.open(CACHE_NAME).then((cache) => {
                        cache.put(event.request, networkResponse.clone());
                    });
                }
                return networkResponse;
            }).catch(() => {
                return cachedResponse;
            });

            return cachedResponse || fetchPromise;
        })
    );
});

// Listen for message to skip waiting
self.addEventListener('message', (event) => {
    if (event.data && event.data.type === 'SKIP_WAITING') {
        self.skipWaiting();
    }
});
