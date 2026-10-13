<script setup lang="ts">
defineProps<{ title?: string }>()
</script>

<template>
  <section>
    <h2 v-if="title" class="mb-1.5 px-4 text-[13px] font-medium uppercase tracking-wide text-hint">{{ title }}</h2>
    <div class="rounded-2xl bg-surface p-4">
      <slot />
    </div>
  </section>
</template>
