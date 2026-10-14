<script setup lang="ts">
import { settingsService } from '~/services/settings.service'
import { useAuthStore } from '~/stores/auth'
import type { CompanySettings, SettingsUpdate } from '~/types/api'
import { formatDateTime } from '~/utils/format'

definePageMeta({ permission: 'settings.view' })

const auth = useAuthStore()
const canEdit = computed(() => auth.can('settings.edit'))
const resp = useResource(() => settingsService.get())
const current = computed(() => resp.data.value?.settings)
const form = reactive({} as CompanySettings)
watch(current, s => s && Object.assign(form, s))

const changes = computed<SettingsUpdate>(() => {
  const s = current.value
  if (!s) return {}
  return Object.fromEntries(Object.entries(form).filter(([k, v]) => v !== s[k as keyof CompanySettings])) as SettingsUpdate
})
const dirty = computed(() => Object.keys(changes.value).length > 0)

const { busy, run } = useAction()
async function apply(update: SettingsUpdate, message: string) {
  return run('save', async () => {
    const saved = await settingsService.update(update)
    resp.data.value = saved
    auth.company = { name: saved.settings.company_name, timezone: saved.settings.timezone } // sidebar + time formatting
  }, message)
}
const save = () => apply(changes.value, 'Settings saved')

const zones: string[] = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []
const zoneOptions = zones.map(z => ({ value: z, label: z.replaceAll('_', ' ') }))

</script>

<template>
  <div>
    <PageHeader title="Settings" :subtitle="canEdit ? 'Changes apply within a minute; no restart needed.' : 'Only a super admin can change settings.'">
      <AppButton v-if="canEdit" :disabled="!dirty" :loading="busy === 'save'" @click="save">Save changes</AppButton>
    </PageHeader>

    <StateView :loading="resp.loading.value && !current" :error="resp.error.value" @retry="resp.load">
      <form v-if="current" class="grid gap-4 lg:grid-cols-2" @submit.prevent="save">
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

        <section class="card flex flex-col justify-between gap-3 p-5">
          <div>
            <h2 class="text-sm font-semibold">Each branch runs on its own</h2>
            <p class="mt-1 text-sm text-muted">
              Telegram (its own bot and chats) and Location (GPS rules and office) are set on each branch's page;
              departments, positions, shifts, leave types and holidays belong to one branch too.
            </p>
          </div>
          <NuxtLink to="/branches" class="link text-sm font-medium">Open Branches →</NuxtLink>
        </section>

        <fieldset class="card grid gap-4 p-5 sm:grid-cols-3 lg:col-span-2" :disabled="!canEdit">
          <legend class="sr-only">Scheduled jobs</legend>
          <h2 class="text-sm font-semibold sm:col-span-3">Scheduled jobs (company time)</h2>
          <FormField label="Missing check-in alert" for="s-alert" hint="Reviewers are told who hasn't checked in."><input id="s-alert" v-model="form.missing_checkin_alert_time" class="input" type="time" /></FormField>
          <FormField label="Daily report" for="s-report" hint="Posted to each branch's Telegram group."><input id="s-report" v-model="form.daily_report_time" class="input" type="time" /></FormField>
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

    <p class="mt-6 text-sm text-muted">
      Shifts, the weekly roster, special schedules and public holidays are under
      <NuxtLink to="/schedules/shifts" class="link">Schedules</NuxtLink>.
    </p>
  </div>
</template>
