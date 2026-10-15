<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import type { Attendance } from '~/types/api'
import { formatClock, formatDate, nextDay } from '~/utils/format'

// Corrects one attendance record's times with an audited reason; the status (late,
// early leave) follows from the times and the day's shift. Used on the attendance
// list (Edit) and on a record's own page.
const open = defineModel<boolean>({ required: true })
const props = defineProps<{ record: Attendance | null }>()
const emit = defineEmits<{ saved: [] }>()

const auth = useAuthStore()
const tz = auth.company.timezone
const clock24 = (iso: string | null | undefined) => (iso ? formatClock(iso, tz) : '')

const form = reactive({ check_in: '', check_out: '', reason: '' })
watch(open, v => {
  const r = props.record
  if (v && r) Object.assign(form, { check_in: clock24(r.check_in_at), check_out: clock24(r.check_out_at), reason: '' })
})
// A check-out at or before the check-in time is read as the next morning (night shifts).
const overnight = computed(() => !!form.check_in && !!form.check_out && form.check_out <= form.check_in)
const wasOvernight = computed(() => !!props.record && nextDay(props.record.work_date, props.record.check_out_at, tz))

const { busy, run } = useAction()
async function save() {
  const r = props.record
  if (!r) return
  const ok = await run('edit', () => attendanceService.edit(r.id, {
    check_in: form.check_in || undefined,
    check_out: form.check_out || undefined,
    reason: form.reason.trim(),
  }), 'Attendance updated')
  if (ok) {
    open.value = false
    emit('saved')
  }
}
</script>

<template>
  <AppModal v-model="open" :title="record?.employee ? `Edit attendance — ${record.employee.full_name}` : 'Edit attendance'">
    <p v-if="record" class="-mt-2 mb-4 text-sm text-muted">
      {{ formatDate(record.work_date, true) }}<template v-if="record.employee"> · {{ record.employee.employee_code }}</template>
    </p>
    <form id="attendance-edit-form" class="grid gap-3 sm:grid-cols-2" @submit.prevent="save">
      <FormField label="Check in" for="ae-in" :hint="`Company time (${tz})`"><input id="ae-in" v-model="form.check_in" type="time" class="input" required /></FormField>
      <FormField label="Check out" for="ae-out" :hint="overnight || wasOvernight ? 'Next morning (night shift).' : undefined">
        <input id="ae-out" v-model="form.check_out" type="time" class="input" />
      </FormField>
      <FormField label="Reason (audited)" for="ae-reason" class="sm:col-span-2">
        <textarea id="ae-reason" v-model="form.reason" class="input" rows="2" maxlength="500" required />
      </FormField>
    </form>
    <p class="mt-3 text-xs text-muted">Late and early leave are worked out from the times and the day's shift. Saving marks the record as confirmed by you.</p>
    <template #footer>
      <AppButton variant="secondary" @click="open = false">Cancel</AppButton>
      <AppButton type="submit" form="attendance-edit-form" :loading="busy === 'edit'">Save</AppButton>
    </template>
  </AppModal>
</template>
