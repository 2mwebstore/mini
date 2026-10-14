<script setup lang="ts">
import { pageSizes } from '~/composables/usePaged'

// Under every list: how many there are, rows per page (10 by default) and page moves.
const props = withDefaults(defineProps<{ page: number; pageSize: number; total: number; noun?: string }>(), { noun: 'record' })
defineEmits<{ go: [page: number]; size: [size: number] }>()
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const from = computed(() => (props.total ? (props.page - 1) * props.pageSize + 1 : 0))
const to = computed(() => Math.min(props.total, props.page * props.pageSize))
const uid = useId()
const plural = computed(() => props.total === 1 ? props.noun
  : props.noun.endsWith('y') ? `${props.noun.slice(0, -1)}ies` : /(s|x|ch|sh)$/.test(props.noun) ? `${props.noun}es` : `${props.noun}s`)
</script>

<template>
  <div class="flex flex-wrap items-center justify-between gap-3 border-t border-border px-3 py-2.5 text-sm text-muted">
    <span class="tabular-nums">
      <template v-if="total">{{ from }}–{{ to }} of </template>{{ total }} {{ plural }}
    </span>
    <div class="flex flex-wrap items-center gap-3">
      <label class="flex items-center gap-2" :for="`${uid}-size`">
        Rows
        <select
          :id="`${uid}-size`" :value="pageSize" class="rounded-md border border-border bg-surface px-2 py-1 text-sm text-fg"
          @change="$emit('size', Number(($event.target as HTMLSelectElement).value))"
        >
          <option v-for="n in pageSizes" :key="n" :value="n">{{ n }}</option>
        </select>
      </label>
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
