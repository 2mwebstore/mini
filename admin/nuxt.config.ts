import tailwindcss from '@tailwindcss/vite'

// HR admin panel. A private, client-heavy tool: rendered in the browser
// (ssr: false) and served as static files by nginx at /admin/. The Go API is the
// security boundary; the session lives in an HttpOnly cookie the page can't read.
export default defineNuxtConfig({
  compatibilityDate: '2026-08-01',
  ssr: false,
  devtools: { enabled: false },
  modules: ['@pinia/nuxt'],
  components: [{ path: '~/components', pathPrefix: false }],
  css: ['~/assets/css/main.css'],
  vite: { plugins: [tailwindcss()] },

  app: {
    baseURL: '/admin/',
    head: {
      title: 'Attendance Admin',
      htmlAttrs: { lang: 'en' },
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/admin/favicon.svg' }],
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'robots', content: 'noindex, nofollow' },
      ],
    },
  },

  runtimeConfig: {
    public: { apiBase: '/api/v1' },
  },

  // `npm run dev`: forward API calls to a locally running Go API.
  nitro: {
    devProxy: {
      '/api': { target: process.env.API_DEV_TARGET || 'http://localhost:8080/api', changeOrigin: true },
    },
  },

  typescript: { strict: true },
})
