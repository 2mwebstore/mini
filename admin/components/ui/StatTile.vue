<script setup lang="ts">
defineProps<{ label: string; value: string | number; tone?: 'success' | 'warning' | 'danger' | 'info' | 'neutral'; hint?: string }>()
</script>

<template>
  <div class="card px-4 py-3">
    <p class="text-xs font-medium text-muted">{{ label }}</p>
    <p
      class="mt-1 text-2xl font-semibold tabular-nums"
      :class="{ 'text-success': tone === 'success', 'text-warning': tone === 'warning', 'text-danger': tone === 'danger', 'text-info': tone === 'info' }"
    >{{ value }}</p>
    <p v-if="hint" class="mt-0.5 text-xs text-muted">{{ hint }}</p>
  </div>
</template>
