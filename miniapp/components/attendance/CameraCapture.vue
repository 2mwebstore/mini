<script setup lang="ts">
import { captureFrame, downscaleFile } from '~/utils/image'

// Takes the attendance photo. Prefers a live camera preview (getUserMedia) so the
// picture is taken now; falls back to the device camera picker where the
// Telegram client's web view has no camera access (e.g. some desktop clients).
const photo = defineModel<Blob | null>({ required: true })

const video = ref<HTMLVideoElement>()
const fileInput = ref<HTMLInputElement>()
const mode = ref<'starting' | 'live' | 'preview' | 'fallback'>('starting')
const facing = ref<'user' | 'environment'>('user')
const previewUrl = ref<string>()
const busy = ref(false)
const problem = ref('')
let stream: MediaStream | null = null

function stopStream() {
  stream?.getTracks().forEach(t => t.stop())
  stream = null
}

async function startCamera() {
  problem.value = ''
  if (!navigator.mediaDevices?.getUserMedia) {
    mode.value = 'fallback'
    return
  }
  mode.value = 'starting'
  try {
    stopStream()
    stream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: facing.value, width: { ideal: 1280 }, height: { ideal: 1280 } },
      audio: false,
    })
    mode.value = 'live'
    await nextTick()
    video.value!.srcObject = stream
    await video.value!.play()
  } catch {
    stopStream()
    mode.value = 'fallback'
  }
}

function showPreview(blob: Blob) {
  if (previewUrl.value) URL.revokeObjectURL(previewUrl.value)
  previewUrl.value = URL.createObjectURL(blob)
  photo.value = blob
  mode.value = 'preview'
}

async function capture() {
  if (!video.value?.videoWidth) return
  busy.value = true
  try {
    const blob = await captureFrame(video.value)
    stopStream()
    showPreview(blob)
  } catch {
    problem.value = 'Could not take the photo. Please try again.'
  } finally {
    busy.value = false
  }
}

async function onFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  ;(e.target as HTMLInputElement).value = ''
  if (!file) return
  busy.value = true
  problem.value = ''
  try {
    showPreview(await downscaleFile(file))
  } catch {
    problem.value = 'That file is not a photo we can use. Please take a new photo.'
  } finally {
    busy.value = false
  }
}

function retake() {
  photo.value = null
  if (previewUrl.value) URL.revokeObjectURL(previewUrl.value)
  previewUrl.value = undefined
  startCamera()
}

function flip() {
  facing.value = facing.value === 'user' ? 'environment' : 'user'
  startCamera()
}

// The page clears the photo when the server refuses it (e.g. PHOTO_REUSED): reopen the camera.
watch(photo, p => {
  if (!p && mode.value === 'preview') retake()
})

onMounted(startCamera)
onBeforeUnmount(() => {
  stopStream()
  if (previewUrl.value) URL.revokeObjectURL(previewUrl.value)
})
</script>

<template>
  <div>
    <div class="relative aspect-[3/4] w-full overflow-hidden rounded-2xl bg-black">
      <video
        v-show="mode === 'live' || mode === 'starting'"
        ref="video"
        class="size-full object-cover"
        :class="{ '-scale-x-100': facing === 'user' }"
        playsinline
        muted
        autoplay
      />
      <img v-if="mode === 'preview' && previewUrl" :src="previewUrl" alt="Your photo" class="size-full object-cover" />
      <div v-if="mode === 'starting'" class="absolute inset-0 flex items-center justify-center text-sm text-white/70">
        Starting camera…
      </div>
      <div v-if="mode === 'fallback'" class="absolute inset-0 flex flex-col items-center justify-center gap-2 p-6 text-center text-white/80">
        <span class="text-4xl" aria-hidden="true">📷</span>
        <p class="text-sm">Take a photo of yourself at work.</p>
      </div>
      <button
        v-if="mode === 'live'"
        type="button"
        class="absolute right-3 top-3 rounded-full bg-black/50 px-3 py-1.5 text-sm text-white"
        @click="flip"
      >Flip</button>
    </div>

    <p v-if="problem" class="mt-2 text-sm text-danger">{{ problem }}</p>

    <div class="mt-3">
      <AppButton v-if="mode === 'live'" block :loading="busy" @click="capture">Take photo</AppButton>
      <AppButton v-else-if="mode === 'fallback'" block :loading="busy" @click="fileInput?.click()">Open camera</AppButton>
      <AppButton v-else-if="mode === 'preview'" block variant="secondary" @click="retake">Retake</AppButton>
    </div>
    <input ref="fileInput" type="file" accept="image/*" capture="user" class="hidden" @change="onFile" />
  </div>
</template>
