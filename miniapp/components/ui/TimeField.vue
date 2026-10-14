<script setup lang="ts">
// A time field that looks the same on every phone (like DateField): the time in our
// own box, over the phone's own time input, which opens its picker when tapped.
const model = defineModel<string>({ required: true }) // HH:MM
defineProps<{ label: string; note?: string; required?: boolean }>()
const uid = useId()
function openPicker(e: MouseEvent) {
  try {
    (e.target as HTMLInputElement).showPicker?.()
  } catch { /* the browser opens its own */ }
}
</script>

<template>
  <div class="min-w-0">
    <label :for="uid" class="block text-[13px] text-hint">{{ label }}</label>
    <div class="relative mt-1">
      <input
        :id="uid" v-model="model" type="time" :required="required" step="300"
        class="peer absolute inset-0 z-10 h-full w-full cursor-pointer opacity-0"
        @click="openPicker"
      />
      <div
        class="flex min-h-[52px] items-center gap-2 rounded-xl border border-separator bg-surface px-3 py-1.5 transition peer-focus-visible:border-primary peer-active:bg-page"
        aria-hidden="true"
      >
        <AppIcon name="clock" class="hidden size-5 shrink-0 text-hint min-[420px]:block" />
        <span class="min-w-0">
          <span class="block text-[17px] font-semibold tabular-nums" :class="!model && 'text-hint'">{{ model || '--:--' }}</span>
          <span v-if="note" class="block truncate text-[12px] text-hint">{{ note }}</span>
        </span>
      </div>
    </div>
  </div>
</template>
