import { describe, expect, it } from 'vitest';
import type { Permission } from '../../api/management';
import { includeDependencies, permissionGroup } from './role-permission-picker';

describe('role-permission-picker helpers', () => {
  it('maps permission IDs to their corresponding functional group', () => {
    expect(permissionGroup('instances:read').name).toBe('工作空间');
    expect(permissionGroup('instances:release-storage').name).toBe('工作数据');
    expect(permissionGroup('personal-model:write').name).toBe('模型提供商');
    expect(permissionGroup('custom:unknown').name).toBe('其他权限');
  });

  it('transitively includes assignable dependencies without granting non-assignable permissions', () => {
    const catalog: Permission[] = [
      { id: 'skills:read', name: '读取技能', assignable: true, dependencies: [] },
      {
        id: 'skills:manage',
        name: '管理技能',
        assignable: true,
        dependencies: ['skills:read', 'internal:locked'],
      },
      { id: 'internal:locked', name: '内部权限', assignable: false, dependencies: [] },
    ];
    expect(includeDependencies(['skills:manage'], catalog)).toEqual([
      'skills:manage',
      'skills:read',
    ]);
  });
});
