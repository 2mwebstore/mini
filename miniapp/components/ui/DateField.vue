<script setup lang="ts">
// A date field with our own calendar (a bottom sheet). Phones' native date inputs
// behave differently inside Telegram — some don't open at all — so the app draws
// the picker itself: a month grid, today marked, earlier than `min` greyed out.
const model = defineModel<string>({ required: true }) // YYYY-MM-DD
const props = defineProps<{ label: string; min?: string; required?: boolean }>()

const iso = (y: number, m: number, d: number) => `${y}-${String(m + 1).padStart(2, '0')}-${String(d).padStart(2, '0')}`
const parse = (v?: string) => {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v ?? '')
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null
}
const shown = computed(() => {
  const d = parse(model.value)
  return d && {
    date: d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' }),
    weekday: d.toLocaleDateString('en-GB', { weekday: 'long' }),
  }
})

// ---------- the calendar ----------
const open = ref(false)
const today = (() => { const n = new Date(); return iso(n.getFullYear(), n.getMonth(), n.getDate()) })()
const view = reactive({ y: 0, m: 0 }) // the month on screen
function show() {
  const d = parse(model.value) ?? parse(props.min) ?? new Date()
  Object.assign(view, { y: d.getFullYear(), m: d.getMonth() })
  open.value = true
}
function move(delta: number) {
  const d = new Date(view.y, view.m + delta, 1)
  Object.assign(view, { y: d.getFullYear(), m: d.getMonth() })
}
const monthName = computed(() => new Date(view.y, view.m, 1).toLocaleDateString('en-GB', { month: 'long', year: 'numeric' }))
const weekdays = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']
// The month's days, Monday first, padded with blanks before the 1st.
const cells = computed(() => {
  const first = new Date(view.y, view.m, 1)
  const blanks = (first.getDay() + 6) % 7
  const days = new Date(view.y, view.m + 1, 0).getDate()
  const out: ({ day: number; value: string; disabled: boolean } | null)[] = Array(blanks).fill(null)
  for (let d = 1; d <= days; d++) {
    const value = iso(view.y, view.m, d)
    out.push({ day: d, value, disabled: !!props.min && value < props.min })
  }
  return out
})
function pick(value: string) {
  model.value = value
  webApp()?.HapticFeedback?.selectionChanged()
  open.value = false
}
const canToday = computed(() => !props.min || today >= props.min)
</script>

<template>
  <div class="min-w-0">
    <span class="block text-[13px] text-hint">{{ label }}</span>
    <button
      type="button"
      class="mt-1 flex min-h-[52px] w-full items-center gap-2 rounded-xl border border-separator bg-surface px-3 py-1.5 text-left transition active:bg-page"
      :aria-label="`${label}: ${shown ? `${shown.weekday} ${shown.date}` : 'choose a date'}`"
      aria-haspopup="dialog"
      @click="show"
    >
      <AppIcon name="calendar" class="hidden size-5 shrink-0 text-hint min-[420px]:block" />
      <span class="min-w-0 flex-1">
        <span class="block truncate text-[15px] font-medium tabular-nums" :class="!shown && 'text-hint'">{{ shown?.date ?? 'Choose a date' }}</span>
        <span v-if="shown" class="block truncate text-[12px] text-hint">{{ shown.weekday }}</span>
      </span>
    </button>

    <BottomSheet v-model="open" :title="label">
      <div class="mb-3 flex items-center justify-between">
        <button type="button" class="flex size-10 items-center justify-center rounded-full active:bg-page" aria-label="Previous month" @click="move(-1)">
          <AppIcon name="chevron" class="size-5 rotate-180" />
        </button>
        <p class="text-[16px] font-semibold" aria-live="polite">{{ monthName }}</p>
        <button type="button" class="flex size-10 items-center justify-center rounded-full active:bg-page" aria-label="Next month" @click="move(1)">
          <AppIcon name="chevron" class="size-5" />
        </button>
      </div>
      <div class="grid grid-cols-7 gap-1 text-center" role="grid" :aria-label="monthName">
        <span v-for="w in weekdays" :key="w" class="pb-1 text-[12px] font-medium text-hint">{{ w }}</span>
        <template v-for="(c, i) in cells" :key="i">
          <span v-if="!c" aria-hidden="true" />
          <button
            v-else
            type="button"
            class="flex aspect-square items-center justify-center rounded-full text-[15px] tabular-nums transition disabled:opacity-30"
            :class="c.value === model ? 'bg-primary font-semibold text-primary-fg'
              : c.value === today ? 'font-semibold text-accent ring-1 ring-inset ring-primary/50' : 'active:bg-page'"
            :disabled="c.disabled"
            :aria-pressed="c.value === model"
            :aria-label="new Date(view.y, view.m, c.day).toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' })"
            @click="pick(c.value)"
          >{{ c.day }}</button>
        </template>
      </div>
      <div class="mt-4 grid grid-cols-2 gap-2">
        <AppButton variant="secondary" :disabled="!canToday" @click="pick(today)">Today</AppButton>
        <AppButton variant="secondary" @click="open = false">Close</AppButton>
      </div>
    </BottomSheet>
  </div>
</template>
