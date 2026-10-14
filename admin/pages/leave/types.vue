<script setup lang="ts">
import { leaveService } from '~/services/leave.service'
import { useAuthStore } from '~/stores/auth'
import type { LeaveApproval, LeaveType } from '~/types/api'

const auth = useAuthStore()
const canEdit = computed(() => auth.can('leave.manage'))
const types = useResource(() => leaveService.allTypes())
const { busy, run } = useAction()
const { confirm } = useConfirm()

const palette = ['#16a34a', '#dc2626', '#7c3aed', '#ea580c', '#0891b2', '#db2777', '#ca8a04', '#2563eb', '#64748b']
const approvalOptions = [
  { value: 'MANUAL', label: 'A reviewer approves' },
  { value: 'AUTO', label: 'Approved automatically' },
]
const blank = () => ({
  id: 0, code: '', name: '', annual_days: 0, is_paid: true, approval: 'MANUAL' as LeaveApproval, allow_exceed: false,
  max_days_per_request: 0, min_notice_days: 0, color: palette[0]!, description: '', active: true,
})
const form = reactive(blank())
const open = ref(false)
function edit(t?: LeaveType) {
  Object.assign(form, blank(), t
    ? { id: t.id, code: t.code, name: t.name, annual_days: t.annual_days, is_paid: t.is_paid, approval: t.approval, allow_exceed: t.allow_exceed,
        max_days_per_request: t.max_days_per_request, min_notice_days: t.min_notice_days, color: t.color, description: t.description, active: t.active }
    : { color: palette.find(c => !types.data.value?.some(t => t.color === c)) ?? palette[0] })
  open.value = true
}
// The code is suggested from the name for new types (MATERNITY_LEAVE).
watch(() => form.name, n => {
  if (!form.id) form.code = n.toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_|_$/g, '').slice(0, 32)
})

async function save() {
  const { id, ...input } = form
  const body = { ...input, annual_days: Number(input.annual_days), max_days_per_request: Number(input.max_days_per_request), min_notice_days: Number(input.min_notice_days) }
  const ok = await run('save', () => (id ? leaveService.updateType(id, body) : leaveService.createType(body)), id ? 'Leave type saved' : 'Leave type added')
  if (ok) {
    open.value = false
    types.load()
  }
}
async function toggle(t: LeaveType) {
  if (await run(`a${t.id}`, () => leaveService.updateType(t.id, { active: !t.active }), t.active ? `${t.name} turned off` : `${t.name} turned on`)) types.load()
}
async function remove(t: LeaveType) {
  if (!(await confirm({ title: `Delete the leave type “${t.name}”?`, message: 'It has never been used. A type that has been used can only be turned off.' }))) return
  if (await run(`d${t.id}`, () => leaveService.deleteType(t.id), 'Leave type deleted')) types.load()
}
const days = (n: number) => `${n % 1 ? n.toFixed(1) : n} day${n === 1 ? '' : 's'}`
</script>

