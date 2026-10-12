import { buildUrl, http, withToken, type RequestOptions } from './http'
import { normMediaInfo } from './normalize'
import type { Entry, MediaInfo, TranscodeRequest, TranscodeResponse } from './types'

export type ThumbSize = 128 | 256 | 512 | 1024

export const mediaApi = {
  /**
   * 预览图地址：只带内容摘要与签名（签名不过期），因此无需 access_token，
   * 也不会把文件路径暴露在 URL 里。不可预览时返回空串。
   */
  thumbUrl(entry: Pick<Entry, 'thumb' | 'thumb_sig'>, size: ThumbSize = 256): string {
    if (!entry.thumb) return ''
    return buildUrl('/media/thumb', { d: entry.thumb, s: entry.thumb_sig, size })
  },
  async info(path: string, opts?: RequestOptions): Promise<MediaInfo> {
    return normMediaInfo(await http.get<MediaInfo>('/media/info', { ...opts, query: { path } }))
  },
  transcode(req: TranscodeRequest, opts?: RequestOptions): Promise<TranscodeResponse> {
    return http.post<TranscodeResponse>('/media/transcode', req, opts)
  },
  stopHls(id: string): Promise<void> {
    return http.del<void>(`/media/hls/${encodeURIComponent(id)}`, { silent: true })
  },
  /** 音频实时转码 MP3 流 */
  audioStreamUrl(path: string, start?: number): string {
    return withToken(buildUrl('/media/audio', { path, start: start ? Math.floor(start) : undefined }))
  },
  /** 内嵌字幕：index 为 MediaInfo.subtitles 下标 */
  embeddedSubtitleUrl(videoPath: string, index: number): string {
    return withToken(buildUrl('/media/subtitle', { path: videoPath, index }))
  },
  /** 外挂字幕：path 为字幕文件虚拟路径 */
  externalSubtitleUrl(subtitlePath: string): string {
    return withToken(buildUrl('/media/subtitle', { path: subtitlePath }))
  },
}
