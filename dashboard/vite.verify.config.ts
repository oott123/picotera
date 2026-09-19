// Throwaway config used to point the dev-server proxy at a scratch backend
// instance during verification. Not part of the build; delete after use.
import { defineConfig, mergeConfig } from 'vite'
import base from './vite.config'

export default mergeConfig(
  base,
  defineConfig({
    server: {
      proxy: {
        '/api/': {
          target: 'http://localhost:9899',
          changeOrigin: true,
          headers: { 'X-User-Identity': 'root' },
        },
      },
    },
  }),
)