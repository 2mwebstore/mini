<script setup lang="ts">
import { branchService } from '~/services/branch.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Branch, BranchInput } from '~/types/api'
import { roleLabels } from '~/utils/format'
import type { SelectOption } from '~/utils/options'

definePageMeta({ permission: ['branches.manage', 'branch.settings'] })

const auth = useAuthStore()
const lookups = useLookupsStore()
const id = Number(useRoute().params.id)
const owner = computed(() => auth.can('branches.manage'))

const branch = useResource(() => branchService.get(id))
const { busy, run } = useAction()

// ---------- details, office location, Telegram chats ----------
const form = reactive({
  code: '', name: '', address: '', status: 'ACTIVE' as Branch['status'],
  ownOffice: false, lat: '' as number | '', lng: '' as number | '', radius: '' as number | '',
  group: 0, review: 0,
})
function fill(b: Branch) {
  Object.assign(form, {
    code: b.code, name: b.name, address: b.address, status: b.status,
    ownOffice: b.office_latitude != null, lat: b.office_latitude ?? '', lng: b.office_longitude ?? '', radius: b.allowed_radius_meters ?? 100,
    group: b.telegram_group_id, review: b.telegram_review_chat_id,
  })
}
watch(branch.data, b => b && fill(b))

async function save() {
  // A branch admin may only send the operational fields; the API enforces the same.
  const input: BranchInput = { telegram_group_id: form.group || 0, telegram_review_chat_id: form.review || 0 }
  if (form.ownOffice) {
    Object.assign(input, { office_latitude: Number(form.lat), office_longitude: Number(form.lng), allowed_radius_meters: Number(form.radius) })
  } else {
    input.use_company_office = true
  }
  if (owner.value) Object.assign(input, { code: form.code.trim(), name: form.name.trim(), address: form.address.trim(), status: form.status })
  await run('save', async () => {
    branch.data.value = await branchService.update(id, input)
    lookups.branches = null
  }, 'Branch saved')
}

// ---------- branch admin (owner only) ----------
onMounted(() => owner.value && lookups.loadEmployees().catch(() => {}))
const candidates = computed(() =>
  (lookups.employees ?? []).filter(e => !e.deleted_at && e.status === 'ACTIVE' && e.role?.code !== 'super_admin'),
)
const chosen = ref('')
const chosenEmp = computed(() => candidates.value.find(e => String(e.id) === chosen.value))
const candidateOptions = computed((): SelectOption[] =>
  candidates.value.map(e => ({
    value: String(e.id),
    label: e.full_name,
    sub: [e.employee_code, e.branch?.name, e.role ? roleLabels[e.role.code] : ''].filter(Boolean).join(' · '),
    disabled: e.id === branch.data.value?.admin_employee_id,
  })),
)
const statusOptions: SelectOption[] = [
  { value: 'ACTIVE', label: 'Open' },
  { value: 'INACTIVE', label: 'Closed' },
]
async function assign(employeeId: number | null) {
  const ok = await run('admin', async () => {
    branch.data.value = await branchService.assignAdmin(id, employeeId)
  }, employeeId ? 'Branch admin assigned' : 'Branch admin removed')
  if (ok) {
    chosen.value = ''
    lookups.invalidateStaff() // roles and branches changed
    lookups.loadEmployees().catch(() => {})
  }
}
const adminHasLogin = computed(() => {
  const adminId = branch.data.value?.admin_employee_id
  return adminId ? (lookups.employees ?? []).find(e => e.id === adminId)?.has_login ?? branch.data.value?.admin?.has_login : true
})
</script>

