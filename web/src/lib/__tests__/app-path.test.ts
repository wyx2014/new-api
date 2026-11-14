import {
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
} from '@tanstack/react-router'
import { expect, it } from 'vitest'

import { appPath } from '../app-path'
import { appUrl } from '../dom-utils'
import { buildDiscordOAuthUrl, buildOIDCOAuthUrl } from '../oauth'

it.each([
  ['/api/status', '/modelgw/api/status'],
  ['/sign-in?redirect=%2Fkeys', '/modelgw/sign-in?redirect=%2Fkeys'],
])('mounts application URL %s', (input, expected) => {
  expect(appPath(input)).toBe(expected)
})

it('uses the public application path for OAuth callbacks and copied base URLs', () => {
  expect(appUrl()).toBe(`${window.location.origin}/modelgw`)
  expect(
    new URL(buildDiscordOAuthUrl('client', 'state')).searchParams.get(
      'redirect_uri'
    )
  ).toBe(appUrl('/oauth/discord'))
  expect(
    new URL(
      buildOIDCOAuthUrl('https://provider.example/authorize', 'client', 'state')
    ).searchParams.get('redirect_uri')
  ).toBe(appUrl('/oauth/oidc'))
})

it('lets the existing router mount navigation without manual URL prefixes', async () => {
  const root = createRootRoute()
  const dashboard = createRoute({
    getParentRoute: () => root,
    path: '/dashboard',
  })
  const router = createRouter({
    routeTree: root.addChildren([dashboard]),
    basepath: '/modelgw',
    history: createMemoryHistory({ initialEntries: ['/modelgw/dashboard'] }),
  })
  await router.load()
  await router.navigate({ href: '/dashboard' })
  expect(router.state.location.pathname).toBe('/dashboard')
  expect(router.history.location.href).toBe('/modelgw/dashboard')
})
