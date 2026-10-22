import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'
import { msw } from 'msw/vite'

export default defineConfig({
  plugins: [
    react(), 
    tailwindcss(),
    msw() 
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        // (path) => path.replace(/^\/api/, ''), jika backend diganti dan tidak pakai prefix /api
      }
    }
  }
})