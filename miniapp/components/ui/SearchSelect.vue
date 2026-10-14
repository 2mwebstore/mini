<script setup lang="ts">
// Picker with a search box, opened as a bottom sheet (the Mini App's equivalent of
// the admin panel's SearchSelect). Values are strings; "" means nothing chosen.
// Each option may carry a colour dot, a second line and a hint on the right
// (e.g. "5 days left"). Every list has a search box (searchable=false hides it).
export interface PickOption {
  value: string
  label: string
  sub?: string // second line, also searched
  hint?: string // right-hand note
  hintTone?: 'hint' | 'warning' | 'success'
  color?: string // dot before the label
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    options: PickOption[]
    title: string // sheet title, and the accessible name of the trigger
    placeholder?: string
    searchable?: boolean
    disabled?: boolean
  }>(),
  { placeholder: 'Choose…', searchable: undefined },
)
const model = defineModel<string>({ required: true })

const open = ref(false)
const query = ref('')
const search = ref<HTMLInputElement>()
const showSearch = computed(() => props.searchable ?? true)
const selected = computed(() => props.options.find(o => o.value === model.value))
const items = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? props.options.filter(o => `${o.label} ${o.sub ?? ''}`.toLowerCase().includes(q)) : props.options
})
const tones = { hint: 'text-hint', warning: 'text-warning', success: 'text-success' } as const

function show() {
  if (props.disabled) return
  query.value = ''
  open.value = true
}
function pick(o: PickOption) {
  if (o.disabled) return
  model.value = o.value
  webApp()?.HapticFeedback?.selectionChanged()
  open.value = false
}
</script>

<template>
  <button
    type="button"
    class="flex w-full items-center gap-3 rounded-xl border border-separator px-3 py-2.5 text-left transition active:bg-page disabled:opacity-50"
    :aria-label="`${title}: ${selected?.label ?? placeholder}`"
    aria-haspopup="dialog"
    :aria-expanded="open"
    :disabled="disabled"
    @click="show"
  >
    <span v-if="selected?.color" class="size-3 shrink-0 rounded-full" :style="{ background: selected.color }" aria-hidden="true" />
    <span class="min-w-0 flex-1">
      <span class="block truncate text-[15px]" :class="selected ? 'font-medium' : 'text-hint'">{{ selected?.label ?? placeholder }}</span>
      <span v-if="selected?.sub" class="block truncate text-[13px] text-hint">{{ selected.sub }}</span>
    </span>
    <span v-if="selected?.hint" class="shrink-0 text-[13px]" :class="tones[selected.hintTone ?? 'hint']">{{ selected.hint }}</span>
    <AppIcon name="chevron" class="size-5 shrink-0 rotate-90 text-hint" />
  </button>

  <BottomSheet v-model="open" :title="title">
    <div v-if="showSearch" class="sticky -top-4 z-10 -mx-4 -mt-1 mb-2 bg-surface px-4 pb-2 pt-1">
      <label class="flex items-center gap-2 rounded-xl bg-page px-3 py-2">
        <AppIcon name="search" class="size-4 shrink-0 text-hint" />
        <input
          ref="search"
          v-model="query"
          type="search"
          class="min-w-0 flex-1 bg-transparent text-[15px] outline-none placeholder:text-hint"
          placeholder="Search"
          aria-label="Search"
          autocomplete="off"
        />
        <button v-if="query" type="button" class="text-hint" aria-label="Clear search" @click="query = ''; search?.focus()">
          <AppIcon name="close" class="size-4" />
        </button>
      </label>
    </div>
    <ul role="listbox" :aria-label="title" class="-mx-2 flex flex-col">
      <li v-if="!items.length" class="px-2 py-8 text-center text-[15px] text-hint">No matches</li>
      <li
        v-for="o in items"
        :key="o.value"
        role="option"
        :aria-selected="o.value === model"
        :aria-disabled="o.disabled || undefined"
        tabindex="0"
        class="flex cursor-pointer items-center gap-3 rounded-xl px-2 py-3 active:bg-page"
        :class="o.disabled && 'cursor-not-allowed opacity-50'"
        @click="pick(o)"
        @keydown.enter.prevent="pick(o)"
        @keydown.space.prevent="pick(o)"
      >
        <span v-if="o.color" class="size-3 shrink-0 rounded-full" :style="{ background: o.color }" aria-hidden="true" />
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[15px]" :class="o.value === model && 'font-semibold'">{{ o.label }}</span>
          <span v-if="o.sub" class="block truncate text-[13px] text-hint">{{ o.sub }}</span>
        </span>
        <span v-if="o.hint" class="shrink-0 text-[13px]" :class="tones[o.hintTone ?? 'hint']">{{ o.hint }}</span>
        <AppIcon name="check" class="size-5 shrink-0 text-accent" :class="o.value === model ? 'opacity-100' : 'opacity-0'" />
      </li>
    </ul>
  </BottomSheet>
</template>
