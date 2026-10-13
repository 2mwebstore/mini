<script setup lang="ts">
import type { ApiError } from '~/services/api'

// Loading / error (with retry) / empty states for an API-driven section.
defineProps<{ loading?: boolean; error?: ApiError | null; empty?: boolean; emptyText?: string }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <div v-if="loading" class="flex justify-center py-16" role="status" aria-label="Loading">
    <span class="size-6 animate-spin rounded-full border-2 border-border border-t-primary" />
  </div>
  <div v-else-if="error" class="card px-6 py-12 text-center">
    <p class="text-sm">{{ error.message }}</p>
    <AppButton class="mt-4" variant="secondary" @click="$emit('retry')">Try again</AppButton>
  </div>
  <div v-else-if="empty" class="card px-6 py-12 text-center text-sm text-muted">
    {{ emptyText || 'Nothing to show.' }}
  </div>
  <slot v-else />
</template>
