<script setup lang="ts">
// Several values from a searchable list (SearchSelect's sibling). The menu stays
// open while ticking; "Select all shown" ticks every match of the search, so
// typing a department or branch name (searched in `sub`) picks that whole group.
import type { SelectOption } from '~/utils/options'

const props = withDefaults(
  defineProps<{
    options: SelectOption[]
    id?: string
    label?: string // accessible name when there is no <label for>
    placeholder?: string
    noun?: string // "employee" -> "3 employees"
    searchPlaceholder?: string
    required?: boolean
    disabled?: boolean
  }>(),
  { placeholder: 'Choose…', noun: 'item', searchPlaceholder: 'Search…' },
)
const model = defineModel<string[]>({ required: true })

const uid = useId()
const listId = `${uid}-list`
const open = ref(false)
const query = ref('')
const active = ref(0)
const root = ref<HTMLElement>()
const trigger = ref<HTMLButtonElement>()
const panel = ref<HTMLElement>()
const search = ref<HTMLInputElement>()
const list = ref<HTMLElement>()
const panelStyle = ref<Record<string, string>>({})
const inDialog = ref(false)

const chosen = computed(() => new Set(model.value))
const picked = computed(() => props.options.filter(o => chosen.value.has(o.value)))
const items = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? props.options.filter(o => `${o.label} ${o.sub ?? ''}`.toLowerCase().includes(q)) : props.options
})
const shownFree = computed(() => items.value.filter(o => !o.disabled))
const allShown = computed(() => shownFree.value.length > 0 && shownFree.value.every(o => chosen.value.has(o.value)))
const plural = (n: number) => `${n} ${props.noun}${n === 1 ? '' : /(s|x|ch|sh)$/.test(props.noun) ? 'es' : 's'}`