<template>
  <div>
    <PageHeader title="Leave types" subtitle="Leave categories, their yearly allowance and approval policy. Employees choose from the active types in the Mini App.">
      <AppButton v-if="canEdit" @click="edit()">New leave type</AppButton>
    </PageHeader>

    <StateView :loading="types.loading.value && !types.data.value" :error="types.error.value" :empty="types.data.value?.length === 0" empty-text="No leave types yet." @retry="types.load">
      <div class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Leave type</th><th>Allowance / year</th><th>Approval</th><th>Rules</th><th>Requests</th><th>Active</th><th v-if="canEdit" class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="t in types.data.value ?? []" :key="t.id" :class="!t.active && 'opacity-60'">
                <td>
                  <span class="flex items-center gap-2 font-medium">
                    <span class="size-3 shrink-0 rounded-full" :style="{ background: t.color }" aria-hidden="true" />{{ t.name }}
                  </span>
                  <span class="block text-xs text-muted">{{ t.code }}<template v-if="!t.is_paid"> · unpaid</template><template v-if="t.description"> · {{ t.description }}</template></span>
                </td>
                <td class="whitespace-nowrap">
                  <template v-if="t.annual_days > 0">{{ days(t.annual_days) }}</template>
                  <span v-else class="text-muted">Not counted</span>
                </td>
                <td><StatusBadge :tone="t.approval === 'AUTO' ? 'success' : 'neutral'" :label="t.approval === 'AUTO' ? 'Automatic' : 'Reviewer'" /></td>
                <td class="text-xs text-muted">
                  <span v-if="t.annual_days > 0" class="block">{{ t.allow_exceed ? 'May exceed balance' : 'Within balance' }}</span>
                  <span v-if="t.max_days_per_request" class="block">Max {{ days(t.max_days_per_request) }} per request</span>
                  <span v-if="t.min_notice_days" class="block">{{ days(t.min_notice_days) }} notice</span>
                  <span v-if="!t.annual_days && !t.max_days_per_request && !t.min_notice_days">—</span>
                </td>
                <td class="tabular-nums">{{ t.requests ?? 0 }}</td>
                <td>
                  <button
                    type="button"
                    role="switch"
                    :aria-checked="t.active"
                    :aria-label="`${t.name} active`"
                    :disabled="!canEdit || busy === `a${t.id}`"
                    class="relative h-5 w-9 rounded-full transition disabled:cursor-not-allowed"
                    :class="t.active ? 'bg-success' : 'bg-border'"
                    @click="toggle(t)"
                  >
                    <span class="absolute top-0.5 size-4 rounded-full bg-white shadow transition-all" :class="t.active ? 'left-[1.125rem]' : 'left-0.5'" />
                  </button>
                </td>
                <td v-if="canEdit" class="whitespace-nowrap text-right">
                  <AppButton size="sm" variant="ghost" :aria-label="`Edit ${t.name}`" @click="edit(t)">Edit</AppButton>
                  <AppButton v-if="!t.requests" size="sm" variant="ghost" :loading="busy === `d${t.id}`" :aria-label="`Delete ${t.name}`" @click="remove(t)">Delete</AppButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <p class="mt-3 text-xs text-muted">
        Balances are counted per calendar year from approved requests. Set a person's own entitlement or carry-over on
        <NuxtLink to="/leave/balances" class="link">Leave balances</NuxtLink>.
      </p>
    </StateView>

    <AppModal v-model="open" :title="form.id ? `Edit ${form.name}` : 'New leave type'" wide>
      <form id="lt-form" class="grid gap-4 sm:grid-cols-2" @submit.prevent="save">
        <FormField label="Name" for="lt-name"><input id="lt-name" v-model="form.name" class="input" required maxlength="100" placeholder="e.g. Maternity Leave" /></FormField>
        <FormField label="Code" for="lt-code" :hint="form.id ? 'Cannot be changed.' : 'Capital letters, digits and _'">
          <input id="lt-code" v-model="form.code" class="input font-mono uppercase" required pattern="[A-Za-z0-9_]{2,32}" :disabled="!!form.id" />
        </FormField>
        <FormField label="Yearly allowance (days)" for="lt-days" hint="0 = not counted (no balance).">
          <input id="lt-days" v-model.number="form.annual_days" type="number" min="0" max="365" step="0.5" class="input" required />
        </FormField>
        <FormField label="Colour">
          <div class="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Colour">
            <button
              v-for="c in palette" :key="c" type="button" role="radio" :aria-checked="form.color === c" :aria-label="c"
              class="size-7 rounded-full ring-offset-2 ring-offset-surface" :class="form.color === c && 'ring-2 ring-fg'" :style="{ background: c }" @click="form.color = c"
            />
          </div>
        </FormField>
        <FormField label="Approval" class="sm:col-span-2"><Segmented v-model="form.approval" :options="approvalOptions" label="Approval" /></FormField>
        <FormField label="Max days per request" for="lt-max" hint="0 = no limit.">
          <input id="lt-max" v-model.number="form.max_days_per_request" type="number" min="0" max="60" class="input" />
        </FormField>
        <FormField label="Notice (days before it starts)" for="lt-notice" hint="0 = can be requested any time.">
          <input id="lt-notice" v-model.number="form.min_notice_days" type="number" min="0" max="90" class="input" />
        </FormField>
        <div class="flex flex-col gap-2 text-sm sm:col-span-2">
          <label class="flex items-center gap-2"><input v-model="form.is_paid" type="checkbox" class="size-4 accent-[var(--primary)]" /> Paid leave</label>
          <label class="flex items-center gap-2" :class="!form.annual_days && 'opacity-50'">
            <input v-model="form.allow_exceed" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!form.annual_days" />
            May be requested beyond the remaining balance (e.g. sick leave with a certificate)
          </label>
          <label class="flex items-center gap-2"><input v-model="form.active" type="checkbox" class="size-4 accent-[var(--primary)]" /> Active (offered in the Mini App)</label>
        </div>
        <FormField label="Description" for="lt-desc" class="sm:col-span-2">
          <input id="lt-desc" v-model="form.description" class="input" maxlength="255" placeholder="Optional — e.g. With a medical certificate after 2 days" />
        </FormField>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="open = false">Cancel</AppButton>
        <AppButton type="submit" form="lt-form" :loading="busy === 'save'">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
