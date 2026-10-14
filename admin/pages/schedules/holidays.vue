<script setup lang="ts">
import { settingsService } from '~/services/settings.service'
import { useAuthStore } from '~/stores/auth'
import { formatDate } from '~/utils/format'

const auth = useAuthStore()
const canEdit = computed(() => auth.can('settings.edit'))
const { busy, run } = useAction()

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
    <PageHeader title="Public holidays" subtitle="Nobody is marked absent on a holiday, and leave does not count it.">
      <div class="flex items-center gap-2">
        <AppButton variant="secondary" aria-label="Previous year" @click="year--">‹</AppButton>
        <span class="w-12 text-center text-sm font-medium tabular-nums">{{ year }}</span>
        <AppButton variant="secondary" aria-label="Next year" @click="year++">›</AppButton>
      </div>
    </PageHeader>

    <section class="card p-5">
      <form v-if="canEdit" class="mb-4 flex flex-wrap items-end gap-2" @submit.prevent="addHoliday">
        <FormField label="Date" for="h-date"><input id="h-date" v-model="newHoliday.date" class="input" type="date" required /></FormField>
        <FormField label="Name" for="h-name" class="min-w-48 flex-1"><input id="h-name" v-model="newHoliday.name" class="input" required maxlength="150" /></FormField>
        <AppButton type="submit" :loading="busy === 'holiday'">Add holiday</AppButton>
      </form>
      <StateView :loading="holidays.loading.value" :error="holidays.error.value" :empty="holidays.data.value?.length === 0" :empty-text="`No holidays in ${year}.`" @retry="holidays.load">
        <ul class="divide-y divide-border">
          <li v-for="h in holidays.data.value ?? []" :key="h.id" class="flex items-center justify-between gap-3 py-2 text-sm">
            <span><span class="inline-block w-36 tabular-nums text-muted">{{ formatDate(h.date, true) }}</span> {{ h.name }}</span>
            <AppButton v-if="canEdit" size="sm" variant="ghost" :loading="busy === `h${h.id}`" :aria-label="`Remove ${h.name}`" @click="removeHoliday(h.id)">Remove</AppButton>
          </li>
        </ul>
      </StateView>
    </section>
  </div>
</template>
