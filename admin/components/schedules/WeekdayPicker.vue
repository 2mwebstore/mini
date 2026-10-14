<script setup lang="ts">
import { weekdayShort } from '~/utils/format'

// Pick ISO weekdays (1 = Monday … 7 = Sunday) as toggle buttons.
const model = defineModel<number[]>({ required: true })
defineProps<{ label: string; disabled?: boolean }>()

function toggle(d: number) {
  model.value = model.value.includes(d) ? model.value.filter(x => x !== d) : [...model.value, d].sort((a, b) => a - b)
}
const presets = [
  { label: 'Mon–Fri', days: [1, 2, 3, 4, 5] },
  { label: 'Mon–Sat', days: [1, 2, 3, 4, 5, 6] },
  { label: 'Every day', days: [1, 2, 3, 4, 5, 6, 7] },
]
</script>

<template>
  <div>
    <div class="flex flex-wrap gap-1.5" role="group" :aria-label="label">
      <button
        v-for="(name, i) in weekdayShort"
        :key="name"
        type="button"
        :aria-pressed="model.includes(i + 1)"
        :disabled="disabled"
        class="h-9 min-w-11 rounded-lg border px-2 text-sm font-medium transition disabled:opacity-50"
        :class="model.includes(i + 1) ? 'border-primary bg-primary text-primary-fg' : 'border-border bg-surface text-muted hover:text-fg'"
        @click="toggle(i + 1)"
      >
        {{ name }}
      </button>
    </div>
    <div class="mt-1.5 flex flex-wrap gap-3 text-xs">
      <button v-for="p in presets" :key="p.label" type="button" class="text-primary hover:underline disabled:opacity-50" :disabled="disabled" @click="model = [...p.days]">
        {{ p.label }}
      </button>
    </div>
  </div>
</template>
