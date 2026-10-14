<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { overtimeService } from '~/services/overtime.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { OvertimeRequest } from '~/types/api'
import { formatClock, formatDate, formatMinutes, leaveStatus, nextDay } from '~/utils/format'
import { optionsFrom } from '~/utils/options'

// Overtime employees asked for in the Mini App: by default from the end of their
// shift. Their line manager (or branch admin, HR, the owner) approves or rejects.
const statusOptions = optionsFrom(leaveStatus)
const auth = useAuthStore()
const lookups = useLookupsStore()
const tz = auth.company.timezone
const route = useRoute()
const filter = reactive({ status: typeof route.query.status === 'string' ? route.query.status : '', branch_id: '', employee_id: '', from: '', to: '' })

const list = usePaged(p =>
  overtimeService.page({
    status: filter.status || undefined,
    branch_id: Number(filter.branch_id) || undefined,
    employee_id: Number(filter.employee_id) || undefined,
    from: filter.from || undefined,
    to: filter.to || undefined,
  }, p),
)
watch(filter, () => list.reset())
onMounted(() => lookups.loadEmployees().catch(() => {}))

// Approved and pending time on this page's filter (all pages: see Reports).
const { busy, error, run } = useAction()
async function decide(o: OvertimeRequest, fn: () => Promise<unknown>, msg: string) {
  const ok = await run(`o${o.id}`, fn, msg)
  if (ok || (error.value as ApiError | null)?.status === 409) list.refresh()
  return ok
}
const approve = (o: OvertimeRequest) => decide(o, () => overtimeService.approve(o.id), `Approved overtime for ${o.employee?.full_name}`)
const rejecting = ref<OvertimeRequest | null>(null)
const rejectOpen = computed({ get: () => !!rejecting.value, set: v => !v && (rejecting.value = null) })
async function reject(note: string) {
  const o = rejecting.value!
  if (await decide(o, () => overtimeService.reject(o.id, note), 'Overtime rejected')) rejecting.value = null
}
// Reviewers act on their scope; the API checks each request (no self-approval, team only for managers).
const canDecide = (o: OvertimeRequest) => o.status === 'PENDING' && auth.isReviewer && (o.employee_id !== auth.employee?.id || auth.role === 'super_admin')
</script>

<template>
  <div>
    <PageHeader
      title="Overtime"
      subtitle="Extra time employees ask for in the Mini App — by default from the end of their shift, for an hour. Their line manager approves it; Reports count approved and pending overtime."
    />

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-3 lg:grid-cols-5">
      <FormField label="Status" for="o-status">
        <SearchSelect id="o-status" v-model="filter.status" :options="statusOptions" empty-label="All" />
      </FormField>
      <BranchFilter id="o-branch" v-model="filter.branch_id" />
      <FormField label="Employee" for="o-emp">
        <SearchSelect id="o-emp" v-model="filter.employee_id" :options="lookups.employeeOptions" empty-label="All" />
      </FormField>
      <FormField label="From" for="o-from"><input id="o-from" v-model="filter.from" type="date" class="input" /></FormField>
      <FormField label="To" for="o-to"><input id="o-to" v-model="filter.to" type="date" class="input" :min="filter.from" /></FormField>
    </div>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.total.value === 0" empty-text="No overtime requests match." @retry="list.load">
      <div class="card overflow-hidden" :class="{ 'opacity-60': list.loading.value }">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Employee</th><th>Date</th><th>Time</th><th>Length</th><th>Reason</th><th>Status</th><th class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="o in list.rows.value" :key="o.id">
                <td>
                  <span class="font-medium">{{ o.employee?.full_name }}</span>
                  <span class="block text-xs text-muted">{{ o.employee?.employee_code }}<template v-if="auth.multiBranch && o.employee?.branch"> · {{ o.employee.branch.name }}</template></span>
                </td>
                <td class="whitespace-nowrap">
                  {{ formatDate(o.work_date, true) }}
                  <span class="block text-xs text-muted">asked {{ formatDate(o.created_at) }}</span>
                </td>
                <td class="whitespace-nowrap tabular-nums">
                  {{ formatClock(o.start_at, tz) }} – {{ formatClock(o.end_at, tz) }}<sup v-if="nextDay(o.work_date, o.end_at, tz)" class="ml-0.5 text-[10px] text-muted" title="Next day">+1</sup>
                </td>
                <td class="whitespace-nowrap font-medium tabular-nums">{{ formatMinutes(o.minutes) }}</td>
                <td class="max-w-xs"><p class="line-clamp-2" :title="o.reason">{{ o.reason }}</p></td>
                <td>
                  <StatusBadge v-bind="leaveStatus[o.status]" />
                  <!-- the employee cancels; a reviewer only approves or rejects -->
                  <span v-if="o.reviewer" class="block text-xs text-muted">
                    {{ o.status === 'CANCELLED' ? `approved by ${o.reviewer.full_name}, then cancelled by the employee` : `by ${o.reviewer.full_name}` }}
                  </span>
                  <span v-else-if="o.status === 'CANCELLED'" class="block text-xs text-muted">by the employee</span>
                  <span v-if="o.review_note" class="block max-w-[12rem] truncate text-xs text-muted" :title="o.review_note">“{{ o.review_note }}”</span>
                </td>
                <td>
                  <div v-if="canDecide(o)" class="flex justify-end gap-1.5">
                    <IconAction kind="approve" :label="`Approve overtime for ${o.employee?.full_name}`" :loading="busy === `o${o.id}`" @click="approve(o)" />
                    <IconAction kind="reject" :label="`Reject overtime for ${o.employee?.full_name}`" :disabled="busy === `o${o.id}`" @click="rejecting = o" />
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <AppPagination :page="list.page.value" :page-size="list.pageSize.value" :total="list.total.value" noun="request" @go="list.go" @size="list.resize" />
      </div>
    </StateView>

    <RejectModal
      v-model="rejectOpen" :title="`Reject overtime — ${rejecting?.employee?.full_name ?? ''}`" :loading="!!busy"
      :presets="['Not needed this time', 'Please plan it with me first', 'Too many hours this week']" @submit="reject"
    />
  </div>
</template>
