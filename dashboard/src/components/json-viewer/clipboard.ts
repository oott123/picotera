/**
 * Clipboard writes for the JSON viewer.
 *
 * Every call site reports its own outcome, so a failure surfaces as "复制失败"
 * rather than a silent no-op. That matters here more than elsewhere: copying a
 * value is the viewer's main job, and `navigator.clipboard` is unavailable on
 * insecure origins, which is a realistic way to run this dashboard.
 */
export type CopyResult = 'ok' | 'unavailable' | 'error'

export async function writeClipboard(text: string): Promise<CopyResult> {
  if (!navigator.clipboard?.writeText) return 'unavailable'
  try {
    await navigator.clipboard.writeText(text)
    return 'ok'
  } catch {
    return 'error'
  }
}

export function copyFailureText(result: CopyResult): string {
  return result === 'unavailable' ? '浏览器不允许访问剪贴板' : '复制失败'
}
