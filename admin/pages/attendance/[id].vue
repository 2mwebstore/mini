<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import type { Confirmation } from '~/types/api'
import { confirmationStatus, dayStatus, formatClock, formatDate, formatDateTime, formatMinutes, typeLabel } from '~/utils/format'
import { optionsFrom } from '~/utils/options'

const statusOptions = optionsFrom(dayStatus, ['PRESENT', 'LATE', 'EARLY_LEAVE', 'ABSENT', 'LEAVE', 'HOLIDAY'])

const auth = useAuthStore()
const route = useRoute()
const id = Number(route.params.id)
const tz = auth.company.timezone

const detail = useResource(() => attendanceService.get(id))
const att = computed(() => detail.data.value?.attendance)
const { busy, run } = useAction()

const reviews = computed(() => [...(att.value?.confirmations ?? [])].sort((a, b) => b.id - a.id))

async function confirm(c: Confirmation) {
  if (await run(`c${c.id}`, () => attendanceService.confirm(c.id), `${typeLabel(c.type)} confirmed`)) detail.load()
}
const rejecting = ref<Confirmation | null>(null)
const rejectOpen = computed({ get: () => !!rejecting.value, set: v => !v && (rejecting.value = null) })
async function reject(reason: string) {
  const c = rejecting.value!
  if (await run(`c${c.id}`, () => attendanceService.reject(c.id, reason), `${typeLabel(c.type)} rejected`)) {
    rejecting.value = null
    detail.load()
  }
}

// ---------- manual correction ----------
const clock24 = (iso: string | null | undefined) => (iso ? formatClock(iso, tz) : '')
const edit = reactive({ open: false, check_in: '', check_out: '', status: '', reason: '' })
function openEdit() {
  Object.assign(edit, { open: true, check_in: clock24(att.value?.check_in_at), check_out: clock24(att.value?.check_out_at), status: '', reason: '' })
}
async function saveEdit() {
  const ok = await run('edit', () =>
    attendanceService.edit(id, { check_in: edit.check_in || undefined, check_out: edit.check_out || undefined, status: edit.status || undefined, reason: edit.reason.trim() }),
  'Attendance updated')
  if (ok) {
    edit.open = false
    detail.load()
  }
}
const editOpen = computed({ get: () => edit.open, set: v => (edit.open = v) })
const mapUrl = (lat: number, lng: number) => `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lng}#map=18/${lat}/${lng}`
</script>

