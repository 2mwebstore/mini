<script setup lang="ts">
const props = defineProps<{ page: number; pageSize: number; total: number }>()
defineEmits<{ go: [page: number] }>()
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
</script>

<template>
  <div class="flex items-center justify-between gap-3 px-3 py-3 text-sm text-muted">
    <span>{{ total }} record{{ total === 1 ? '' : 's' }}</span>
    <div class="flex items-center gap-2">
      <AppButton size="sm" variant="secondary" :disabled="page <= 1" @click="$emit('go', page - 1)">Previous</AppButton>
      <span class="tabular-nums">Page {{ page }} of {{ pages }}</span>
      <AppButton size="sm" variant="secondary" :disabled="page >= pages" @click="$emit('go', page + 1)">Next</AppButton>
    </div>
  </div>
</template>
