<script setup lang="ts">
import { pageSizes } from '~/composables/usePaged'

// Under every list: how many there are, rows per page (10 by default) and page moves.
const props = withDefaults(defineProps<{ page: number; pageSize: number; total: number; noun?: string }>(), { noun: 'record' })
defineEmits<{ go: [page: number]; size: [size: number] }>()
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const from = computed(() => (props.total ? (props.page - 1) * props.pageSize + 1 : 0))
const to = computed(() => Math.min(props.total, props.page * props.pageSize))
const uid = useId()
// The same picker as every other select in the panel.
const sizeOptions = pageSizes.map(n => ({ value: String(n), label: String(n) }))
const plural = computed(() => props.total === 1 ? props.noun
  : props.noun.endsWith('y') ? `${props.noun.slice(0, -1)}ies` : /(s|x|ch|sh)$/.test(props.noun) ? `${props.noun}es` : `${props.noun}s`)
</script>

<template>
  <div class="flex flex-wrap items-center justify-between gap-3 border-t border-border px-3 py-2.5 text-sm text-muted">
    <span class="tabular-nums">
      <template v-if="total">{{ from }}–{{ to }} of </template>{{ total }} {{ plural }}
    </span>
    <div class="flex flex-wrap items-center gap-3">
      <div class="flex items-center gap-2">
        <span>Rows</span>
        <SearchSelect
          :id="`${uid}-size`" :model-value="String(pageSize)" :options="sizeOptions" label="Rows per page" class="w-24"
          @update:model-value="v => $emit('size', Number(v))"
        />
      </div>
      <div class="flex items-center gap-1">
        <IconAction kind="first" label="First page" :disabled="page <= 1" @click="$emit('go', 1)" />
        <IconAction kind="previous" label="Previous page" :disabled="page <= 1" @click="$emit('go', page - 1)" />
        <span class="px-1.5 tabular-nums">{{ page }} / {{ pages }}</span>
        <IconAction kind="next" label="Next page" :disabled="page >= pages" @click="$emit('go', page + 1)" />
        <IconAction kind="last" label="Last page" :disabled="page >= pages" @click="$emit('go', pages)" />
      </div>
    </div>
  </div>
</template>
