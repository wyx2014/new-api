export const MODEL_GATEWAY_PREFIX = '/modelgw'

export function appPath(path: string): string {
  return `${MODEL_GATEWAY_PREFIX}${path}`
}
