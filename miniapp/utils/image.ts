// Photos are re-encoded in the browser before upload: a ~1280px JPEG keeps
// uploads small on mobile data, and re-encoding drops EXIF metadata (including
// any embedded GPS) so only the explicit location step shares a position.

const MAX_SIDE = 1280
const QUALITY = 0.85

function toJpeg(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) =>
    canvas.toBlob(b => (b ? resolve(b) : reject(new Error('Could not encode photo'))), 'image/jpeg', QUALITY),
  )
}

function fit(w: number, h: number) {
  const scale = Math.min(1, MAX_SIDE / Math.max(w, h))
  return { w: Math.round(w * scale), h: Math.round(h * scale) }
}

/** Captures the current frame of a playing <video>. */
export function captureFrame(video: HTMLVideoElement): Promise<Blob> {
  const { w, h } = fit(video.videoWidth, video.videoHeight)
  const canvas = document.createElement('canvas')
  canvas.width = w
  canvas.height = h
  canvas.getContext('2d')!.drawImage(video, 0, 0, w, h)
  return toJpeg(canvas)
}

/** Downscales a picked/captured file (EXIF orientation is applied by the browser). */
export async function downscaleFile(file: File): Promise<Blob> {
  const bitmap = await createImageBitmap(file)
  const { w, h } = fit(bitmap.width, bitmap.height)
  const canvas = document.createElement('canvas')
  canvas.width = w
  canvas.height = h
  canvas.getContext('2d')!.drawImage(bitmap, 0, 0, w, h)
  bitmap.close()
  return toJpeg(canvas)
}
