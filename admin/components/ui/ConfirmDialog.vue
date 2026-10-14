<script setup lang="ts">
// The panel's confirmation dialog (delete, deactivate, overwrite…). Mounted once in
// app.vue and driven by useConfirm(); see composables/useConfirm.ts.
const { state, answer } = useConfirmState()

const open = computed({ get: () => state.open, set: v => !v && answer(false) })
const o = computed(() => state.options)
const reason = ref('')
watch(() => state.open, v => v && (reason.value = ''))
const danger = computed(() => (o.value?.tone ?? 'danger') === 'danger')
const needsReason = computed(() => !!o.value?.reason?.required)
const valid = computed(() => !needsReason.value || reason.value.trim().length > 0)

function ok() {
  if (valid.value) answer({ reason: reason.value.trim() })
}
</script>

<template>
  <AppModal v-model="open" :title="o?.title ?? 'Are you sure?'">
    <form v-if="o" id="confirm-form" class="flex gap-3" @submit.prevent="ok">
      <span
        class="flex size-10 shrink-0 items-center justify-center rounded-full"
        :class="danger ? 'bg-danger/10 text-danger' : 'bg-primary/10 text-primary'"
        aria-hidden="true"
      >
        <svg v-if="danger" viewBox="0 0 24 24" class="size-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0-1 14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2L4 6M10 11v6M14 11v6" />
        </svg>
        <svg v-else viewBox="0 0 24 24" class="size-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
        </svg>
      </span>
      <div class="min-w-0 flex-1">
        <p v-if="o.message" class="text-sm">{{ o.message }}</p>
        <dl v-if="o.details?.length" class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-lg bg-surface-2 px-3 py-2 text-sm">
          <template v-for="d in o.details" :key="d.label">
            <dt class="text-muted">{{ d.label }}</dt>
            <dd class="min-w-0 truncate font-medium">{{ d.value }}</dd>
          </template>
        </dl>
        <div v-if="o.reason" class="mt-3">
          <div v-if="o.reason.presets?.length" class="mb-2 flex flex-wrap gap-1.5">
            <button
              v-for="p in o.reason.presets"
              :key="p"
              type="button"
              class="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-surface-2"
              :class="{ 'border-primary bg-primary/10 text-primary': reason === p }"
              @click="reason = p"
            >{{ p }}</button>
          </div>
          <FormField :label="o.reason.label ?? (needsReason ? 'Reason (audited)' : 'Reason (optional)')" for="confirm-reason">
            <textarea id="confirm-reason" v-model="reason" class="input" rows="2" maxlength="500" :placeholder="o.reason.placeholder" :required="needsReason" />
          </FormField>
        </div>
      </div>
    </form>
    <template #footer>
      <AppButton variant="secondary" @click="answer(false)">Cancel</AppButton>
      <AppButton type="submit" form="confirm-form" :variant="danger ? 'danger' : 'primary'" :disabled="!valid">{{ o?.confirmLabel ?? 'Delete' }}</AppButton>
    </template>
  </AppModal>
</template>
