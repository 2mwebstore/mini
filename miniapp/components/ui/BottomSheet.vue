<script setup lang="ts">
const open = defineModel<boolean>({ required: true })
defineProps<{ title: string }>()
</script>

<template>
  <Teleport to="body">
    <Transition enter-from-class="opacity-0" leave-to-class="opacity-0" enter-active-class="transition" leave-active-class="transition">
      <div v-if="open" class="fixed inset-0 z-40 bg-black/40" @click="open = false" />
    </Transition>
    <Transition enter-from-class="translate-y-full" leave-to-class="translate-y-full" enter-active-class="transition duration-200" leave-active-class="transition duration-200">
      <div
        v-if="open"
        class="fixed inset-x-0 bottom-0 z-50 max-h-[85vh] overflow-y-auto rounded-t-2xl bg-surface p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
      >
        <div class="mx-auto mb-3 h-1 w-10 rounded-full bg-hint/40" />
        <h2 class="mb-3 text-lg font-semibold">{{ title }}</h2>
        <slot />
      </div>
    </Transition>
  </Teleport>
</template>
