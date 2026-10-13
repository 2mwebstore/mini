import tailwindcss from '@tailwindcss/vite'

// Telegram Mini App. Client-only (ssr: false): Telegram hands the signed initData
// to the page in the browser, so there is nothing a server render could
// authenticate with. `nuxt generate` emits static files that nginx serves at /app/.
export default defineNuxtConfig({
  compatibilityDate: '2026-08-01',
  ssr: false,
  devtools: { enabled: false },
  modules: ['@pinia/nuxt'],
  components: [{ path: '~/components', pathPrefix: false }],
  css: ['~/assets/css/main.css'],
  vite: { plugins: [tailwindcss()] },

  app: {
    baseURL: '/app/',
    head: {
      title: 'Attendance',
      htmlAttrs: { lang: 'en' },
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/app/favicon.svg' }],
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1, maximum-scale=1, viewport-fit=cover' },
        { name: 'format-detection', content: 'telephone=no' },
      ],
      // Official SDK; must load before the app (sets window.Telegram.WebApp and theme CSS variables).
      script: [{ src: 'https://telegram.org/js/telegram-web-app.js' }],
    },
  },

  runtimeConfig: {
    public: {
      // Same origin as the page in production (nginx routes /api/ to the Go API).
      apiBase: '/api/v1/app',
    },
  },

  // `npm run dev`: forward API calls to a locally running Go API.
  nitro: {
    devProxy: {
      '/api': { target: process.env.API_DEV_TARGET || 'http://localhost:8080/api', changeOrigin: true },
    },
  },

  typescript: { strict: true },
})
