<script setup lang="ts">
import type { Tone } from '~/utils/format'

defineProps<{ label: string; tone: Tone }>()
</script>

<template>
  <span
    class="inline-flex items-center whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium"
    :class="{
      'bg-success/12 text-success': tone === 'success',
      'bg-warning/12 text-warning': tone === 'warning',
      'bg-danger/12 text-danger': tone === 'danger',
      'bg-info/12 text-info': tone === 'info',
      'bg-muted/12 text-muted': tone === 'neutral',
    }"
  >{{ label }}</span>
</template>
