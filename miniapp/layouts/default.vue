<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'

const auth = useAuthStore()

const tabs = computed(() => [
  { to: '/', label: 'Home', icon: 'M3 10.5 12 3l9 7.5V20a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z' },
  { to: '/history', label: 'History', icon: 'M7 3v3M17 3v3M4 8h16M5 5h14a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm3 7h3v3H8z' },
  { to: '/leave', label: 'Leave', icon: 'M12 3a9 9 0 0 1 9 9H3a9 9 0 0 1 9-9zm0 9v9m-3 0h6' },
  ...(auth.isReviewer ? [{ to: '/team', label: 'Team', icon: 'M16 19v-1a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v1M9 10a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm13 9v-1a4 4 0 0 0-3-3.9M16 4.1a3 3 0 0 1 0 5.8' }] : []),
  { to: '/profile', label: 'Profile', icon: 'M20 21v-1a5 5 0 0 0-5-5H9a5 5 0 0 0-5 5v1M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8z' },
])

const route = useRoute()
const active = (to: string) => (to === '/' ? route.path === '/' : route.path.startsWith(to))
</script>

<template>
  <div class="mx-auto max-w-xl px-4 pb-[calc(5rem+env(safe-area-inset-bottom))] pt-4">
    <slot />
  </div>
  <nav
    class="fixed inset-x-0 bottom-0 z-30 border-t border-separator bg-surface/95 pb-[env(safe-area-inset-bottom)] backdrop-blur"
    aria-label="Main"
  >
    <ul class="mx-auto flex max-w-xl">
      <li v-for="t in tabs" :key="t.to" class="flex-1">
        <NuxtLink
          :to="t.to"
          class="flex flex-col items-center gap-0.5 py-2 text-[11px] font-medium"
          :class="active(t.to) ? 'text-accent' : 'text-hint'"
          :aria-current="active(t.to) ? 'page' : undefined"
        >
          <svg viewBox="0 0 24 24" class="size-6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path :d="t.icon" />
          </svg>
          {{ t.label }}
        </NuxtLink>
      </li>
    </ul>
  </nav>
</template>
