<script setup lang="ts">
import { branchService } from '~/services/branch.service'
import { settingsService } from '~/services/settings.service'
import { useAuthStore } from '~/stores/auth'
import type { Branch } from '~/types/api'

definePageMeta({ permission: ['settings.view', 'branch.settings'] })

const auth = useAuthStore()
const canCompany = computed(() => auth.can('settings.view'))
const canEditCompany = computed(() => auth.can('settings.edit'))
const settings = useResource(() => (canCompany.value ? settingsService.get() : Promise.resolve(null)))
const branches = useResource(() => branchService.list())
const { busy, run } = useAction()

// ---------- shared helpers ----------
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
const mapUrl = (lat: number, lng: number) => `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lng}#map=17/${lat}/${lng}`
const locating = ref(false)
function here(set: (lat: number, lng: number) => void) {
  if (!navigator.geolocation) return
  locating.value = true
  navigator.geolocation.getCurrentPosition(
    p => {
      set(Number(p.coords.latitude.toFixed(6)), Number(p.coords.longitude.toFixed(6)))
      locating.value = false
    },
    () => (locating.value = false),
    { enableHighAccuracy: true, timeout: 15000 },
  )
}

// ---------- company default ----------
const company = reactive({ gps_enabled: false, gps_required: false, gps_reject_outside: false, office_latitude: 0, office_longitude: 0, allowed_radius_meters: 150 })
watch(() => settings.data.value?.settings, s => s && Object.assign(company, {
  gps_enabled: s.gps_enabled, gps_required: s.gps_required, gps_reject_outside: s.gps_reject_outside,
  office_latitude: s.office_latitude, office_longitude: s.office_longitude, allowed_radius_meters: s.allowed_radius_meters,
}), { immediate: true })
const companyPaste = ref('')
watch(companyPaste, v => {
  const p = parseLocation(v)
  if (p) Object.assign(company, { office_latitude: p.lat, office_longitude: p.lng })
})
async function saveCompany() {
  const ok = await run('company', () => settingsService.update({
    gps_enabled: company.gps_enabled, gps_required: company.gps_enabled && company.gps_required, gps_reject_outside: company.gps_enabled && company.gps_reject_outside,
    office_latitude: Number(company.office_latitude), office_longitude: Number(company.office_longitude), allowed_radius_meters: Number(company.allowed_radius_meters),
  }), 'Company GPS settings saved')
  if (ok) settings.load()
}

// ---------- per branch ----------
function rules(b: Branch) {
  if (b.gps_enabled === null) return { custom: false, text: 'Company default' }
  if (!b.gps_enabled) return { custom: true, text: 'Off' }
  return { custom: true, text: ['On', b.gps_required && 'required', b.gps_reject_outside && 'refuse outside'].filter(Boolean).join(' · ') }
}
const canEditBranch = (b: Branch) => auth.can('branches.manage') || (auth.can('branch.settings'))
const editing = ref<Branch | null>(null)
const form = reactive({ custom: false, enabled: true, required: false, reject_outside: false, ownOffice: false, lat: 0, lng: 0, radius: 150, paste: '' })
const editOpen = computed({ get: () => !!editing.value, set: v => !v && (editing.value = null) })
const modeOptions = [{ value: 'default', label: 'Company default' }, { value: 'custom', label: 'Custom for this branch' }]
const officeOptions = [{ value: 'company', label: 'Company office' }, { value: 'own', label: 'Own location' }]
const mode = computed({ get: () => (form.custom ? 'custom' : 'default'), set: v => (form.custom = v === 'custom') })
const office = computed({ get: () => (form.ownOffice ? 'own' : 'company'), set: v => (form.ownOffice = v === 'own') })
function edit(b: Branch) {
  editing.value = b
  const s = settings.data.value?.settings
  Object.assign(form, {
    custom: b.gps_enabled !== null, enabled: b.gps_enabled ?? true, required: !!b.gps_required, reject_outside: !!b.gps_reject_outside,
    ownOffice: b.office_latitude !== null, lat: b.office_latitude ?? s?.office_latitude ?? 0, lng: b.office_longitude ?? s?.office_longitude ?? 0,
    radius: b.allowed_radius_meters ?? s?.allowed_radius_meters ?? 150, paste: '',
  })
}
watch(() => form.paste, v => {
  const p = parseLocation(v)
  if (p) Object.assign(form, { lat: p.lat, lng: p.lng, ownOffice: true })
})
async function saveBranch() {
  const b = editing.value!
  const input = {
    gps: form.custom ? { enabled: form.enabled, required: form.required, reject_outside: form.reject_outside } : { inherit: true },
    ...(form.ownOffice
      ? { office_latitude: Number(form.lat), office_longitude: Number(form.lng), allowed_radius_meters: Number(form.radius) }
      : { use_company_office: true }),
  }
  if (await run('branch', () => branchService.update(b.id, input), `${b.name}: location saved`)) {
    editing.value = null
    branches.load()
  }
}
</script>