function place() {
  const r = trigger.value?.getBoundingClientRect()
  if (!r) return
  const width = Math.max(r.width, 280)
  const left = Math.max(8, Math.min(r.left, window.innerWidth - width - 8))
  const below = window.innerHeight - r.bottom
  const up = below < 320 && r.top > below
  panelStyle.value = {
    width: `${width}px`,
    left: `${left}px`,
    ...(up ? { bottom: `${window.innerHeight - r.top + 4}px` } : { top: `${r.bottom + 4}px` }),
    maxHeight: `${Math.max(200, (up ? r.top : below) - 16)}px`,
  }
}
async function show() {
  if (props.disabled || open.value) return
  query.value = ''
  place()
  open.value = true
  active.value = 0
  await nextTick()
  search.value?.focus()
}
function hide(refocus = true) {
  if (!open.value) return
  open.value = false
  if (refocus) trigger.value?.focus()
}
function toggle(o: SelectOption) {
  if (o.disabled) return
  model.value = chosen.value.has(o.value) ? model.value.filter(v => v !== o.value) : [...model.value, o.value]
}
function toggleShown() {
  const shown = new Set(shownFree.value.map(o => o.value))
  model.value = allShown.value ? model.value.filter(v => !shown.has(v)) : [...new Set([...model.value, ...shown])]
}
function move(delta: number) {
  const n = items.value.length
  if (!n) return
  active.value = (active.value + delta + n) % n
  nextTick(() => list.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest' }))
}
function onPanelKey(e: KeyboardEvent) {
  switch (e.key) {
    case 'ArrowDown': e.preventDefault(); move(1); break
    case 'ArrowUp': e.preventDefault(); move(-1); break
    case 'Enter': {
      e.preventDefault()
      const o = items.value[active.value]
      if (o) toggle(o)
      break
    }
    case 'Escape': e.preventDefault(); hide(); break
    case 'Tab': hide(false); break
  }
}
watch(query, () => (active.value = 0))

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
      :aria-label="label"
      :disabled="disabled"
      class="input flex min-h-10 flex-wrap items-center gap-1 pr-9 text-left"
      :class="open && 'border-primary ring-2 ring-primary/20'"
      @click="open ? hide() : show()"
      @keydown.down.prevent="show()"
      @keydown.enter.prevent="show()"
    >
      <span v-if="!picked.length" class="text-muted">{{ placeholder }}</span>
      <template v-else>
        <span v-for="o in picked.slice(0, 4)" :key="o.value" class="max-w-[10rem] truncate rounded-md bg-primary/10 px-1.5 py-0.5 text-xs font-medium text-primary">{{ o.label }}</span>
        <span v-if="picked.length > 4" class="text-xs text-muted">+{{ picked.length - 4 }} more</span>
      </template>
    </button>
    <svg
      viewBox="0 0 24 24"
      class="pointer-events-none absolute right-3 top-3 size-4 text-muted transition-transform"
      :class="open && 'rotate-180'"
      fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
    ><path d="m6 9 6 6 6-6" /></svg>
    <input
      v-if="required"
      tabindex="-1"
      aria-hidden="true"
      class="pointer-events-none absolute bottom-0 left-4 h-px w-px opacity-0"
      :value="model.join(',')"
      required
      @focus="trigger?.focus()"
    />

    <Teleport to="body" :disabled="inDialog">
      <div
        v-if="open"
        ref="panel"
        class="fixed z-[60] flex flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-xl"
        :style="panelStyle"
        @keydown="onPanelKey"
      >
        <div class="border-b border-border p-2">
          <div class="flex items-center gap-2 rounded-lg bg-surface-2 px-2.5 py-1.5">
            <svg viewBox="0 0 24 24" class="size-4 shrink-0 text-muted" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="m21 21-4.3-4.3M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14z" /></svg>
            <input
              ref="search"
              v-model="query"
              type="text"
              class="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-muted"
              :placeholder="searchPlaceholder"
              role="searchbox"
              aria-label="Search options"
              :aria-controls="listId"
              :aria-activedescendant="items.length ? `${uid}-opt-${active}` : undefined"
              autocomplete="off"
            />
          </div>
          <div class="mt-2 flex items-center justify-between gap-2 px-1 text-xs">
            <button type="button" class="font-medium text-primary hover:underline disabled:opacity-50" :disabled="!shownFree.length" @click="toggleShown">
              {{ allShown ? 'Untick' : 'Tick' }} {{ query ? 'all matches' : 'all' }} ({{ shownFree.length }})
            </button>
            <span class="text-muted">{{ plural(model.length) }} chosen</span>
            <button v-if="model.length" type="button" class="text-muted hover:text-fg" @click="model = []">Clear</button>
          </div>
        </div>
        <ul :id="listId" ref="list" role="listbox" aria-multiselectable="true" class="min-h-0 flex-1 overflow-y-auto py-1">
          <li v-if="!items.length" class="px-3 py-6 text-center text-sm text-muted">No matches</li>
          <li
            v-for="(o, i) in items"
            :id="`${uid}-opt-${i}`"
            :key="o.value"
            :data-index="i"
            role="option"
            :aria-selected="chosen.has(o.value)"
            :aria-disabled="o.disabled || undefined"
            class="mx-1 flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm"
            :class="[i === active && 'bg-surface-2', o.disabled && 'cursor-not-allowed opacity-50']"
            @pointermove="active = i"
            @click="toggle(o)"
          >
            <span
              class="flex size-4 shrink-0 items-center justify-center rounded border"
              :class="chosen.has(o.value) ? 'border-primary bg-primary text-white' : 'border-border'"
              aria-hidden="true"
            >
              <svg v-if="chosen.has(o.value)" viewBox="0 0 24 24" class="size-3" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
            </span>
            <span class="min-w-0 flex-1 truncate">{{ o.label }}</span>
            <span v-if="o.sub" class="max-w-[50%] shrink-0 truncate text-xs text-muted">{{ o.sub }}</span>
          </li>
        </ul>
        <div class="border-t border-border p-2 text-right">
          <AppButton size="sm" @click="hide()">Done</AppButton>
        </div>
      </div>
    </Teleport>
  </div>
</template>
