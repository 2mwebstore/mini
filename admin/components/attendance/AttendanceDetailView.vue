<script setup lang="ts">
// Everything about one attendance day: who, the hours they were expected to work
// (shift, roster, special schedule, half-day leave), then the check-in and the
// check-out each with its own time, status, review and location, and the full
// review history. Used by the list's "View" and by /attendance/:id.
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import type { AttendanceDetail, AttendanceLocation, Confirmation } from '~/types/api'
import {
  confirmationStatus, dayStatus, formatClock, formatDate, formatDateTime, formatMinutes, latestReview, leaveSpan, nextDay, typeLabel,
} from '~/utils/format'

const props = defineProps<{ detail: AttendanceDetail }>()
const emit = defineEmits<{ changed: [] }>()

const auth = useAuthStore()
const tz = auth.company.timezone
const att = computed(() => props.detail.attendance)
const emp = computed(() => att.value.employee)
const plan = computed(() => props.detail.schedule)
const leave = computed(() => props.detail.leave)

const sourceLabel = { DEFAULT: 'Usual shift', ROSTER: 'Weekly roster', SPECIAL: 'Special schedule' } as const
const recordSource = computed(() => ({ MANUAL: 'Manual entry', SYSTEM: 'System', TELEGRAM: 'Mini App' })[att.value.source] ?? att.value.source)

const mapUrl = (l: AttendanceLocation) => `https://www.openstreetmap.org/?mlat=${l.latitude}&mlon=${l.longitude}#map=18/${l.latitude}/${l.longitude}`

// One side of the day: the check-in or the check-out.
function side(type: 'CHECK_IN' | 'CHECK_OUT') {
  const a = att.value
  const isIn = type === 'CHECK_IN'
  const at = isIn ? a.check_in_at : a.check_out_at
  const review = latestReview(a.confirmations, type)
  const location = (review?.location_id ? a.locations?.find(l => l.id === review.location_id) : undefined)
    ?? (a.source === 'TELEGRAM' ? [...(a.locations ?? [])].filter(l => l.type === type).sort((x, y) => y.id - x.id)[0] : undefined)
  const photo = review?.photo_id ? props.detail.photos.find(p => p.id === review.photo_id) : undefined
  let status: { label: string; tone: 'success' | 'warning' | 'neutral' }
  if (!at) status = { label: isIn ? 'Not checked in' : 'Not checked out', tone: 'neutral' }
  else if (isIn) status = a.late_minutes ? { label: `Late ${formatMinutes(a.late_minutes)}`, tone: 'warning' } : { label: 'On time', tone: 'success' }
  else status = a.early_leave_minutes ? { label: `Left ${formatMinutes(a.early_leave_minutes)} early`, tone: 'warning' } : { label: 'On time', tone: 'success' }
  const expected = plan.value.work ? (isIn ? plan.value.start : `${plan.value.end}${plan.value.cross_day ? ' (+1)' : ''}`) : ''
  const reason = isIn ? a.check_in_reason : a.check_out_reason // the employee's: why late / early
  return { type, isIn, at, next: !isIn && nextDay(a.work_date, at, tz), review, location, photo, status, expected, reason }
}
const sides = computed(() => [side('CHECK_IN'), side('CHECK_OUT')])
const history = computed(() => [...(att.value.confirmations ?? [])].sort((a, b) => b.id - a.id))

