<script setup lang="ts">
import type { Tone } from '~/utils/format'

defineProps<{ label: string; tone: Tone }>()
</script>

<template>
  <span
    class="inline-flex items-center whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium"
    :class="{
      'bg-success/15 text-success': tone === 'success',
      'bg-warning/15 text-warning': tone === 'warning',
      'bg-danger/15 text-danger': tone === 'danger',
      'bg-info/15 text-info': tone === 'info',
      'bg-hint/15 text-hint': tone === 'neutral',
    }"
  >{{ label }}</span>
</template>
