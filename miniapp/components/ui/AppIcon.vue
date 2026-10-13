<script setup lang="ts">
import { icons, type IconName } from '~/utils/icons'

defineProps<{ name: IconName }>()
</script>

<template>
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path :d="icons[name]" />
  </svg>
</template>
