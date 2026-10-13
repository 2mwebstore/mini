<script setup lang="ts">
import type { ApiError } from '~/services/api'

// Loading / error (with retry) / empty states for an API-driven section.
defineProps<{ loading?: boolean; error?: ApiError | null; empty?: boolean; emptyText?: string }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <div v-if="loading" class="flex justify-center py-10" role="status" aria-label="Loading">
    <span class="size-6 animate-spin rounded-full border-2 border-hint/40 border-t-primary" />
  </div>
  <div v-else-if="error" class="rounded-2xl bg-surface px-4 py-8 text-center">
    <p class="text-[15px]">{{ error.message }}</p>
    <AppButton class="mt-4" variant="secondary" @click="$emit('retry')">Try again</AppButton>
  </div>
  <div v-else-if="empty" class="flex flex-col items-center gap-3 rounded-2xl bg-surface px-6 py-10 text-center">
    <span class="flex size-12 items-center justify-center rounded-full bg-page text-hint">
      <AppIcon name="inbox" class="size-6" />
    </span>
    <p class="text-[15px] text-hint">{{ emptyText || 'Nothing here yet.' }}</p>
    <slot name="empty-action" />
  </div>
  <slot v-else />
</template>
