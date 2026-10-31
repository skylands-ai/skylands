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

export default defineConfig({
  plugins: [keepGitkeep()],
  // three.js is one chunk on purpose, so the default 500 kB warning is noise.
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 700 },
  server: {
    // During "npm run dev", a running citadel answers the WebSocket.
    proxy: {
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
      '/healthz': 'http://127.0.0.1:8080',
    },
  },
})
