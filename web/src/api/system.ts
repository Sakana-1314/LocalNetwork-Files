import { http } from './http'
import { arr } from './normalize'
import type { ServiceStatus, SystemInfo } from './types'

const emptyService: ServiceStatus = { enabled: false, running: false, port: 0 }

export const systemApi = {
  async info(): Promise<SystemInfo> {
    const r = await http.get<SystemInfo>('/system/info')
    return {
      ...r,
      addresses: arr(r.addresses),
      services: {
        webdav: r.services?.webdav ?? emptyService,
        s3: r.services?.s3 ?? emptyService,
        smb: r.services?.smb ?? emptyService,
      },
    }
  },
}
