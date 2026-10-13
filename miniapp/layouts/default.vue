<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'

const auth = useAuthStore()

const tabs = computed(() => [
  { to: '/', label: 'Home', icon: 'home' as const },
  { to: '/history', label: 'History', icon: 'history' as const },
  { to: '/leave', label: 'Leave', icon: 'leave' as const },
  ...(auth.isReviewer ? [{ to: '/team', label: 'Team', icon: 'team' as const }] : []),
  { to: '/profile', label: 'Profile', icon: 'user' as const },
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
          class="flex flex-col items-center gap-0.5 pb-1.5 pt-2 text-[11px] font-medium"
          :class="active(t.to) ? 'text-accent' : 'text-hint'"
          :aria-current="active(t.to) ? 'page' : undefined"
        >
          <span class="flex h-7 w-12 items-center justify-center rounded-full transition-colors" :class="active(t.to) ? 'bg-accent/15' : ''">
            <AppIcon :name="t.icon" class="size-[22px]" />
          </span>
          {{ t.label }}
        </NuxtLink>
      </li>
    </ul>
  </nav>
</template>
