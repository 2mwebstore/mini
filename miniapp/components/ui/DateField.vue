<script setup lang="ts">
// A date field that looks the same on every phone: our own box with the date written
// out ("14 Oct 2026", "Wednesday"), over the phone's own date input, which opens its
// picker when tapped. (Native date inputs are sized and drawn differently by iOS,
// Android and Telegram Desktop, and get cut off in narrow columns.)
const model = defineModel<string>({ required: true }) // YYYY-MM-DD
defineProps<{ label: string; min?: string; required?: boolean }>()
const uid = useId()

const shown = computed(() => {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(model.value ?? '')
  if (!m) return null
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return {
    date: d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' }),
    weekday: d.toLocaleDateString('en-GB', { weekday: 'long' }),
  }
})
// Some browsers (Telegram Desktop) open the picker only from its icon: ask for it.
function openPicker(e: MouseEvent) {
  try {
    (e.target as HTMLInputElement).showPicker?.()
  } catch { /* not allowed here: the browser opens its own */ }
}
</script>

<template>
  <div class="min-w-0">
    <label :for="uid" class="block text-[13px] text-hint">{{ label }}</label>
    <div class="relative mt-1">
      <input
        :id="uid" v-model="model" type="date" :min="min" :required="required"
        class="peer absolute inset-0 z-10 h-full w-full cursor-pointer opacity-0"
        @click="openPicker"
      />
      <div
        class="flex min-h-[52px] items-center gap-2 rounded-xl border border-separator bg-surface px-3 py-1.5 transition peer-focus-visible:border-primary peer-active:bg-page"
        aria-hidden="true"
      >
        <AppIcon name="calendar" class="hidden size-5 shrink-0 text-hint min-[360px]:block" />
        <span class="min-w-0">
          <span class="block truncate text-[15px] font-medium tabular-nums" :class="!shown && 'text-hint'">{{ shown?.date ?? 'Choose a date' }}</span>
          <span v-if="shown" class="block truncate text-[12px] text-hint">{{ shown.weekday }}</span>
        </span>
      </div>
    </div>
  </div>
</template>
