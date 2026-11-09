// The citadel's service worker. Chrome offers to install a page as an app
// only when a service worker with a fetch handler controls it.
//
// It caches nothing: every request goes to the citadel, so an upgraded
// citadel is never served from a stale copy. The API, the assets, and the
// WebSocket are left to the browser. Only a page opened while the citadel
// can't be reached is answered here, with a page saying so, which carries
// its own copy of the logo, since nothing can be fetched then.

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

const UNREACHABLE = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Skylands</title>
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font:16px system-ui,sans-serif;background:#E6EDF4;color:#13202E}
@media (prefers-color-scheme: dark){body{background:#101B27;color:#E7EEF4}}
main{max-width:420px;padding:24px;text-align:center}
button{margin-top:12px;min-height:40px;padding:0 18px;border-radius:8px;border:0;background:#FFD54A;color:#1A1708;font:inherit;font-weight:600;cursor:pointer}
</style></head>
<body><main>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="10 4 200 200" width="72" height="72" aria-hidden="true"><polygon points="36,95 60,105 66,142 54,128 44,112" fill="#9A7656"/><polygon points="60,105 100,110 102,176 92,166 80,156 66,142" fill="#7C5D44"/><polygon points="100,110 136,107 124,152 112,164 102,176" fill="#614836"/><polygon points="136,107 164,93 158,108 148,124 136,138 124,152" fill="#4B382B"/><path d="M36,86 C34,96 64,101 100,101 C136,101 166,94 164,84 L164,93 C164,103 136,110 100,110 C64,110 36,104 36,95 Z" fill="#8A6542"/><path d="M36,86 C40,74 70,68 100,69 C130,68 160,73 164,84 C166,94 136,101 100,101 C64,101 34,96 36,86 Z" fill="#6CB35A"/><path d="M50,82 C60,75 84,72 100,72 C120,72 144,75 152,82 C130,78 70,78 50,82 Z" fill="#86C76F"/><polygon points="54,80 61,92 47,92" fill="#2F6B3A"/><polygon points="66,74 72,86 60,86" fill="#3C7D43"/><rect x="110" y="74" width="9" height="12" fill="#EFE9D8"/><rect x="121" y="68" width="8" height="18" fill="#E2DBC7"/><rect x="131" y="77" width="8" height="9" fill="#EFE9D8"/><circle cx="102" cy="187" r="12" fill="#7FE0F0" opacity="0.35"/><polygon points="102,179 108,187 102,197 96,187" fill="#5FD3E6"/><polygon points="102,179 108,187 102,187" fill="#BDF3FA"/><path d="M150 14 Q153 31 170 34 Q153 37 150 54 Q147 37 130 34 Q147 31 150 14 Z" fill="#F5B014"/><path d="M175 51 Q176.4 58.6 184 60 Q176.4 61.4 175 69 Q173.6 61.4 166 60 Q173.6 58.6 175 51 Z" fill="#F5B014"/><path d="M124 11 Q124.8 15.2 129 16 Q124.8 16.8 124 21 Q123.2 16.8 119 16 Q123.2 15.2 124 11 Z" fill="#F5B014"/></svg>
<h1>The citadel can’t be reached</h1>
<p>Skylands needs its citadel to show anything. Check that it’s running, and that this device can reach its address.</p>
<button type="button" onclick="location.reload()">Try again</button>
</main></body></html>`

self.addEventListener('fetch', (event) => {
  if (event.request.mode !== 'navigate') return
  event.respondWith(
    fetch(event.request).catch(() => new Response(UNREACHABLE, { status: 503, headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' } })),
  )
})
