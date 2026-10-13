<script setup lang="ts">
// Dropdown with a search box (after BubbleWhite's SearchableSelect) for the panel's
// filters and forms. Values are strings; "" means nothing chosen. `emptyLabel` adds a
// top option for "" (e.g. "All branches"). The search box appears for longer lists.
import type { SelectOption } from '~/utils/options'

const props = withDefaults(
  defineProps<{
    options: SelectOption[]
    id?: string
    label?: string // accessible name when there is no <label for>
    placeholder?: string
    emptyLabel?: string
    searchable?: boolean
    clearable?: boolean
    required?: boolean
    disabled?: boolean
  }>(),
  { placeholder: 'Choose…', searchable: undefined, clearable: undefined },
)
const model = defineModel<string>({ required: true })

const uid = useId()
const listId = `${uid}-list`
const open = ref(false)
const query = ref('')
const active = ref(-1) // index into `items`
const root = ref<HTMLElement>()
const trigger = ref<HTMLButtonElement>()
const panel = ref<HTMLElement>()
const search = ref<HTMLInputElement>()
const list = ref<HTMLElement>()
const panelStyle = ref<Record<string, string>>({})
// Inside a modal <dialog> (top layer, rest of the page inert) the menu stays in the
// dialog (rendered in place, still position: fixed); elsewhere it goes to <body> so no
// overflow or stacking context clips it. Decided once, before the menu ever opens.
const inDialog = ref(false)

const showSearch = computed(() => props.searchable ?? props.options.length > 7)
const canClear = computed(() => (props.clearable ?? props.emptyLabel !== undefined) && model.value !== '' && !props.disabled)
const selected = computed(() => props.options.find(o => o.value === model.value))
const display = computed(() => selected.value?.label ?? (model.value === '' ? props.emptyLabel : undefined))

const items = computed<SelectOption[]>(() => {
  const all = props.emptyLabel !== undefined ? [{ value: '', label: props.emptyLabel }, ...props.options] : props.options
  const q = query.value.trim().toLowerCase()
  if (!q) return all
  return all.filter(o => o.value !== '' && `${o.label} ${o.sub ?? ''}`.toLowerCase().includes(q))
})

function place() {
  const r = trigger.value?.getBoundingClientRect()
  if (!r) return
  const width = Math.max(r.width, 240)
  const left = Math.max(8, Math.min(r.left, window.innerWidth - width - 8))
  const below = window.innerHeight - r.bottom
  const up = below < 300 && r.top > below
  panelStyle.value = {
    width: `${width}px`,
    left: `${left}px`,
    ...(up ? { bottom: `${window.innerHeight - r.top + 4}px` } : { top: `${r.bottom + 4}px` }),
    maxHeight: `${Math.max(160, (up ? r.top : below) - 16)}px`,
  }
}

async function show(initial = '') {
  if (props.disabled || open.value) return
  query.value = initial
  place()
  open.value = true
  active.value = Math.max(0, items.value.findIndex(o => o.value === model.value))
  await nextTick()
  if (showSearch.value) search.value?.focus()
  else list.value?.focus()
  scrollToActive()
}

function hide(refocus = true) {
  if (!open.value) return
  open.value = false
  query.value = ''
  if (refocus) trigger.value?.focus()
}

function pick(o: SelectOption) {
  if (o.disabled) return
  model.value = o.value
  hide()
}

function move(delta: number) {
  const n = items.value.length
  if (!n) return
  let i = active.value
  for (let step = 0; step < n; step++) {
    i = (i + delta + n) % n
    if (!items.value[i]?.disabled) break
  }
  active.value = i
  scrollToActive()
}

function scrollToActive() {
  nextTick(() => list.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest' }))
}

function onTriggerKey(e: KeyboardEvent) {
  if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) {
    e.preventDefault()
    show()
  } else if (showSearch.value && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
    e.preventDefault()
    show(e.key) // start typing to search
  }
}

function onPanelKey(e: KeyboardEvent) {
  switch (e.key) {
    case 'ArrowDown': e.preventDefault(); move(1); break
    case 'ArrowUp': e.preventDefault(); move(-1); break
    case 'Home': if (!showSearch.value) { e.preventDefault(); active.value = -1; move(1) } break
    case 'End': if (!showSearch.value) { e.preventDefault(); active.value = 0; move(-1) } break
    case 'Enter': {
      e.preventDefault()
      const o = items.value[active.value]
      if (o) pick(o)
      break
    }
    case 'Escape': e.preventDefault(); hide(); break
    case 'Tab': hide(false); break
  }
}

watch(query, () => {
  active.value = items.value.findIndex(o => !o.disabled)
})