<template>
  <div>
    <PageHeader title="Location (GPS)" subtitle="Where people may check in. Each branch can follow the company default or have its own GPS rules and office location." />

    <section v-if="canCompany" class="card mb-6 p-5">
      <h2 class="mb-3 text-sm font-semibold">Company default</h2>
      <StateView :loading="settings.loading.value && !settings.data.value" :error="settings.error.value" @retry="settings.load">
        <form class="grid gap-4 sm:grid-cols-3" @submit.prevent="saveCompany">
          <fieldset class="flex flex-col gap-2 text-sm sm:col-span-3" :disabled="!canEditCompany">
            <label class="flex items-center gap-2"><input v-model="company.gps_enabled" type="checkbox" class="size-4 accent-[var(--primary)]" /> Ask for location at check-in / check-out</label>
            <label class="flex items-center gap-2 pl-6" :class="!company.gps_enabled && 'opacity-50'"><input v-model="company.gps_required" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!company.gps_enabled" /> Location is required</label>
            <label class="flex items-center gap-2 pl-6" :class="!company.gps_enabled && 'opacity-50'"><input v-model="company.gps_reject_outside" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!company.gps_enabled" /> Refuse check-ins outside the office radius</label>
          </fieldset>
          <FormField label="Office latitude" for="c-lat"><input id="c-lat" v-model.number="company.office_latitude" class="input" type="number" step="any" min="-90" max="90" :disabled="!canEditCompany" /></FormField>
          <FormField label="Office longitude" for="c-lng"><input id="c-lng" v-model.number="company.office_longitude" class="input" type="number" step="any" min="-180" max="180" :disabled="!canEditCompany" /></FormField>
          <FormField label="Allowed radius (m)" for="c-radius"><input id="c-radius" v-model.number="company.allowed_radius_meters" class="input" type="number" min="10" max="100000" :disabled="!canEditCompany" /></FormField>
          <div v-if="canEditCompany" class="flex flex-wrap items-end gap-2 sm:col-span-3">
            <FormField label="Paste a map link or “lat, lng”" for="c-paste" class="min-w-64 flex-1">
              <input id="c-paste" v-model="companyPaste" class="input" placeholder="https://maps.google.com/…@11.5564,104.9282… or 11.5564, 104.9282" />
            </FormField>
            <AppButton variant="secondary" :loading="locating" @click="here((lat, lng) => Object.assign(company, { office_latitude: lat, office_longitude: lng }))">Use my current location</AppButton>
            <a :href="mapUrl(company.office_latitude, company.office_longitude)" target="_blank" rel="noopener" class="px-2 py-2 text-sm text-primary hover:underline">Check on map ↗</a>
            <AppButton type="submit" class="ml-auto" :loading="busy === 'company'">Save</AppButton>
          </div>
        </form>
      </StateView>
    </section>

    <section class="card overflow-hidden">
      <h2 class="border-b border-border px-4 py-3 text-sm font-semibold">Branches</h2>
      <StateView :loading="branches.loading.value && !branches.data.value" :error="branches.error.value" :empty="branches.data.value?.length === 0" empty-text="No branches." @retry="branches.load">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Branch</th><th>GPS rules</th><th>Office location</th><th>Radius</th><th class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="b in branches.data.value ?? []" :key="b.id">
                <td><span class="font-medium">{{ b.name }}</span><span class="block text-xs text-muted">{{ b.code }} · {{ b.staff_count }} staff</span></td>
                <td>
                  <StatusBadge :tone="rules(b).custom ? (b.gps_enabled ? 'success' : 'neutral') : 'info'" :label="rules(b).text" />
                </td>
                <td class="text-sm">
                  <template v-if="b.office_latitude !== null && b.office_longitude !== null">
                    <a :href="mapUrl(b.office_latitude, b.office_longitude)" target="_blank" rel="noopener" class="tabular-nums text-primary hover:underline">
                      {{ b.office_latitude.toFixed(5) }}, {{ b.office_longitude.toFixed(5) }} ↗
                    </a>
                  </template>
                  <span v-else class="text-muted">Company office</span>
                </td>
                <td class="tabular-nums text-sm">{{ b.allowed_radius_meters !== null ? `${b.allowed_radius_meters} m` : '—' }}</td>
                <td class="text-right">
                  <AppButton v-if="canEditBranch(b)" size="sm" variant="ghost" :aria-label="`Edit location of ${b.name}`" @click="edit(b)">Edit</AppButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </StateView>
    </section>

    <AppModal v-model="editOpen" :title="editing ? `${editing.name} · Location (GPS)` : 'Location'" wide>
      <form id="loc-form" class="grid gap-4" @submit.prevent="saveBranch">
        <FormField label="GPS rules"><Segmented v-model="mode" :options="modeOptions" label="GPS rules" /></FormField>
        <fieldset v-if="form.custom" class="flex flex-col gap-2 rounded-lg border border-border p-3 text-sm">
          <label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" class="size-4 accent-[var(--primary)]" /> Ask for location at check-in / check-out</label>
          <label class="flex items-center gap-2 pl-6" :class="!form.enabled && 'opacity-50'"><input v-model="form.required" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!form.enabled" /> Location is required</label>
          <label class="flex items-center gap-2 pl-6" :class="!form.enabled && 'opacity-50'"><input v-model="form.reject_outside" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!form.enabled" /> Refuse check-ins outside the office radius</label>
        </fieldset>
        <FormField label="Office location"><Segmented v-model="office" :options="officeOptions" label="Office location" /></FormField>
        <div v-if="form.ownOffice" class="grid gap-3 sm:grid-cols-3">
          <FormField label="Latitude" for="b-lat"><input id="b-lat" v-model.number="form.lat" class="input" type="number" step="any" min="-90" max="90" required /></FormField>
          <FormField label="Longitude" for="b-lng"><input id="b-lng" v-model.number="form.lng" class="input" type="number" step="any" min="-180" max="180" required /></FormField>
          <FormField label="Allowed radius (m)" for="b-radius"><input id="b-radius" v-model.number="form.radius" class="input" type="number" min="10" max="100000" required /></FormField>
          <div class="flex flex-wrap items-center gap-2 sm:col-span-3">
            <AppButton variant="secondary" :loading="locating" @click="here((lat, lng) => Object.assign(form, { lat, lng }))">Use my current location</AppButton>
            <a :href="mapUrl(form.lat, form.lng)" target="_blank" rel="noopener" class="text-sm text-primary hover:underline">Check on map ↗</a>
          </div>
        </div>
        <FormField label="Paste a map link or “lat, lng”" for="b-paste" hint="A Google Maps or OpenStreetMap link to the office fills in the coordinates.">
          <input id="b-paste" v-model="form.paste" class="input" placeholder="https://maps.google.com/…  or  13.3633, 103.8564" />
        </FormField>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="editing = null">Cancel</AppButton>
        <AppButton type="submit" form="loc-form" :loading="busy === 'branch'">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
