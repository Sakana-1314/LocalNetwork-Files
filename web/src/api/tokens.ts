import { http } from './http'
import { arr } from './normalize'
import type { AccessToken, CreatedAccessToken, CreatedS3Key, CreateS3KeyInput, CreateTokenInput, S3Key } from './types'

export const tokensApi = {
  async list(): Promise<AccessToken[]> {
    return arr(await http.get<AccessToken[]>('/tokens')).map((t) => ({ ...t, scopes: arr(t.scopes) }))
  },
  create(input: CreateTokenInput): Promise<CreatedAccessToken> {
    return http.post<CreatedAccessToken>('/tokens', input)
  },
  revoke(id: number): Promise<void> {
    return http.del<void>(`/tokens/${id}`)
  },
}

export const s3KeysApi = {
  async list(): Promise<S3Key[]> {
    return arr(await http.get<S3Key[]>('/s3keys'))
  },
  create(input: CreateS3KeyInput): Promise<CreatedS3Key> {
    return http.post<CreatedS3Key>('/s3keys', input)
  },
  remove(accessKey: string): Promise<void> {
    return http.del<void>(`/s3keys/${encodeURIComponent(accessKey)}`)
  },
}