<template>
  <div>
    <NuxtLink v-if="owner" to="/branches" class="link text-sm">← Branches</NuxtLink>
    <StateView :loading="branch.loading.value && !branch.data.value" :error="branch.error.value" @retry="branch.load">
      <template v-if="branch.data.value">
        <PageHeader class="mt-3" :title="branch.data.value.name" :subtitle="`${branch.data.value.code} · ${branch.data.value.staff_count} staff`">
          <AppButton variant="secondary" @click="navigateTo({ path: '/employees', query: { branch_id: String(id) } })">Staff</AppButton>
          <AppButton :loading="busy === 'save'" @click="save">Save changes</AppButton>
        </PageHeader>

        <div class="grid gap-4 lg:grid-cols-2">
          <section v-if="owner" class="card grid gap-4 p-5 sm:grid-cols-3">
            <h2 class="text-sm font-semibold sm:col-span-3">Details</h2>
            <FormField label="Code" for="b-code"><input id="b-code" v-model="form.code" class="input uppercase" required /></FormField>
            <FormField label="Name" for="b-name" class="sm:col-span-2"><input id="b-name" v-model="form.name" class="input" required maxlength="150" /></FormField>
            <FormField label="Address" for="b-addr" class="sm:col-span-2"><input id="b-addr" v-model="form.address" class="input" maxlength="255" /></FormField>
            <FormField label="Status" for="b-status">
              <SearchSelect id="b-status" v-model="form.status" :options="statusOptions" />
            </FormField>
          </section>

          <section class="card flex flex-col gap-3 p-5">
            <h2 class="text-sm font-semibold">Branch admin</h2>
            <div v-if="branch.data.value.admin" class="flex items-center justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2">
              <div>
                <NuxtLink :to="`/employees/${branch.data.value.admin.id}`" class="font-medium hover:underline">{{ branch.data.value.admin.full_name }}</NuxtLink>
                <span class="block text-xs text-muted">{{ branch.data.value.admin.employee_code }}</span>
              </div>
              <AppButton v-if="owner" size="sm" variant="danger" :loading="busy === 'admin'" @click="assign(null)">Remove</AppButton>
            </div>
            <p v-else class="text-sm text-warning">This branch has no admin. Review requests go to managers, or to HR and the owner.</p>
            <p v-if="branch.data.value.admin && owner && !adminHasLogin" class="rounded-lg bg-warning/10 px-3 py-2 text-sm">
              {{ branch.data.value.admin.full_name }} has no admin panel login yet.
              <NuxtLink :to="`/employees/${branch.data.value.admin.id}`" class="link">Create one</NuxtLink> so they can manage the branch.
            </p>
            <template v-if="owner">
              <FormField label="Assign a new admin" for="b-admin" hint="The person becomes Branch Admin and moves to this branch; a replaced admin becomes staff.">
                <div class="flex gap-2">
                  <SearchSelect id="b-admin" v-model="chosen" :options="candidateOptions" placeholder="Choose an employee…" />
                  <AppButton :disabled="!chosen" :loading="busy === 'admin'" @click="assign(Number(chosen))">Assign</AppButton>
                </div>
              </FormField>
              <p v-if="chosenEmp?.role?.code === 'hr_admin'" class="text-xs text-warning">
                {{ chosenEmp.full_name }} is a company-wide HR admin; assigning limits them to this branch.
              </p>
            </template>
          </section>

          <section class="card grid gap-4 p-5 sm:grid-cols-3">
            <h2 class="text-sm font-semibold sm:col-span-3">Office location (check-in GPS)</h2>
            <label class="flex items-center gap-2 text-sm sm:col-span-3">
              <input v-model="form.ownOffice" type="checkbox" class="size-4 accent-[var(--primary)]" />
              This branch has its own office location
            </label>
            <template v-if="form.ownOffice">
              <FormField label="Latitude" for="b-lat"><input id="b-lat" v-model.number="form.lat" class="input" type="number" step="any" min="-90" max="90" required /></FormField>
              <FormField label="Longitude" for="b-lng"><input id="b-lng" v-model.number="form.lng" class="input" type="number" step="any" min="-180" max="180" required /></FormField>
              <FormField label="Radius (m)" for="b-radius"><input id="b-radius" v-model.number="form.radius" class="input" type="number" min="10" max="100000" required /></FormField>
            </template>
            <p v-else class="text-sm text-muted sm:col-span-3">Uses the company office location from Settings.</p>
            <p class="text-xs text-muted sm:col-span-3">Whether GPS is asked for at all is a company setting.</p>
          </section>

          <section class="card grid gap-4 p-5 sm:grid-cols-2">
            <h2 class="text-sm font-semibold sm:col-span-2">Telegram</h2>
            <FormField label="Branch group chat ID" for="b-group" hint="Gets this branch's daily report. 0 = none.">
              <input id="b-group" v-model.number="form.group" class="input font-mono" type="number" step="1" />
            </FormField>
            <FormField label="Review chat ID" for="b-review" hint="Optional group for this branch's review requests. 0 = message the branch admin and managers.">
              <input id="b-review" v-model.number="form.review" class="input font-mono" type="number" step="1" />
            </FormField>
          </section>
        </div>
      </template>
    </StateView>
  </div>
</template>
