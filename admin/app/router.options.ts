import type { RouterConfig } from '@nuxt/schema'

export default <RouterConfig>{
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
}
