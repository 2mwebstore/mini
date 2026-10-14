<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { leaveService } from '~/services/leave.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { LeaveRequest } from '~/types/api'
import { fmtDays, formatDate, formatDateRange, leaveStatus } from '~/utils/format'
import { optionsFrom } from '~/utils/options'

const statusOptions = optionsFrom(leaveStatus)
const dayTypeOptions = [{ value: 'FULL', label: 'Full day' }, { value: 'HALF', label: 'Half day' }]

const auth = useAuthStore()
const lookups = useLookupsStore()
// All statuses by default; ?status=PENDING (dashboard link) pre-selects one.
const route = useRoute()
const filter = reactive({ status: typeof route.query.status === 'string' ? route.query.status : '', branch_id: '', employee_id: '', from: '', to: '', day_type: '' })

const list = usePaged(p =>
  leaveService.page({
    status: filter.status || undefined,
    branch_id: Number(filter.branch_id) || undefined,
    employee_id: Number(filter.employee_id) || undefined,
    from: filter.from || undefined,
    to: filter.to || undefined,
    day_type: filter.day_type || undefined,
  }, p),
)
watch(filter, () => list.reset())
onMounted(() => lookups.loadEmployees().catch(() => {}))

const { busy, error, run } = useAction()
async function decide(l: LeaveRequest, fn: () => Promise<unknown>, msg: string) {
  const ok = await run(`l${l.id}`, fn, msg)
  if (ok || (error.value as ApiError | null)?.status === 409) list.refresh()
  return ok
}
const approve = (l: LeaveRequest) => decide(l, () => leaveService.approve(l.id), `Approved leave for ${l.employee?.full_name}`)
const rejecting = ref<LeaveRequest | null>(null)
const rejectOpen = computed({ get: () => !!rejecting.value, set: v => !v && (rejecting.value = null) })
async function reject(note: string) {
  const l = rejecting.value!
  if (await decide(l, () => leaveService.reject(l.id, note), 'Leave rejected')) rejecting.value = null
}
// Reviewers act on their scope; the API checks each request (no self-approval, team only for managers).
const canDecide = (l: LeaveRequest) => l.status === 'PENDING' && auth.isReviewer && (l.employee_id !== auth.employee?.id || auth.role === 'super_admin')
</script>

<template>
  <div>
    <PageHeader title="Leave requests" subtitle="Requests from the Mini App. Approving a full day marks it as leave; a half day only changes the hours expected that day." />

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-3 lg:grid-cols-6">
      <FormField label="Status" for="l-status">
        <SearchSelect id="l-status" v-model="filter.status" :options="statusOptions" empty-label="All" />
      </FormField>
      <BranchFilter id="l-branch" v-model="filter.branch_id" />
      <FormField label="Employee" for="l-emp">
        <SearchSelect id="l-emp" v-model="filter.employee_id" :options="lookups.employeeOptions" empty-label="All" />
      </FormField>
      <FormField label="Day" for="l-day">
        <SearchSelect id="l-day" v-model="filter.day_type" :options="dayTypeOptions" empty-label="All" />
      </FormField>
      <FormField label="From" for="l-from"><input id="l-from" v-model="filter.from" type="date" class="input" /></FormField>
      <FormField label="To" for="l-to"><input id="l-to" v-model="filter.to" type="date" class="input" /></FormField>
    </div>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.total.value === 0" empty-text="No leave requests match." @retry="list.load">
      <div class="card overflow-hidden" :class="{ 'opacity-60': list.loading.value }">
        <div class="overflow-x-auto">
        <table class="table">
          <thead><tr><th>Employee</th><th>Type</th><th>Dates</th><th>Days</th><th>Reason</th><th>Status</th><th class="text-right">Actions</th></tr></thead>
          <tbody>
            <tr v-for="l in list.rows.value" :key="l.id">
              <td><span class="font-medium">{{ l.employee?.full_name }}</span><span class="block text-xs text-muted">{{ l.employee?.employee_code }} · requested {{ formatDate(l.created_at) }}</span></td>
              <td>{{ l.leave_type?.name }}</td>
              <td class="whitespace-nowrap">
                {{ formatDateRange(l.start_date, l.end_date) }}
                <span v-if="l.day_type !== 'FULL'" class="block text-xs text-muted">
                  {{ l.day_type === 'FIRST_HALF' ? 'First half' : 'Second half' }}<template v-if="l.half_from"> · {{ l.half_from }}–{{ l.half_to }}</template>
                </span>
              </td>
              <td class="whitespace-nowrap tabular-nums">
                <StatusBadge v-if="l.day_type !== 'FULL'" tone="info" label="½ day" />
                <template v-else>{{ fmtDays(l.days) }}</template>
              </td>
              <td class="max-w-xs"><p class="line-clamp-2">{{ l.reason }}</p></td>
              <td>
                <StatusBadge v-bind="leaveStatus[l.status]" />
                <span v-if="l.reviewer" class="block text-xs text-muted">by {{ l.reviewer.full_name }}</span>
                <span v-if="l.review_note" class="block max-w-[12rem] truncate text-xs text-muted" :title="l.review_note">“{{ l.review_note }}”</span>
              </td>
              <td>
                <div v-if="canDecide(l)" class="flex justify-end gap-1.5">
                  <IconAction kind="approve" :label="`Approve leave for ${l.employee?.full_name}`" :loading="busy === `l${l.id}`" @click="approve(l)" />
                  <IconAction kind="reject" :label="`Reject leave for ${l.employee?.full_name}`" :disabled="busy === `l${l.id}`" @click="rejecting = l" />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        </div>
        <AppPagination :page="list.page.value" :page-size="list.pageSize.value" :total="list.total.value" noun="request" @go="list.go" @size="list.resize" />
      </div>
    </StateView>

    <RejectModal v-model="rejectOpen" :title="`Reject leave — ${rejecting?.employee?.full_name ?? ''}`" :loading="!!busy" @submit="reject" />
  </div>
</template>
