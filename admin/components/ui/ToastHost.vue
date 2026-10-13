<script setup lang="ts">
import { useToastStore } from '~/stores/toast'

const toast = useToastStore()
</script>

<template>
  <div class="pointer-events-none fixed bottom-4 right-4 z-50 flex w-[min(24rem,calc(100%-2rem))] flex-col gap-2" aria-live="polite">
    <TransitionGroup enter-from-class="translate-y-2 opacity-0" leave-to-class="opacity-0" enter-active-class="transition" leave-active-class="transition">
      <div
        v-for="t in toast.items"
        :key="t.id"
        class="pointer-events-auto flex items-start gap-3 rounded-lg border px-4 py-3 text-sm shadow-lg"
        :class="t.tone === 'error' ? 'border-danger/30 bg-surface text-danger' : 'border-success/30 bg-surface text-fg'"
        :role="t.tone === 'error' ? 'alert' : 'status'"
      >
        <span aria-hidden="true">{{ t.tone === 'error' ? '⚠️' : '✓' }}</span>
        <p class="flex-1">{{ t.message }}</p>
        <button type="button" class="text-muted hover:text-fg" aria-label="Dismiss" @click="toast.dismiss(t.id)">×</button>
      </div>
    </TransitionGroup>
  </div>
</template>
