<script setup lang="ts">
// Reason picker for rejecting attendance (reason required) or leave (note optional).
const open = defineModel<boolean>({ required: true })
const props = defineProps<{ title: string; presets?: string[]; required?: boolean; loading?: boolean }>()
const emit = defineEmits<{ submit: [reason: string] }>()

const reason = ref('')
watch(open, v => v && (reason.value = ''))
const valid = computed(() => !props.required || reason.value.trim().length > 0)
</script>

<template>
  <AppModal v-model="open" :title="title">
    <div v-if="presets?.length" class="mb-3 flex flex-wrap gap-2">
      <button
        v-for="p in presets"
        :key="p"
        type="button"
        class="rounded-full border border-border px-3 py-1 text-xs hover:bg-surface-2"
        :class="{ 'border-primary bg-primary/10 text-primary': reason === p }"
        @click="reason = p"
      >{{ p }}</button>
    </div>
    <FormField :label="required ? 'Reason (shown to the employee)' : 'Note (optional, shown to the employee)'" for="reject-reason">
      <textarea id="reject-reason" v-model="reason" class="input" rows="3" maxlength="500" />
    </FormField>
    <template #footer>
      <AppButton variant="secondary" @click="open = false">Cancel</AppButton>
      <AppButton variant="danger" :disabled="!valid" :loading="loading" @click="emit('submit', reason.trim())">Reject</AppButton>
    </template>
  </AppModal>
</template>
