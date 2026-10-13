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
  <div v-else-if="empty" class="rounded-2xl bg-surface px-4 py-10 text-center text-[15px] text-hint">
    {{ emptyText || 'Nothing here yet.' }}
  </div>
  <slot v-else />
</template>