// ---------- review ----------
const { busy, run } = useAction()
async function confirm(c: Confirmation) {
  if (await run(`c${c.id}`, () => attendanceService.confirm(c.id), `${typeLabel(c.type)} confirmed`)) emit('changed')
}
const rejecting = ref<Confirmation | null>(null)
const rejectOpen = computed({ get: () => !!rejecting.value, set: v => !v && (rejecting.value = null) })
async function reject(reason: string) {
  const c = rejecting.value!
  if (await run(`c${c.id}`, () => attendanceService.reject(c.id, reason), `${typeLabel(c.type)} rejected`)) {
    rejecting.value = null
    emit('changed')
  }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <!-- who and the day's status -->
    <section class="flex flex-wrap items-start gap-3">
      <span class="flex size-11 shrink-0 items-center justify-center rounded-full bg-primary/10 text-lg font-semibold text-primary" aria-hidden="true">
        {{ (emp?.full_name ?? '?').charAt(0).toUpperCase() }}
      </span>
      <div class="min-w-0 flex-1">
        <p class="font-semibold">
          <NuxtLink v-if="emp" :to="`/employees/${emp.id}`" class="hover:underline">{{ emp.full_name }}</NuxtLink>
        </p>
        <p class="text-xs text-muted">
          {{ [emp?.employee_code, emp?.position, emp?.department?.name, emp?.branch?.name].filter(Boolean).join(' · ') }}
        </p>
        <p class="mt-0.5 text-sm">{{ formatDate(att.work_date, true) }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <StatusBadge v-bind="dayStatus[att.status]" />
        <StatusBadge v-if="att.check_in_at" v-bind="confirmationStatus[att.confirmation_status]" />
      </div>
    </section>

    <!-- what they were expected to work -->
    <section class="rounded-lg border border-border p-3 text-sm">
      <h3 class="label mb-1.5">Schedule</h3>
      <p v-if="detail.holiday" class="font-medium">Holiday · {{ detail.holiday }}</p>
      <p v-else-if="!plan.work" class="font-medium">Day off<template v-if="plan.source === 'SPECIAL'"> (special schedule<template v-if="plan.note">: {{ plan.note }}</template>)</template></p>
      <div v-else class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span class="inline-flex items-center gap-1.5 font-medium">
          <span v-if="plan.color" class="size-2.5 rounded-full" :style="{ background: plan.color }" aria-hidden="true" />
          {{ plan.shift_name || 'Shift' }} {{ plan.start }} – {{ plan.end }}<template v-if="plan.cross_day"> (+1 day)</template>
        </span>
        <span class="text-muted">{{ plan.cross_day ? 'Cross-day (night) shift' : 'Normal shift' }} · grace {{ plan.grace_minutes }} min</span>
        <span class="text-muted">{{ sourceLabel[plan.source] }}<template v-if="plan.source === 'SPECIAL' && plan.note">: {{ plan.note }}</template></span>
      </div>
      <p v-if="leave" class="mt-1.5 rounded-md bg-info/10 px-2 py-1 text-info">
        {{ leave.leave_type?.name ?? 'Leave' }} · {{ leaveSpan(leave) }}
        <template v-if="leave.day_type !== 'FULL' && plan.work"> — the times above are the half still worked.</template>
      </p>
    </section>

    <!-- check-in and check-out, each on its own -->
    <div class="grid gap-3 md:grid-cols-2">
      <section v-for="s in sides" :key="s.type" class="flex flex-col rounded-lg border border-border p-3" :aria-label="typeLabel(s.type)">
        <div class="flex items-center justify-between gap-2">
          <h3 class="flex items-center gap-1.5 text-sm font-semibold">
            <span class="size-2 rounded-full" :class="s.isIn ? 'bg-success' : 'bg-primary'" aria-hidden="true" />{{ typeLabel(s.type) }}
          </h3>
          <StatusBadge v-if="s.review" v-bind="confirmationStatus[s.review.status]" />
        </div>
        <p class="mt-2 text-2xl font-semibold tabular-nums" :class="!s.at && 'text-muted'">
          {{ s.at ? formatClock(s.at, tz) : '--:--' }}<sup v-if="s.next" class="ml-0.5 text-xs text-muted" title="Next morning (night shift)">+1</sup>
        </p>
        <p class="text-sm">
          <StatusBadge :tone="s.status.tone" :label="s.status.label" />
          <span v-if="s.expected" class="ml-1.5 text-xs text-muted">{{ s.isIn ? 'expected' : 'ends' }} {{ s.expected }}</span>
        </p>
        <blockquote v-if="s.reason" class="mt-2 rounded-md border-l-2 border-warning bg-warning/5 px-2.5 py-1.5 text-sm">
          <span class="block text-xs font-medium text-muted">{{ s.isIn ? 'Reason for checking in late' : 'Reason for leaving early' }}</span>
          {{ s.reason }}
        </blockquote>

        <dl class="mt-3 grid gap-1.5 text-sm">
          <div v-if="s.review?.reviewed_at" class="flex justify-between gap-3">
            <dt class="text-muted">{{ s.review.status === 'REJECTED' ? 'Rejected' : 'Reviewed' }}</dt>
            <dd class="text-right">{{ s.review.reviewer?.full_name ?? 'Automatic' }} · {{ formatDateTime(s.review.reviewed_at, tz) }}</dd>
          </div>
          <div v-if="s.review?.reason" class="flex justify-between gap-3">
            <dt class="text-muted">Review note</dt><dd class="text-right">{{ s.review.reason }}</dd>
          </div>
          <div v-if="s.at" class="flex justify-between gap-3">
            <dt class="text-muted">Location</dt>
            <dd class="text-right">
              <template v-if="s.location">
                <template v-if="s.location.distance_meters != null">
                  {{ Math.round(s.location.distance_meters) }} m from the office
                  <StatusBadge class="ml-1" :tone="s.location.inside_office ? 'success' : 'warning'" :label="s.location.inside_office ? 'inside' : 'outside'" />
                </template>
                <template v-else>Shared</template>
                <span class="block text-xs text-muted">
                  <span class="font-mono">{{ s.location.latitude.toFixed(5) }}, {{ s.location.longitude.toFixed(5) }}</span>
                  <template v-if="s.location.accuracy != null"> · ±{{ Math.round(s.location.accuracy) }} m</template>
                  · <a :href="mapUrl(s.location)" target="_blank" rel="noopener noreferrer" class="link">Map ↗</a>
                </span>
              </template>
              <span v-else class="text-muted">{{ att.source === 'TELEGRAM' ? 'Not shared' : '—' }}</span>
            </dd>
          </div>
        </dl>

        <a v-if="s.photo" :href="attendanceService.photoUrl(s.photo.id)" target="_blank" rel="noopener" class="mt-3 block w-24">
          <img :src="attendanceService.photoUrl(s.photo.id)" :alt="`${typeLabel(s.type)} photo`" class="aspect-[3/4] w-full rounded-md bg-surface-2 object-cover" loading="lazy" />
        </a>

        <div v-if="s.review?.status === 'PENDING' && auth.isReviewer" class="mt-auto flex gap-2 pt-3">
          <AppButton size="sm" variant="danger" :disabled="busy === `c${s.review.id}`" @click="rejecting = s.review">Reject</AppButton>
          <AppButton size="sm" variant="success" :loading="busy === `c${s.review.id}`" @click="confirm(s.review)">Confirm</AppButton>
        </div>
      </section>
    </div>

    <!-- totals and record facts -->
    <dl class="grid grid-cols-2 gap-3 rounded-lg bg-surface-2 p-3 text-sm sm:grid-cols-4">
      <div><dt class="label">Worked</dt><dd class="font-semibold tabular-nums">{{ formatMinutes(att.working_minutes) }}</dd></div>
      <div><dt class="label">Late</dt><dd class="font-semibold tabular-nums" :class="att.late_minutes ? 'text-warning' : ''">{{ formatMinutes(att.late_minutes) }}</dd></div>
      <div><dt class="label">Early leave</dt><dd class="font-semibold tabular-nums" :class="att.early_leave_minutes ? 'text-warning' : ''">{{ formatMinutes(att.early_leave_minutes) }}</dd></div>
      <div><dt class="label">Recorded by</dt><dd>{{ recordSource }}</dd></div>
      <div v-if="att.note" class="col-span-2 sm:col-span-4"><dt class="label">Note</dt><dd>{{ att.note }}</dd></div>
    </dl>

    <!-- every submission and review, newest first -->
    <section v-if="history.length">
      <h3 class="label mb-2">Review history</h3>
      <ol class="flex flex-col gap-2 border-l-2 border-border pl-3 text-sm">
        <li v-for="c in history" :key="c.id">
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-medium">{{ typeLabel(c.type) }}</span>
            <StatusBadge v-bind="confirmationStatus[c.status]" />
            <span class="text-xs text-muted">submitted {{ formatDateTime(c.created_at, tz) }}</span>
          </div>
          <p v-if="c.reviewed_at" class="text-xs text-muted">
            {{ c.reviewer?.full_name ?? 'Automatic' }} · {{ formatDateTime(c.reviewed_at, tz) }}<template v-if="c.reason"> — “{{ c.reason }}”</template>
          </p>
        </li>
      </ol>
    </section>

    <RejectModal
      v-model="rejectOpen"
      :title="`Reject ${rejecting ? typeLabel(rejecting.type).toLowerCase() : ''}`"
      :presets="['Not at the workplace.', 'Location does not match the workplace.', 'Wrong time or duplicate submission.']"
      required
      :loading="!!busy"
      @submit="reject"
    />
  </div>
</template>
