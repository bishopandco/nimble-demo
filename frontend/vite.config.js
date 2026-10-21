import { defineConfig, loadEnv } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'API_')
  const proxy = {
    '^/ws$': {
      target: process.env.API_TARGET || env.API_TARGET || 'http://127.0.0.1:8080',
      ws: true,
      // Keep Host and Origin aligned for the API's existing origin check.
      changeOrigin: false,
    },
  }

  return {
    plugins: [svelte(), svelteTesting({ autoCleanup: false })],
    server: { host: '127.0.0.1', port: 5173, strictPort: true, proxy },
    preview: { host: '127.0.0.1', port: 4173, strictPort: true, proxy },
    test: { environment: 'happy-dom' },
  }
})
