/**
 * 与 server/internal/api/openapi.yaml 中 components.schemas 一一对应的类型定义。
 * 注意：后端为 Go，nil 切片会序列化为 null，api 模块中会统一归一化为空数组。
 */

export type Kind = 'dir' | 'image' | 'video' | 'audio' | 'text' | 'pdf' | 'archive' | 'other'

export interface Entry {
  name: string
  /** 虚拟路径 */
  path: string
  is_dir: boolean
  size: number
  mod_time: string
  mime?: string
  kind: Kind
  writable: boolean
  /** 是否可生成预览图 */
  thumb: boolean
}

export interface ListResponse {
  path: string
  writable: boolean
  entries: Entry[]
}

export interface TextResponse {
  content: string
  truncated: boolean
}

export interface TransferRequest {
  sources: string[]
  dest_dir: string
  overwrite?: boolean
}

export interface BatchFailure {
  path: string
  error: string
}

export interface BatchResult {
  succeeded: string[]
  failed: BatchFailure[]
}

export interface SearchResult extends Entry {
  score: number
  /** 名称中命中的字符下标（按 Unicode 字符计） */
  matches: number[]
}

export interface SearchResponse {
  results: SearchResult[]
  total: number
  indexing: boolean
  took_ms: number
}

export interface SearchParams {
  q: string
  path?: string
  kind?: Kind
  limit?: number
}

export type Role = 'admin' | 'user'

export interface Mount {
  id: number
  name: string
  /** 服务器本地绝对路径（仅管理员可见） */
  path?: string
  writable: boolean
}

export interface User {
  id: number
  username: string
  role: Role
  disabled: boolean
  mounts: Mount[]
  created_at: string
}

export interface MountInput {
  name: string
  path: string
  writable: boolean
}

export interface UserInput {
  username: string
  password?: string
  role: Role
  disabled: boolean
  mounts: MountInput[]
}

export interface LoginResponse {
  token: string
  expires_at: string
  user: User
}

export interface PasswordChangeResponse {
  token: string
  expires_at: string
}

export type TokenScope = 'read' | 'write'

export interface AccessToken {
  id: number
  name: string
  prefix: string
  scopes: string[]
  expires_at: string | null
  last_used_at: string | null
  created_at: string
}

export interface CreatedAccessToken extends AccessToken {
  token: string
}

export interface CreateTokenInput {
  name: string
  scopes?: TokenScope[]
  /** 0 或省略表示永不过期 */
  expires_in_days?: number
}

export interface S3Key {
  access_key: string
  name: string
  read_only: boolean
  created_at: string
}

export interface CreatedS3Key extends S3Key {
  secret_key: string
}

export interface CreateS3KeyInput {
  name?: string
  read_only?: boolean
}

export interface VideoStream {
  codec: string
  width: number
  height: number
  fps: number
}

export interface AudioStream {
  index: number
  codec: string
  channels: number
  language?: string
  title?: string
}

export interface SubtitleStream {
  index: number
  codec: string
  language?: string
  title?: string
  /** 外挂字幕的虚拟路径（内嵌字幕为空） */
  external?: string
}

export interface MediaInfo {
  duration: number
  format: string
  bit_rate: number
  /** 浏览器大概率可直接播放（无需转码） */
  direct_play: boolean
  video: VideoStream | null
  audio: AudioStream[]
  subtitles: SubtitleStream[]
}

export type Quality = 'original' | '1080p' | '720p' | '480p'

export interface TranscodeRequest {
  path: string
  quality?: Quality
  audio_track?: number
}

export interface TranscodeResponse {
  id: string
  /** 如 /api/v1/media/hls/{id}/index.m3u8 */
  playlist: string
  duration: number
}

export interface ServiceStatus {
  enabled: boolean
  running: boolean
  port: number
  /** WebDAV 路径前缀，如 /dav */
  path?: string
  error?: string
  /** 仅 SMB：当前用户的挂载名 → SMB 共享名 */
  shares?: Record<string, string>
  /** 仅 SMB：当前用户的 SMB 登录名（可能带前缀、小写） */
  account?: string
}

export interface SystemInfo {
  version: string
  hostname: string
  addresses: string[]
  http_port: number
  ffmpeg: boolean
  services: {
    webdav: ServiceStatus
    s3: ServiceStatus
    smb: ServiceStatus
  }
}

export type Encoder = 'libx264' | 'h264_videotoolbox' | 'h264_nvenc' | 'h264_qsv' | 'h264_vaapi'
export type Preset = 'ultrafast' | 'superfast' | 'veryfast' | 'faster' | 'fast' | 'medium'

export interface Settings {
  cache: {
    thumb_ttl_hours: number
    thumb_max_mb: number
    transcode_ttl_hours: number
    transcode_max_mb: number
    cleanup_interval_minutes: number
  }
  transcode: {
    encoder: Encoder
    preset: Preset
    max_sessions: number
    segment_seconds: number
  }
  search: {
    reindex_interval_minutes: number
    watch: boolean
  }
  services: {
    webdav_enabled: boolean
    s3_enabled: boolean
    s3_port: number
    smb_enabled: boolean
    smb_port: number
    smb_workgroup: string
  }
}

export interface CacheBucketStats {
  files: number
  bytes: number
  max_bytes: number
  ttl_hours: number
}

export interface CacheSession {
  id: string
  path: string
  quality: string
  user: string
  running: boolean
  last_access: string
}

export interface CacheStats {
  dir: string
  thumb: CacheBucketStats
  transcode: CacheBucketStats
  sessions: CacheSession[]
  last_cleanup: string | null
}

export type CacheKind = 'thumb' | 'transcode' | 'all'

export interface CleanRequest {
  kind?: CacheKind
  /** false 时清空全部 */
  expired_only?: boolean
}

export interface CleanResult {
  removed_files: number
  removed_bytes: number
}

export interface IndexStatus {
  indexing: boolean
  entries: number
  roots: number
  last_built: string | null
  duration_ms: number
}

export interface BrowseResponse {
  path: string
  parent: string
  /** 子目录绝对路径 */
  dirs: string[]
}

export interface ErrorBody {
  error: string
}
