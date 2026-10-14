<script setup lang="ts">
defineProps<{ options: { value: string; label: string }[] }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div class="flex rounded-xl bg-hint/15 p-1" role="tablist">
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="tab"
      :aria-selected="model === o.value"
      class="flex-1 rounded-lg py-1.5 text-sm font-medium transition"
      :class="model === o.value ? 'bg-surface text-fg shadow-sm' : 'text-hint'"
      @click="model = o.value"
    >{{ o.label }}</button>
  </div>
</template>
