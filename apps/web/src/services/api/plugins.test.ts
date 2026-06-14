import { describe, expect, it } from 'vitest';
import {
  normalizePluginDeleteResult,
  normalizePluginList,
  normalizePluginStoreList,
  normalizePluginStoreInstallResult,
} from './plugins';

describe('plugin API normalizers', () => {
  it('normalizes plugin list responses and filters invalid entries', () => {
    const result = normalizePluginList({
      plugins_enabled: true,
      plugins_dir: 'custom-plugins',
      plugins: [
        {
          id: 'demo',
          path: '/plugins/demo',
          configured: true,
          registered: true,
          enabled: false,
          effective_enabled: true,
          supports_oauth: true,
          logo: '/plugins/demo/logo.png',
          config_fields: [
            {
              name: 'mode',
              type: 'enum',
              enum_values: ['fast', 'safe'],
              description: 'Mode',
            },
          ],
          menus: [{ path: '/plugins/demo/page', menu: 'Demo', description: 'Demo page' }],
          metadata: {
            name: 'Demo Plugin',
            version: '1.0.0',
            author: 'CPA',
            github_repository: 'owner/repo',
          },
        },
        { path: '/missing-id' },
      ],
    });

    expect(result.pluginsEnabled).toBe(true);
    expect(result.pluginsDir).toBe('custom-plugins');
    expect(result.plugins).toHaveLength(1);
    expect(result.plugins[0]).toMatchObject({
      id: 'demo',
      enabled: false,
      effectiveEnabled: true,
      supportsOAuth: true,
      metadata: {
        name: 'Demo Plugin',
        githubRepository: 'owner/repo',
      },
    });
    expect(result.plugins[0]?.configFields[0]?.enumValues).toEqual(['fast', 'safe']);
    expect(result.plugins[0]?.menus[0]?.path).toBe('/plugins/demo/page');
  });

  it('normalizes plugin delete results', () => {
    expect(
      normalizePluginDeleteResult({
        status: 'deleted',
        id: 'demo',
        path: '/plugins/demo.so',
        file_deleted: true,
        configured_removed: true,
        restart_required: false,
      })
    ).toEqual({
      status: 'deleted',
      id: 'demo',
      path: '/plugins/demo.so',
      fileDeleted: true,
      configuredRemoved: true,
      restartRequired: false,
    });

    expect(
      normalizePluginDeleteResult({
        fileDeleted: true,
        configuredRemoved: false,
        restartRequired: true,
      })
    ).toMatchObject({
      fileDeleted: true,
      configuredRemoved: false,
      restartRequired: true,
    });
  });

  it('normalizes plugin store responses and install results', () => {
    const store = normalizePluginStoreList({
      pluginsEnabled: true,
      pluginsDir: 'plugins',
      plugins: [
        {
          id: 'demo',
          name: 'Demo',
          installed: true,
          installedVersion: '1.0.0',
          effectiveEnabled: true,
          updateAvailable: true,
          tags: ['tool', null, ''],
        },
      ],
    });

    expect(store.pluginsEnabled).toBe(true);
    expect(store.plugins[0]).toMatchObject({
      id: 'demo',
      installed: true,
      installedVersion: '1.0.0',
      effectiveEnabled: true,
      updateAvailable: true,
      tags: ['tool'],
    });

    expect(
      normalizePluginStoreInstallResult({
        status: 'installed',
        id: 'demo',
        version: '1.1.0',
        path: '/plugins/demo',
        plugins_enabled: true,
        restart_required: true,
      })
    ).toEqual({
      status: 'installed',
      id: 'demo',
      version: '1.1.0',
      path: '/plugins/demo',
      pluginsEnabled: true,
      restartRequired: true,
    });
  });
});
