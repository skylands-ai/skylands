import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vite'

// Vite empties dist/ on every build, which removes the .gitkeep that lets a
// fresh clone compile the Go embed before the client is built. Put it back.
function keepGitkeep(): Plugin {
  return {
    name: 'keep-gitkeep',
    closeBundle() {
      writeFileSync(resolve(import.meta.dirname, 'dist/.gitkeep'), '')
    },
  }
}

// The citadel serves the setup page at /setup, where a browser that can't
// sign in is sent. Serve it there during "npm run dev" too.
function setupPage(): Plugin {
  return {
    name: 'setup-page',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        if (req.url === '/setup' || req.url?.startsWith('/setup?')) req.url = '/setup.html' + req.url.slice('/setup'.length)
        next()
      })
    },
  }
}

export default defineConfig({
  plugins: [keepGitkeep(), setupPage()],
  // three.js is one chunk on purpose, so the default 500 kB warning is noise.
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 700,
    // The world's page, and the page that sets a world up.
    rollupOptions: { input: { main: resolve(import.meta.dirname, 'index.html'), setup: resolve(import.meta.dirname, 'setup.html') } },
  },
  server: {
    // During "npm run dev", a running citadel answers the WebSocket and the
    // API. The proxy keeps the page's Host, which the citadel checks against
    // its Origin and names the sign-in cookie for; Vite's shorthand would
    // send the citadel's own instead.
    proxy: {
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true, changeOrigin: false },
      '/healthz': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/themes': { target: 'http://127.0.0.1:8080', changeOrigin: false },
    },
  },
})
