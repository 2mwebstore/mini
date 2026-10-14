<script setup lang="ts">
import { accessService } from '~/services/access.service'
import { branchService } from '~/services/branch.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Branch, BranchInput } from '~/types/api'
import { mapsUrl } from '~/utils/format'
import type { SelectOption } from '~/utils/options'

definePageMeta({ permission: ['branches.manage', 'branches.create', 'branch.settings'] })

const auth = useAuthStore()
const lookups = useLookupsStore()
const { confirm } = useConfirm()
const id = Number(useRoute().params.id)
const owner = computed(() => auth.can('branches.manage'))
// The owner assigns any branch's admin; a Company Admin those of their own branches.
const canAssign = computed(() => owner.value || auth.can('users.branch'))
// Owners and company admins (who set up their own branches) edit everything;
// a branch admin edits the location and Telegram of their branch.
const full = computed(() => owner.value || auth.can('branches.create'))

const branch = useResource(() => branchService.get(id))
const { busy, run } = useAction()

// ---------- details, location (GPS), Telegram ----------
const form = reactive({
  code: '', name: '', address: '', status: 'ACTIVE' as Branch['status'],
  gps: false, required: false, reject: false,
  ownOffice: false, lat: '' as number | '', lng: '' as number | '', radius: 150 as number | '', paste: '',
  token: '', miniapp: '', group: 0, review: 0,
})
function fill(b: Branch) {
  Object.assign(form, {
    code: b.code, name: b.name, address: b.address, status: b.status,
    gps: !!b.gps_enabled, required: !!b.gps_required, reject: !!b.gps_reject_outside,
    ownOffice: b.office_latitude != null, lat: b.office_latitude ?? '', lng: b.office_longitude ?? '', radius: b.allowed_radius_meters ?? 150, paste: '',
    token: '', miniapp: b.miniapp_url, group: b.telegram_group_id, review: b.telegram_review_chat_id,
  })
}
watch(branch.data, b => b && fill(b))

async function save() {
  const b = branch.data.value!
  // A branch admin may only send the location and Telegram fields; the API enforces the same.
  const input: BranchInput = {
    gps: { enabled: form.gps, required: form.gps && form.required, reject_outside: form.gps && form.reject },
    miniapp_url: form.miniapp.trim(),
    telegram_group_id: form.group || 0,
    telegram_review_chat_id: form.review || 0,
  }
  if (form.ownOffice) Object.assign(input, { office_latitude: Number(form.lat), office_longitude: Number(form.lng), allowed_radius_meters: Number(form.radius) })
  else if (b.office_latitude != null) input.clear_office = true
  if (form.token.trim()) input.telegram_bot_token = form.token.trim()
  if (full.value) Object.assign(input, { code: form.code.trim(), name: form.name.trim(), address: form.address.trim(), status: form.status })
  await run('save', async () => {
    branch.data.value = await branchService.update(id, input)
    lookups.branches = null
  }, 'Branch saved')
}

// ---------- location helpers ----------
/** "11.5564, 104.9282" or a Google Maps / OpenStreetMap link → coordinates. */
function parseLocation(text: string): { lat: number; lng: number } | null {
  const t = text.trim()
  const patterns = [/@(-?\d+\.\d+),(-?\d+\.\d+)/, /!3d(-?\d+\.\d+)!4d(-?\d+\.\d+)/, /[?&](?:q|query|ll|mlat)=(-?\d+\.\d+)(?:,|&mlon=)(-?\d+\.\d+)/, /^(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)$/]
  for (const re of patterns) {
    const m = t.match(re)
    if (m) {
      const lat = Number(m[1])
      const lng = Number(m[2])
      if (Math.abs(lat) <= 90 && Math.abs(lng) <= 180) return { lat, lng }
    }
  }
  return null
}
watch(() => form.paste, v => {
  const p = parseLocation(v)
  if (p) Object.assign(form, { lat: p.lat, lng: p.lng, ownOffice: true })
})
const mapUrl = (lat: number | '', lng: number | '') => mapsUrl(lat, lng)
const locating = ref(false)
function useMyLocation() {
  if (!navigator.geolocation) return
  locating.value = true
  navigator.geolocation.getCurrentPosition(
    p => {
      Object.assign(form, { lat: Number(p.coords.latitude.toFixed(6)), lng: Number(p.coords.longitude.toFixed(6)) })
      locating.value = false
    },
    () => (locating.value = false),
    { enableHighAccuracy: true, timeout: 15000 },
  )
}

// ---------- Telegram ----------
// The admin panel and the Mini App are served by the same server.
const thisServerApp = typeof window === 'undefined' ? '' : new URL('/app/', window.location.origin).href
async function removeBot() {
  const b = branch.data.value!
  const ok = await confirm({
    title: `Remove the bot of ${b.name}?`,
    message: 'Its staff can no longer open the Mini App from it, and the branch gets no Telegram messages until a new bot is set.',
    confirmLabel: 'Remove bot',
  })
  if (!ok) return
  await run('bot', async () => {
    branch.data.value = await branchService.update(id, { telegram_bot_token: '' })
  }, 'Bot removed')
}

