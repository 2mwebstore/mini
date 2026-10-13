<script setup lang="ts">
defineProps<{ label: string; for?: string; hint?: string }>()
</script>

<template>
  <div>
    <label class="label" :for="$props.for">{{ label }}</label>
    <slot />
    <p v-if="hint" class="mt-1 text-xs text-muted">{{ hint }}</p>
  </div>
</template>
