import { http } from './http'
import { arr, normServiceStatus } from './normalize'
import type { SystemInfo } from './types'

export const systemApi = {
  async info(): Promise<SystemInfo> {
    const r = await http.get<SystemInfo>('/system/info')
    return {
      ...r,
      addresses: arr(r.addresses),
      services: {
        webdav: normServiceStatus(r.services?.webdav),
        s3: normServiceStatus(r.services?.s3),
        smb: normServiceStatus(r.services?.smb),
      },
    }
  },
}
