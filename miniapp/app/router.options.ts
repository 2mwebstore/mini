import type { RouterConfig } from '@nuxt/schema'

// Telegram appends launch parameters to the URL hash (#tgWebAppData=…), which
// must not be treated as an anchor to scroll to.
export default <RouterConfig>{
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
}
