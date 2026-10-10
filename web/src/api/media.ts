import { buildUrl, http, withToken, type RequestOptions } from './http'
import { normMediaInfo } from './normalize'
import type { MediaInfo, TranscodeRequest, TranscodeResponse } from './types'

export type ThumbSize = 128 | 256 | 512 | 1024

export const mediaApi = {
  thumbUrl(path: string, size: ThumbSize = 256): string {
    return withToken(buildUrl('/media/thumb', { path, size }))
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
