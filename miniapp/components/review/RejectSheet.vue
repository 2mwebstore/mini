<script setup lang="ts">
// Reason picker shared by attendance and leave rejection (presets match the bot's).
const open = defineModel<boolean>({ required: true })
const props = defineProps<{ title: string; presets: string[]; required?: boolean; loading?: boolean }>()
const emit = defineEmits<{ submit: [reason: string] }>()

const custom = ref('')
watch(open, v => v && (custom.value = ''))

function send(reason: string) {
  const r = reason.trim()
  if (props.required && !r) return
  emit('submit', r)
}
</script>

<template>
  <BottomSheet v-model="open" :title="title">
    <div class="flex flex-col gap-2">
      <AppButton v-for="p in presets" :key="p" variant="secondary" block :disabled="loading" @click="send(p)">{{ p }}</AppButton>
    </div>
    <label class="mt-4 block text-[13px] text-hint" for="reject-reason">Or write a reason</label>
    <textarea
      id="reject-reason"
      v-model="custom"
      rows="3"
      maxlength="500"
      class="mt-1 w-full rounded-xl border border-separator p-3 text-[15px] outline-none focus:border-primary"
      placeholder="Reason"
    />
    <AppButton class="mt-3" variant="danger" block :loading="loading" :disabled="required && !custom.trim()" @click="send(custom)">
      Reject
    </AppButton>
  </BottomSheet>
</template>