<template>
  <div>
    <NuxtLink to="/attendance" class="link text-sm">← Attendance</NuxtLink>
    <StateView :loading="detail.loading.value && !att" :error="detail.error.value" @retry="detail.load">
      <template v-if="att">
        <PageHeader class="mt-3" :title="att.employee?.full_name ?? 'Attendance'" :subtitle="`${att.employee?.employee_code} · ${formatDate(att.work_date, true)}`">
          <AppButton v-if="auth.can('attendance.edit')" variant="secondary" @click="openEdit">Correct times</AppButton>
        </PageHeader>

        <div class="grid gap-4 lg:grid-cols-3">
          <section class="card p-4 lg:col-span-2">
            <dl class="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <div><dt class="label">Check in</dt><dd class="text-lg font-semibold tabular-nums">{{ formatClock(att.check_in_at, tz) }}</dd></div>
              <div><dt class="label">Check out</dt><dd class="text-lg font-semibold tabular-nums">{{ formatClock(att.check_out_at, tz) }}</dd></div>
              <div><dt class="label">Worked</dt><dd class="text-lg font-semibold tabular-nums">{{ formatMinutes(att.working_minutes) }}</dd></div>
              <div><dt class="label">Late / early</dt><dd class="text-lg font-semibold tabular-nums">{{ formatMinutes(att.late_minutes) }} / {{ formatMinutes(att.early_leave_minutes) }}</dd></div>
              <div><dt class="label">Status</dt><dd><StatusBadge v-bind="dayStatus[att.status]" /></dd></div>
              <div><dt class="label">Review</dt><dd><StatusBadge v-bind="confirmationStatus[att.confirmation_status]" /></dd></div>
              <div><dt class="label">Source</dt><dd class="text-sm">{{ att.source === 'MANUAL' ? 'Manual entry' : att.source === 'SYSTEM' ? 'System' : 'Mini App' }}</dd></div>
              <div v-if="att.note" class="col-span-2 sm:col-span-4"><dt class="label">Note</dt><dd class="text-sm">{{ att.note }}</dd></div>
            </dl>
          </section>

          <section class="card p-4">
            <h2 class="mb-3 text-sm font-semibold">Reviews</h2>
            <p v-if="!reviews.length" class="text-sm text-muted">No reviews.</p>
            <ul class="flex flex-col gap-3">
              <li v-for="c in reviews" :key="c.id" class="text-sm">
                <div class="flex items-center justify-between gap-2">
                  <span class="font-medium">{{ typeLabel(c.type) }}</span>
                  <StatusBadge v-bind="confirmationStatus[c.status]" />
                </div>
                <p v-if="c.reason" class="mt-0.5 text-muted">{{ c.reason }}</p>
                <p v-if="c.reviewed_at" class="text-xs text-muted">{{ formatDateTime(c.reviewed_at, tz) }}</p>
                <div v-if="c.status === 'PENDING' && auth.isReviewer" class="mt-2 flex gap-2">
                  <AppButton size="sm" variant="danger" :disabled="busy === `c${c.id}`" @click="rejecting = c">Reject</AppButton>
                  <AppButton size="sm" variant="success" :loading="busy === `c${c.id}`" @click="confirm(c)">Confirm</AppButton>
                </div>
              </li>
            </ul>
          </section>
        </div>

        <section class="mt-4">
          <h2 class="mb-3 text-sm font-semibold">Photos</h2>
          <p v-if="!detail.data.value?.photos.length" class="card p-4 text-sm text-muted">No photos (manual or system record).</p>
          <div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <figure v-for="p in detail.data.value?.photos" :key="p.id" class="card overflow-hidden">
              <a :href="attendanceService.photoUrl(p.id)" target="_blank" rel="noopener">
                <img :src="attendanceService.photoUrl(p.id)" :alt="`${typeLabel(p.type)} photo`" class="aspect-[3/4] w-full bg-surface-2 object-cover" loading="lazy" />
              </a>
              <figcaption class="px-3 py-2 text-xs text-muted">{{ typeLabel(p.type) }} · {{ formatDateTime(p.created_at, tz) }}</figcaption>
            </figure>
          </div>
        </section>

        <section v-if="att.locations?.length" class="mt-4">
          <h2 class="mb-3 text-sm font-semibold">Location</h2>
          <div class="card overflow-x-auto">
            <table class="table">
              <thead><tr><th>When</th><th>Position</th><th>Accuracy</th><th>From office</th><th /></tr></thead>
              <tbody>
                <tr v-for="l in att.locations" :key="l.id">
                  <td>{{ typeLabel(l.type) }}</td>
                  <td class="font-mono text-xs">{{ l.latitude.toFixed(5) }}, {{ l.longitude.toFixed(5) }}</td>
                  <td>{{ l.accuracy != null ? `±${Math.round(l.accuracy)} m` : '—' }}</td>
                  <td>
                    <template v-if="l.distance_meters != null">
                      {{ Math.round(l.distance_meters) }} m
                      <StatusBadge class="ml-1" :tone="l.inside_office ? 'success' : 'warning'" :label="l.inside_office ? 'inside' : 'outside'" />
                    </template>
                    <template v-else>—</template>
                  </td>
                  <td class="text-right"><a :href="mapUrl(l.latitude, l.longitude)" target="_blank" rel="noopener noreferrer" class="link text-sm">Map ↗</a></td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </StateView>

    <RejectModal
      v-model="rejectOpen"
      :title="`Reject ${rejecting ? typeLabel(rejecting.type).toLowerCase() : ''}`"
      :presets="['Please provide a valid attendance photo.', 'Not at the workplace.', 'Wrong time or duplicate submission.']"
      required
      :loading="!!busy"
      @submit="reject"
    />

    <AppModal v-model="editOpen" title="Correct attendance">
      <form id="edit-form" class="grid gap-3 sm:grid-cols-2" @submit.prevent="saveEdit">
        <FormField label="Check in" for="e-in" :hint="`Company time (${tz})`"><input id="e-in" v-model="edit.check_in" type="time" class="input" /></FormField>
        <FormField label="Check out" for="e-out"><input id="e-out" v-model="edit.check_out" type="time" class="input" /></FormField>
        <FormField label="Status" for="e-status" class="sm:col-span-2" hint="Automatic derives late / early leave from the times.">
          <SearchSelect id="e-status" v-model="edit.status" :options="statusOptions" empty-label="Automatic" />
        </FormField>
        <FormField label="Reason (audited)" for="e-reason" class="sm:col-span-2">
          <textarea id="e-reason" v-model="edit.reason" class="input" rows="2" maxlength="500" required />
        </FormField>
      </form>
      <p class="mt-3 text-xs text-muted">Saving marks the record as confirmed by you.</p>
      <template #footer>
        <AppButton variant="secondary" @click="edit.open = false">Cancel</AppButton>
        <AppButton type="submit" form="edit-form" :loading="busy === 'edit'">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
