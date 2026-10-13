<script setup lang="ts">
import { settingsService } from '~/services/settings.service'
import { useAuthStore } from '~/stores/auth'
import type { CompanySettings, SettingsUpdate } from '~/types/api'
import { formatDate, formatDateTime } from '~/utils/format'

definePageMeta({ permission: 'settings.view' })

const auth = useAuthStore()
const canEdit = computed(() => auth.can('settings.edit'))
const resp = useResource(() => settingsService.get())
const current = computed(() => resp.data.value?.settings)
const form = reactive({} as CompanySettings)
watch(current, s => s && Object.assign(form, s))

// The bot token is write-only: the API only says whether one is set.
const newToken = ref('')
const readOnlyKeys = new Set(['telegram_bot_token_set', 'telegram_bot_username'])
const changes = computed<SettingsUpdate>(() => {
  const s = current.value
  if (!s) return {}
  const out = Object.fromEntries(
    Object.entries(form).filter(([k, v]) => !readOnlyKeys.has(k) && v !== s[k as keyof CompanySettings]),
  ) as SettingsUpdate
  if (newToken.value.trim()) out.telegram_bot_token = newToken.value.trim()
  return out
})
const dirty = computed(() => Object.keys(changes.value).length > 0)

const { busy, run } = useAction()
async function apply(update: SettingsUpdate, message: string) {
  return run('save', async () => {
    const saved = await settingsService.update(update)
    resp.data.value = saved
    newToken.value = ''
    auth.company = { name: saved.settings.company_name, timezone: saved.settings.timezone } // sidebar + time formatting
  }, message)
}
const save = () => apply(changes.value, 'Settings saved')
const removeToken = () => apply({ telegram_bot_token: '' }, 'Bot token removed')

const zones: string[] = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []
const zoneOptions = zones.map(z => ({ value: z, label: z.replaceAll('_', ' ') }))

// ---------- holidays ----------
const year = ref(new Date().getFullYear())
const holidays = useResource(() => settingsService.holidays(year.value))
watch(year, () => holidays.load())
const newHoliday = reactive({ date: '', name: '' })
async function addHoliday() {
  if (await run('holiday', () => settingsService.addHoliday(newHoliday.date, newHoliday.name.trim()), 'Holiday added')) {
    Object.assign(newHoliday, { date: '', name: '' })
    holidays.load()
  }
}
async function removeHoliday(id: number) {
  if (await run(`h${id}`, () => settingsService.deleteHoliday(id), 'Holiday removed')) holidays.load()
}
</script>

