<script setup lang="ts">
// A time field with our own picker (a bottom sheet): an hour column and a minute
// column (5-minute steps). Phones' native time inputs don't open reliably inside
// Telegram, so the app draws the picker itself.
const model = defineModel<string>({ required: true }) // HH:MM
defineProps<{ label: string; note?: string; required?: boolean }>()

const open = ref(false)
const hour = ref(0)
const minute = ref(0)
const hours = Array.from({ length: 24 }, (_, i) => i)
const minutes = Array.from({ length: 12 }, (_, i) => i * 5)
const pad = (n: number) => String(n).padStart(2, '0')
const hourList = ref<HTMLElement>()
const minuteList = ref<HTMLElement>()

async function show() {
  const [h, m] = (model.value || '09:00').split(':').map(Number)
  hour.value = h ?? 9
  minute.value = Math.round((m ?? 0) / 5) * 5 % 60
  open.value = true
  await nextTick()
  // start with the chosen time in view
  for (const list of [hourList.value, minuteList.value]) {
    list?.querySelector<HTMLElement>('[aria-pressed="true"]')?.scrollIntoView({ block: 'center' })
  }
}
function done() {
  model.value = `${pad(hour.value)}:${pad(minute.value)}`
  webApp()?.HapticFeedback?.selectionChanged()
  open.value = false
}
</script>

<template>
  <div class="min-w-0">
    <span class="block text-[13px] text-hint">{{ label }}</span>
    <button
      type="button"
      class="mt-1 flex min-h-[52px] w-full items-center gap-2 rounded-xl border border-separator bg-surface px-3 py-1.5 text-left transition active:bg-page"
      :aria-label="`${label}: ${model || 'choose a time'}`"
      aria-haspopup="dialog"
      @click="show"
    >
      <AppIcon name="clock" class="hidden size-5 shrink-0 text-hint min-[420px]:block" />
      <span class="min-w-0 flex-1">
        <span class="block text-[17px] font-semibold tabular-nums" :class="!model && 'text-hint'">{{ model || '--:--' }}</span>
        <span v-if="note" class="block truncate text-[12px] text-hint">{{ note }}</span>
      </span>
    </button>

    <BottomSheet v-model="open" :title="label">
      <p class="mb-3 text-center text-[40px] font-bold leading-none tabular-nums" aria-live="polite">{{ pad(hour) }}:{{ pad(minute) }}</p>
      <div class="grid grid-cols-2 gap-3">
        <div>
          <p class="mb-1 text-center text-[12px] font-medium uppercase tracking-wide text-hint">Hour</p>
          <div ref="hourList" class="grid h-56 grid-cols-3 content-start gap-1.5 overflow-y-auto rounded-2xl bg-page p-1.5" role="group" aria-label="Hour">
            <button
              v-for="h in hours" :key="h" type="button"
              class="rounded-xl py-2.5 text-[16px] tabular-nums transition"
              :class="h === hour ? 'bg-primary font-semibold text-primary-fg' : 'bg-surface active:bg-separator'"
              :aria-pressed="h === hour" @click="hour = h"
            >{{ pad(h) }}</button>
          </div>
        </div>
        <div>
          <p class="mb-1 text-center text-[12px] font-medium uppercase tracking-wide text-hint">Minute</p>
          <div ref="minuteList" class="grid h-56 grid-cols-3 content-start gap-1.5 overflow-y-auto rounded-2xl bg-page p-1.5" role="group" aria-label="Minute">
            <button
              v-for="m in minutes" :key="m" type="button"
              class="rounded-xl py-2.5 text-[16px] tabular-nums transition"
              :class="m === minute ? 'bg-primary font-semibold text-primary-fg' : 'bg-surface active:bg-separator'"
              :aria-pressed="m === minute" @click="minute = m"
            >{{ pad(m) }}</button>
          </div>
        </div>
      </div>
      <div class="mt-4 grid grid-cols-2 gap-2">
        <AppButton variant="secondary" @click="open = false">Cancel</AppButton>
        <AppButton @click="done">Set {{ pad(hour) }}:{{ pad(minute) }}</AppButton>
      </div>
    </BottomSheet>
  </div>
</template>
