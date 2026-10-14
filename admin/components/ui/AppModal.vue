<script setup lang="ts">
const open = defineModel<boolean>({ required: true })
const props = defineProps<{ title: string; wide?: boolean; xwide?: boolean }>()

const dialog = ref<HTMLDialogElement>()
watch(open, v => (v ? dialog.value?.showModal() : dialog.value?.close()), { flush: 'post' })
onMounted(() => open.value && dialog.value?.showModal())
</script>

<template>
  <!-- Native <dialog>: focus trap, Esc to close and inert background for free. -->
  <dialog
    ref="dialog"
    class="m-auto w-[calc(100%-2rem)] rounded-xl border border-border bg-surface p-0 text-fg shadow-xl backdrop:bg-black/40"
    :class="props.xwide ? 'max-w-4xl' : props.wide ? 'max-w-2xl' : 'max-w-md'"
    :aria-label="title"
    @close="open = false"
  >
    <div v-if="open" class="p-5">
      <div class="mb-4 flex items-start justify-between gap-4">
        <h2 class="text-base font-semibold">{{ title }}</h2>
        <button type="button" class="rounded-md p-1 text-muted hover:bg-surface-2 hover:text-fg" aria-label="Close" @click="open = false">
          <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg>
        </button>
      </div>
      <slot />
      <div v-if="$slots.footer" class="mt-5 flex justify-end gap-2"><slot name="footer" /></div>
    </div>
  </dialog>
</template>