<template>
  <div>
    <PageHeader title="Settings" :subtitle="canEdit ? 'Changes apply within a minute; no restart needed.' : 'Only a super admin can change settings.'">
      <AppButton v-if="canEdit" :disabled="!dirty" :loading="busy === 'save'" @click="save">Save changes</AppButton>
    </PageHeader>

    <StateView :loading="resp.loading.value && !current" :error="resp.error.value" @retry="resp.load">
      <form v-if="current" class="grid gap-4 lg:grid-cols-2" @submit.prevent="save">
        <fieldset class="card grid gap-4 p-5 sm:grid-cols-2 lg:col-span-2" :disabled="!canEdit">
          <legend class="sr-only">Telegram</legend>
          <div class="flex flex-wrap items-center justify-between gap-2 sm:col-span-2">
            <h2 class="text-sm font-semibold">Telegram</h2>
            <StatusBadge
              v-if="current.telegram_bot_token_set"
              tone="success"
              :label="current.telegram_bot_username ? `Connected: @${current.telegram_bot_username}` : 'Connected'"
            />
            <StatusBadge v-else tone="warning" label="Not connected — the Mini App and notifications are off" />
          </div>
          <FormField
            label="Bot token"
            for="s-token"
            class="sm:col-span-2"
            hint="From @BotFather → your bot → API Token. Checked with Telegram before saving and stored encrypted; it is never shown again."
          >
            <div class="flex gap-2">
              <input
                id="s-token"
                v-model="newToken"
                class="input font-mono"
                type="password"
                autocomplete="off"
                :placeholder="current.telegram_bot_token_set ? '•••••••••• (set — paste a new token to replace it)' : '123456789:AA…'"
              />
              <AppButton v-if="canEdit && current.telegram_bot_token_set" variant="danger" :loading="busy === 'save'" @click="removeToken">Remove</AppButton>
            </div>
          </FormField>
          <FormField label="Mini App URL" for="s-url" class="sm:col-span-2" hint="Where the employee app is served, e.g. https://attendance.example.com/app/ — the bot's Open App button opens it.">
            <input id="s-url" v-model.trim="form.miniapp_url" class="input" type="url" placeholder="https://…/app/" />
          </FormField>
          <FormField label="Company group chat ID" for="s-group" hint="Gets the company-wide daily report. 0 = none. Each branch can also have its own group.">
            <input id="s-group" v-model.number="form.telegram_group_id" class="input font-mono" type="number" step="1" />
          </FormField>
          <FormField label="Review chat ID" for="s-review" hint="Optional group for review requests from branches without their own. 0 = message the branch admin / manager directly.">
            <input id="s-review" v-model.number="form.telegram_review_chat_id" class="input font-mono" type="number" step="1" />
          </FormField>
          <p class="text-xs text-muted sm:col-span-2">
            Finding a group's chat ID: add the bot to the group, send a message there, then open
            <code class="rounded bg-surface-2 px-1">https://api.telegram.org/bot&lt;token&gt;/getUpdates</code> and copy the <code>chat.id</code> (it starts with -100).
          </p>
        </fieldset>

        <fieldset class="card grid gap-4 p-5 sm:grid-cols-2" :disabled="!canEdit">
          <legend class="sr-only">Company</legend>
          <h2 class="text-sm font-semibold sm:col-span-2">Company</h2>
          <FormField label="Company name" for="s-name" class="sm:col-span-2"><input id="s-name" v-model="form.company_name" class="input" required /></FormField>
          <FormField label="Timezone" for="s-tz" class="sm:col-span-2" hint="Work dates, late minutes and reports use this timezone.">
            <SearchSelect v-if="zones.length" id="s-tz" v-model="form.timezone" :options="zoneOptions" searchable />
            <input v-else id="s-tz" v-model="form.timezone" class="input" />
          </FormField>
          <label class="flex items-center gap-2 text-sm sm:col-span-2">
            <input v-model="form.confirmation_required" type="checkbox" class="size-4 accent-[var(--primary)]" />
            Check-ins need a reviewer's confirmation
          </label>
        </fieldset>

        <fieldset class="card grid gap-4 p-5 sm:grid-cols-2" :disabled="!canEdit">
          <legend class="sr-only">Location</legend>
          <h2 class="text-sm font-semibold sm:col-span-2">Location (GPS)</h2>
          <label class="flex items-center gap-2 text-sm sm:col-span-2"><input v-model="form.gps_enabled" type="checkbox" class="size-4 accent-[var(--primary)]" /> Ask for location at check-in/out</label>
          <label class="flex items-center gap-2 text-sm" :class="{ 'opacity-50': !form.gps_enabled }"><input v-model="form.gps_required" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!form.gps_enabled" /> Location is required</label>
          <label class="flex items-center gap-2 text-sm" :class="{ 'opacity-50': !form.gps_enabled }"><input v-model="form.gps_reject_outside" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="!form.gps_enabled" /> Refuse outside the office radius</label>
          <FormField label="Office latitude" for="s-lat"><input id="s-lat" v-model.number="form.office_latitude" class="input" type="number" step="any" min="-90" max="90" /></FormField>
          <FormField label="Office longitude" for="s-lng"><input id="s-lng" v-model.number="form.office_longitude" class="input" type="number" step="any" min="-180" max="180" /></FormField>
          <FormField label="Allowed radius (m)" for="s-radius"><input id="s-radius" v-model.number="form.allowed_radius_meters" class="input" type="number" min="10" max="100000" /></FormField>
        </fieldset>

        <fieldset class="card grid gap-4 p-5 sm:grid-cols-3 lg:col-span-2" :disabled="!canEdit">
          <legend class="sr-only">Scheduled jobs</legend>
          <h2 class="text-sm font-semibold sm:col-span-3">Scheduled jobs (company time)</h2>
          <FormField label="Missing check-in alert" for="s-alert" hint="Reviewers are told who hasn't checked in."><input id="s-alert" v-model="form.missing_checkin_alert_time" class="input" type="time" /></FormField>
          <FormField label="Daily report" for="s-report" hint="Posted to the Telegram group."><input id="s-report" v-model="form.daily_report_time" class="input" type="time" /></FormField>
          <FormField label="Close the day" for="s-close" hint="No check-in by then = absent."><input id="s-close" v-model="form.day_close_time" class="input" type="time" /></FormField>
          <label class="flex items-center gap-2 text-sm sm:col-span-3"><input v-model="form.daily_report_enabled" type="checkbox" class="size-4 accent-[var(--primary)]" /> Post the daily report</label>
        </fieldset>
      </form>
      <p v-if="resp.data.value?.backup" class="mt-4 text-sm" :class="resp.data.value.backup.ok ? 'text-muted' : 'text-danger'">
        Last database backup:
        <b>{{ resp.data.value.backup.ok ? 'OK' : 'FAILED' }}</b>
        {{ formatDateTime(resp.data.value.backup.at, auth.company.timezone) }}
        <template v-if="resp.data.value.backup.size"> · {{ resp.data.value.backup.size }}</template>
      </p>
    </StateView>

    <section class="card mt-6 p-5">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-sm font-semibold">Public holidays</h2>
        <div class="flex items-center gap-2">
          <AppButton size="sm" variant="secondary" aria-label="Previous year" @click="year--">‹</AppButton>
          <span class="tabular-nums text-sm font-medium">{{ year }}</span>
          <AppButton size="sm" variant="secondary" aria-label="Next year" @click="year++">›</AppButton>
        </div>
      </div>
      <p class="mb-3 text-sm text-muted">Nobody is marked absent on a holiday, and leave does not count it.</p>
      <form v-if="canEdit" class="mb-4 flex flex-wrap items-end gap-2" @submit.prevent="addHoliday">
        <FormField label="Date" for="h-date"><input id="h-date" v-model="newHoliday.date" class="input" type="date" required /></FormField>
        <FormField label="Name" for="h-name" class="min-w-48 flex-1"><input id="h-name" v-model="newHoliday.name" class="input" required maxlength="150" /></FormField>
        <AppButton type="submit" :loading="busy === 'holiday'">Add holiday</AppButton>
      </form>
      <StateView :loading="holidays.loading.value" :error="holidays.error.value" :empty="holidays.data.value?.length === 0" :empty-text="`No holidays in ${year}.`" @retry="holidays.load">
        <ul class="divide-y divide-border">
          <li v-for="h in holidays.data.value ?? []" :key="h.id" class="flex items-center justify-between gap-3 py-2 text-sm">
            <span><span class="inline-block w-32 tabular-nums text-muted">{{ formatDate(h.date, true) }}</span> {{ h.name }}</span>
            <AppButton v-if="canEdit" size="sm" variant="ghost" :loading="busy === `h${h.id}`" :aria-label="`Remove ${h.name}`" @click="removeHoliday(h.id)">Remove</AppButton>
          </li>
        </ul>
      </StateView>
    </section>
  </div>
</template>