// ---------- branch admin (owner only): a system user, never an employee ----------
const sysUsers = useResource(() => accessService.users(), { immediate: false })
onMounted(() => canAssign.value && sysUsers.load())
const candidates = computed(() => (sysUsers.data.value ?? []).filter(u => u.status === 'ACTIVE' && u.role !== 'super_admin'))
const chosen = ref('')
const chosenUser = computed(() => candidates.value.find(u => String(u.person_id) === chosen.value))
const candidateOptions = computed((): SelectOption[] =>
  candidates.value.map(u => ({
    value: String(u.person_id),
    label: u.full_name,
    sub: [u.role_name, u.branch_name].filter(Boolean).join(' · '),
    disabled: u.person_id === branch.data.value?.admin_employee_id,
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
    lookups.branches = null // the branch's admin changed
    sysUsers.load()
  }
}

// ---------- the branch's own setup lists ----------
const setup = computed(() => [
  { to: '/employees/departments', label: 'Departments', perm: auth.can('staff.manage') },
  { to: '/employees/positions', label: 'Positions', perm: auth.can('staff.manage') },
  { to: '/schedules/shifts', label: 'Shifts', perm: auth.can('schedules.manage') },
  { to: '/leave/types', label: 'Leave types', perm: auth.can('leave.manage') },
  { to: '/schedules/holidays', label: 'Holidays', perm: auth.can('schedules.manage') },
].filter(l => l.perm))
</script>

<template>
  <div>
    <NuxtLink v-if="full" to="/branches" class="link text-sm">← Branches</NuxtLink>
    <StateView :loading="branch.loading.value && !branch.data.value" :error="branch.error.value" @retry="branch.load">
      <template v-if="branch.data.value">
        <PageHeader class="mt-3" :title="branch.data.value.name" :subtitle="`${branch.data.value.code} · ${branch.data.value.staff_count} staff`">
          <AppButton variant="secondary" @click="navigateTo({ path: '/employees', query: { branch_id: String(id) } })">Staff</AppButton>
          <AppButton :loading="busy === 'save'" @click="save">Save changes</AppButton>
        </PageHeader>

        <form class="grid gap-4 lg:grid-cols-2" @submit.prevent="save">
          <section v-if="full" class="card grid gap-4 p-5 sm:grid-cols-3">
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
                <NuxtLink v-if="canAssign" to="/access/users" class="font-medium hover:underline">{{ branch.data.value.admin.full_name }}</NuxtLink>
                <span v-else class="font-medium">{{ branch.data.value.admin.full_name }}</span>
                <span class="block text-xs text-muted">System user · {{ branch.data.value.admin.email || 'Branch Admin' }}</span>
              </div>
              <AppButton v-if="canAssign" size="sm" variant="danger" :loading="busy === 'admin'" @click="assign(null)">Remove</AppButton>
            </div>
            <p v-else class="text-sm text-warning">This branch has no admin. Review requests go to managers and the admins who reach this branch.</p>
            <template v-if="canAssign">
              <FormField label="Assign a new admin" for="b-admin" hint="Choose a system user: they become this branch's Branch Admin. A replaced admin keeps the account without panel powers until given another role.">
                <div class="flex gap-2">
                  <SearchSelect id="b-admin" v-model="chosen" :options="candidateOptions" placeholder="Choose a system user…" class="min-w-0 flex-1" />
                  <AppButton :disabled="!chosen" :loading="busy === 'admin'" @click="assign(Number(chosen))">Assign</AppButton>
                </div>
              </FormField>
              <p v-if="chosenUser && chosenUser.scope === 'all'" class="text-xs text-warning">
                {{ chosenUser.full_name }} is company-wide ({{ chosenUser.role_name }}); assigning limits them to this branch.
              </p>
              <p class="text-xs text-muted">Not listed? <NuxtLink to="/access/users" class="link">Add a system user</NuxtLink> with the Branch Admin role.</p>
            </template>
          </section>

          <section class="card grid content-start gap-4 p-5 sm:grid-cols-3">
            <div class="sm:col-span-3">
              <h2 class="text-sm font-semibold">Location (GPS)</h2>
              <p class="mt-0.5 text-xs text-muted">Whether this branch's staff share their location when they check in or out.</p>
            </div>
            <fieldset class="flex flex-col gap-2 text-sm sm:col-span-3">
              <legend class="sr-only">GPS rules</legend>
              <label class="flex items-center gap-2"><input v-model="form.gps" type="checkbox" class="size-4 accent-[var(--primary)]" /> Ask for location at check-in / check-out</label>
              <label class="flex items-center gap-2 pl-6" :class="!form.gps && 'opacity-50'"><input v-model="form.required" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!form.gps" /> Location is required</label>
              <label class="flex items-center gap-2 pl-6" :class="!form.gps && 'opacity-50'"><input v-model="form.reject" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!form.gps" /> Refuse check-ins outside the office radius</label>
            </fieldset>
            <label class="flex items-center gap-2 text-sm sm:col-span-3">
              <input v-model="form.ownOffice" type="checkbox" class="size-4 accent-[var(--primary)]" />
              Office location set
            </label>
            <template v-if="form.ownOffice">
              <FormField label="Latitude" for="b-lat"><input id="b-lat" v-model.number="form.lat" class="input" type="number" step="any" min="-90" max="90" required /></FormField>
              <FormField label="Longitude" for="b-lng"><input id="b-lng" v-model.number="form.lng" class="input" type="number" step="any" min="-180" max="180" required /></FormField>
              <FormField label="Radius (m)" for="b-radius"><input id="b-radius" v-model.number="form.radius" class="input" type="number" min="10" max="100000" required /></FormField>
              <div class="flex flex-wrap items-center gap-2 sm:col-span-3">
                <AppButton variant="secondary" size="sm" :loading="locating" @click="useMyLocation">Use my current location</AppButton>
                <a v-if="form.lat !== '' && form.lng !== ''" :href="mapUrl(form.lat, form.lng)" target="_blank" rel="noopener" class="text-sm text-primary hover:underline">Check on Google Maps ↗</a>
              </div>
            </template>
            <FormField label="Paste a map link or “lat, lng”" for="b-paste" class="sm:col-span-3" hint="A Google Maps or OpenStreetMap link to the office fills in the coordinates.">
              <input id="b-paste" v-model="form.paste" class="input" placeholder="https://maps.google.com/…  or  13.3633, 103.8564" />
            </FormField>
            <p v-if="form.gps && form.reject && !form.ownOffice" class="text-xs text-warning sm:col-span-3">Refusing check-ins outside the office needs the office location.</p>
            <p v-else-if="form.gps && !form.ownOffice" class="text-xs text-muted sm:col-span-3">Without an office location the position is recorded but not checked.</p>
          </section>

          <section class="card grid content-start gap-4 p-5 sm:grid-cols-2">
            <div class="flex flex-wrap items-center justify-between gap-2 sm:col-span-2">
              <div>
                <h2 class="text-sm font-semibold">Telegram</h2>
                <p class="mt-0.5 text-xs text-muted">This branch's own bot: its staff open the Mini App from it and get its messages.</p>
              </div>
              <StatusBadge
                :tone="branch.data.value.telegram_bot_set ? 'success' : 'warning'"
                :label="branch.data.value.telegram_bot_set ? `Connected${branch.data.value.telegram_bot_username ? ` · @${branch.data.value.telegram_bot_username}` : ''}` : 'No bot'"
              />
            </div>
            <FormField
              :label="branch.data.value.telegram_bot_set ? 'Replace bot token' : 'Bot token'" for="b-token" class="sm:col-span-2"
              hint="From @BotFather (/newbot). Checked with Telegram when you save; stored encrypted and never shown again."
            >
              <div class="flex gap-2">
                <input
                  id="b-token" v-model="form.token" class="input min-w-0 flex-1 font-mono" type="password" autocomplete="off" spellcheck="false"
                  :placeholder="branch.data.value.telegram_bot_set ? '•••••••• (saved — paste a new one to replace)' : '123456789:AA…'"
                />
                <AppButton v-if="branch.data.value.telegram_bot_set" variant="ghost" :loading="busy === 'bot'" @click="removeBot">Remove</AppButton>
              </div>
            </FormField>
            <FormField label="Mini App URL" for="b-app" class="sm:col-span-2" hint="The bot's “Open App” button opens this. Must be https://.">
              <input id="b-app" v-model="form.miniapp" class="input font-mono" type="url" placeholder="https://your-server/app/" />
              <button
                v-if="thisServerApp.startsWith('https://') && form.miniapp !== thisServerApp" type="button"
                class="mt-1 text-xs text-primary hover:underline" @click="form.miniapp = thisServerApp"
              >Use this server: {{ thisServerApp }}</button>
            </FormField>
            <FormField label="Group chat ID" for="b-group" hint="Gets this branch's daily report. 0 = none.">
              <input id="b-group" v-model.number="form.group" class="input font-mono" type="number" step="1" />
            </FormField>
            <FormField label="Review chat ID" for="b-review" hint="Optional group for review requests. 0 = message the branch admin and managers.">
              <input id="b-review" v-model.number="form.review" class="input font-mono" type="number" step="1" />
            </FormField>
            <p class="text-xs text-muted sm:col-span-2">Add the bot to a group, then send a message there; the group's ID (it starts with -100) is in the bot's updates.</p>
          </section>
        </form>

        <section v-if="setup.length" class="card mt-4 p-5">
          <h2 class="text-sm font-semibold">This branch's setup</h2>
          <p class="mt-0.5 text-xs text-muted">Every branch has its own lists; nothing is shared between branches.</p>
          <div class="mt-3 flex flex-wrap gap-2">
            <NuxtLink v-for="l in setup" :key="l.to" :to="{ path: l.to, query: { branch_id: String(id) } }" class="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs font-medium hover:bg-surface-2">{{ l.label }}</NuxtLink>
          </div>
        </section>
      </template>
    </StateView>
  </div>
</template>
