<script setup lang="ts">
// A small set of mutually exclusive choices shown as one segmented control.
const model = defineModel<string>({ required: true })
defineProps<{ options: { value: string; label: string; hint?: string }[]; label: string; disabled?: boolean }>()
</script>

<template>
  <div role="radiogroup" :aria-label="label" class="inline-flex w-full flex-wrap gap-1 rounded-lg bg-surface-2 p-1">
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="radio"
      :aria-checked="model === o.value"
      :disabled="disabled"
      :title="o.hint"
      class="flex-1 whitespace-nowrap rounded-md px-3 py-1.5 text-sm font-medium transition disabled:opacity-50"
      :class="model === o.value ? 'bg-surface text-fg shadow-sm' : 'text-muted hover:text-fg'"
      @click="model = o.value"
    >
      {{ o.label }}
    </button>
  </div>
</template>