function onOutside(e: Event) {
  const t = e.target as Node
  if (open.value && !root.value?.contains(t) && !panel.value?.contains(t)) hide(false)
}
function onViewport(e: Event) {
  if (open.value && !(e.target instanceof Node && panel.value?.contains(e.target))) place()
}
onMounted(() => {
  inDialog.value = !!root.value?.closest('dialog')
  document.addEventListener('pointerdown', onOutside)
  window.addEventListener('resize', onViewport)
  window.addEventListener('scroll', onViewport, true)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onOutside)
  window.removeEventListener('resize', onViewport)
  window.removeEventListener('scroll', onViewport, true)
})
</script>

<template>
  <div ref="root" class="relative">
    <button
      :id="id"
      ref="trigger"
      type="button"
      role="combobox"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-required="required || undefined"
      :aria-label="label"
      :disabled="disabled"
      class="input flex items-center text-left"
      :class="[open && 'border-primary ring-2 ring-primary/20', canClear ? 'pr-14' : 'pr-9']"
      @click="open ? hide() : show()"
      @keydown="onTriggerKey"
    >
      <span class="min-w-0 flex-1 truncate" :class="display === undefined && 'text-muted'">
        {{ display ?? placeholder }}
        <span v-if="selected?.sub" class="text-muted"> · {{ selected.sub }}</span>
      </span>
    </button>
    <button
      v-if="canClear"
      type="button"
      class="absolute right-8 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded-md text-muted hover:bg-surface-2 hover:text-fg"
      aria-label="Clear"
      @click="model = ''"
    >
      <svg viewBox="0 0 24 24" class="size-3.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12" /></svg>
    </button>
    <svg
      viewBox="0 0 24 24"
      class="pointer-events-none absolute right-3 top-1/2 size-4 -translate-y-1/2 text-muted transition-transform"
      :class="open && 'rotate-180'"
      fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
    ><path d="m6 9 6 6 6-6" /></svg>
    <!-- keeps native "required" validation working for the form -->
    <input
      v-if="required"
      tabindex="-1"
      aria-hidden="true"
      class="pointer-events-none absolute bottom-0 left-4 h-px w-px opacity-0"
      :value="model"
      required
      @focus="trigger?.focus()"
    />

    <Teleport to="body" :disabled="inDialog">
      <Transition
        enter-active-class="transition duration-100 ease-out" enter-from-class="opacity-0 -translate-y-1"
        leave-active-class="transition duration-75 ease-in" leave-to-class="opacity-0"
      >
        <div
          v-if="open"
          ref="panel"
          class="fixed z-[60] flex flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-xl"
          :style="panelStyle"
          @keydown="onPanelKey"
        >
          <div v-if="showSearch" class="border-b border-border p-2">
            <div class="flex items-center gap-2 rounded-lg bg-surface-2 px-2.5 py-1.5">
              <svg viewBox="0 0 24 24" class="size-4 shrink-0 text-muted" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="m21 21-4.3-4.3M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14z" /></svg>
              <input
                ref="search"
                v-model="query"
                type="text"
                class="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-muted"
                placeholder="Search…"
                role="searchbox"
                aria-label="Search options"
                :aria-controls="listId"
                :aria-activedescendant="active >= 0 ? `${uid}-opt-${active}` : undefined"
                autocomplete="off"
              />
              <button v-if="query" type="button" class="text-muted hover:text-fg" aria-label="Clear search" @click="query = ''; search?.focus()">
                <svg viewBox="0 0 24 24" class="size-3.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12" /></svg>
              </button>
            </div>
          </div>
          <ul
            :id="listId"
            ref="list"
            role="listbox"
            :tabindex="showSearch ? -1 : 0"
            :aria-activedescendant="!showSearch && active >= 0 ? `${uid}-opt-${active}` : undefined"
            class="min-h-0 flex-1 overflow-y-auto py-1 outline-none"
          >
            <li v-if="!items.length" class="px-3 py-6 text-center text-sm text-muted">No matches</li>
            <li
              v-for="(o, i) in items"
              :id="`${uid}-opt-${i}`"
              :key="o.value"
              :data-index="i"
              role="option"
              :aria-selected="o.value === model"
              :aria-disabled="o.disabled || undefined"
              class="mx-1 flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-2 text-sm"
              :class="[
                i === active && 'bg-surface-2',
                o.value === model ? 'font-medium text-primary' : o.value === '' ? 'text-muted' : 'text-fg',
                o.disabled && 'cursor-not-allowed opacity-50',
              ]"
              @pointermove="active = i"
              @click="pick(o)"
            >
              <svg viewBox="0 0 24 24" class="size-4 shrink-0" :class="o.value === model ? 'opacity-100' : 'opacity-0'" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5" /></svg>
              <span class="min-w-0 flex-1 truncate">{{ o.label }}</span>
              <span v-if="o.sub" class="shrink-0 truncate text-xs text-muted">{{ o.sub }}</span>
            </li>
          </ul>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
