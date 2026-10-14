<script setup lang="ts">
// Picker with a search box, opened as a bottom sheet (the Mini App's equivalent of
// the admin panel's SearchSelect). Values are strings; "" means nothing chosen.
// Each option is a card: an icon tile in its colour, a second line and a pill on
// the right (e.g. "5 days left"). Every list has a search box (searchable=false hides it).
import type { IconName } from '~/utils/icons'

export interface PickOption {
  value: string
  label: string
  sub?: string // second line, also searched
  hint?: string // pill on the right
  hintTone?: 'hint' | 'warning' | 'success'
  color?: string // #rrggbb: the icon tile's colour
  icon?: IconName // in the tile (default: a dot)
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
const textTones = { hint: 'text-hint', warning: 'text-warning', success: 'text-success' } as const
const tones = { hint: 'bg-page text-hint', warning: 'bg-warning/10 text-warning', success: 'bg-success/10 text-success' } as const
// A soft tile in the option's colour (12% of it), its icon in the full colour.
const tile = (c?: string) => (c && /^#[0-9a-f]{6}$/i.test(c) ? { background: `${c}1f`, color: c } : undefined)

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
    <span v-if="selected?.color" class="flex size-9 shrink-0 items-center justify-center rounded-xl" :style="tile(selected.color)" aria-hidden="true">
      <AppIcon v-if="selected.icon" :name="selected.icon" class="size-5" />
      <span v-else class="size-2.5 rounded-full bg-current" />
    </span>
    <!-- The name gets the whole width; its hint (“22 days left”) goes on the second line. -->
    <span class="min-w-0 flex-1">
      <span class="block truncate text-[15px]" :class="selected ? 'font-medium' : 'text-hint'">{{ selected?.label ?? placeholder }}</span>
      <span v-if="selected?.hint || selected?.sub" class="block truncate text-[13px] text-hint">
        <span v-if="selected?.hint" :class="textTones[selected.hintTone ?? 'hint']">{{ selected.hint }}</span><template v-if="selected?.hint && selected?.sub"> · </template>{{ selected?.sub }}
      </span>
    </span>
    <AppIcon name="chevron" class="size-5 shrink-0 rotate-90 text-hint" />
  </button>

  <BottomSheet v-model="open" :title="title">
    <div v-if="showSearch" class="sticky -top-4 z-10 -mx-4 -mt-1 mb-2 bg-surface px-4 pb-2 pt-1">
      <label class="flex items-center gap-2 rounded-xl border border-separator bg-page px-3 py-2.5 focus-within:border-primary">
        <AppIcon name="search" class="size-[18px] shrink-0 text-hint" />
        <!-- appearance-none: no browser box drawn inside the field -->
        <input
          ref="search"
          v-model="query"
          type="search"
          class="min-w-0 flex-1 appearance-none border-0 bg-transparent p-0 text-[15px] text-fg outline-none placeholder:text-hint [&::-webkit-search-cancel-button]:hidden"
          placeholder="Search"
          aria-label="Search"
          autocomplete="off"
        />
        <button v-if="query" type="button" class="text-hint" aria-label="Clear search" @click="query = ''; search?.focus()">
          <AppIcon name="close" class="size-4" />
        </button>
      </label>
    </div>
    <ul role="listbox" :aria-label="title" class="flex flex-col gap-2 pb-1">
      <li v-if="!items.length" class="px-2 py-8 text-center text-[15px] text-hint">No matches</li>
      <li
        v-for="o in items"
        :key="o.value"
        role="option"
        :aria-selected="o.value === model"
        :aria-disabled="o.disabled || undefined"
        tabindex="0"
        class="flex cursor-pointer items-center gap-3 rounded-2xl border px-3 py-3 transition active:scale-[0.99]"
        :class="[o.value === model ? 'border-primary bg-primary/5' : 'border-separator active:bg-page', o.disabled && 'cursor-not-allowed opacity-50']"
        @click="pick(o)"
        @keydown.enter.prevent="pick(o)"
        @keydown.space.prevent="pick(o)"
      >
        <span v-if="o.color" class="flex size-10 shrink-0 items-center justify-center rounded-xl" :style="tile(o.color)" aria-hidden="true">
          <AppIcon v-if="o.icon" :name="o.icon" class="size-5" />
          <span v-else class="size-3 rounded-full bg-current" />
        </span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[15px] font-semibold">{{ o.label }}</span>
          <span v-if="o.sub" class="mt-0.5 line-clamp-2 text-[13px] leading-snug text-hint">{{ o.sub }}</span>
          <span v-if="o.hint" class="mt-1.5 inline-block rounded-full px-2 py-0.5 text-[12px] font-medium" :class="tones[o.hintTone ?? 'hint']">{{ o.hint }}</span>
        </span>
        <!-- radio: filled with a tick when chosen -->
        <span
          class="flex size-6 shrink-0 items-center justify-center rounded-full border-2 transition"
          :class="o.value === model ? 'border-primary bg-primary text-primary-fg' : 'border-separator'"
          aria-hidden="true"
        >
          <AppIcon v-if="o.value === model" name="check" class="size-3.5" />
        </span>
      </li>
    </ul>
  </BottomSheet>
</template>
