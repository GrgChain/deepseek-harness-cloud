import { useState } from 'react';
import { Button, Checkbox, Empty, Input } from 'antd';
import type { Permission } from '../../api/management';

export const permissionGroups: { name: string; prefixes: string[]; ids?: string[] }[] = [
  { name: '工作空间', prefixes: ['instances'] },
  { name: '工作数据', prefixes: [], ids: ['instances:release-storage'] },
  { name: '技能包', prefixes: ['skills'] },
  { name: '连接器', prefixes: ['connectors'] },
  { name: '记忆', prefixes: ['memory'] },
  { name: '知识库', prefixes: ['knowledge'] },
  { name: '模型提供商', prefixes: ['personal-model', 'model', 'models'] },
  { name: '用户管理', prefixes: ['users'] },
  { name: '角色与权限', prefixes: ['roles'] },
  { name: '平台配置', prefixes: ['configuration', 'defaults'] },
  { name: '操作日志', prefixes: ['audit'] },
  { name: '其他权限', prefixes: [] },
];

export const permissionGroup = (id: string) => permissionGroups.find(group => group.ids?.includes(id)) ?? permissionGroups.find(group => group.prefixes.includes(id.split(':')[0])) ?? permissionGroups[permissionGroups.length - 1];

// Called only by the explicit dependency action; ordinary selection never grants extras.
export function includeDependencies(selected: string[], catalog: Permission[]) {
  const result = new Set(selected);
  const byID = new Map(catalog.map(permission => [permission.id, permission]));
  for (const id of result) {
    for (const dependency of byID.get(id)?.dependencies ?? []) {
      if (byID.get(dependency)?.assignable) result.add(dependency);
    }
  }
  return [...result];
}

export function RolePermissionPicker({ catalog, value = [], onChange, disabled = false }: {
  catalog: Permission[]; value?: string[]; onChange?: (value: string[]) => void; disabled?: boolean;
}) {
  const [search, setSearch] = useState('');
  const query = search.trim().toLocaleLowerCase();
  const labels = new Map(catalog.map(permission => [permission.id, permission.name]));
  const groups = permissionGroups.map(group => ({ ...group, items: catalog.filter(permission => permissionGroup(permission.id) === group && (!query || [permission.id, permission.name, permission.description, group.name].join(' ').toLocaleLowerCase().includes(query))) })).filter(group => group.items.length);
  const change = (ids: string[], checked: boolean) => onChange?.(checked ? [...new Set([...value, ...ids])] : value.filter(id => !ids.includes(id)));
  return <div className="role-permission-picker">
    <Input.Search type="search" aria-label="搜索功能权限" placeholder="搜索权限名称、编码或用途" allowClear value={search} onChange={event => setSearch(event.target.value)} />
    <p className="role-permission-help">按需选择权限；依赖权限需单独勾选。搜索和分组操作会保留其他已选权限。</p>
    <div className="role-editor-permission-groups">
      {groups.map(group => {
        const assignable = group.items.filter(permission => permission.assignable).map(permission => permission.id);
        const allSelected = assignable.length > 0 && assignable.every(id => value.includes(id));
        return <section className="role-editor-permission-group" key={group.name} aria-label={group.name}>
          <div className="role-editor-permission-heading"><strong>{group.name}</strong><span>{group.items.filter(permission => value.includes(permission.id)).length} / {group.items.length} 项</span><Button size="small" type="text" disabled={disabled || !assignable.length} aria-label={`${allSelected ? '清空' : '全选'}${group.name}当前显示权限`} onClick={() => change(assignable, !allSelected)}>{allSelected ? '清空' : '全选'}</Button></div>
          <div className="role-editor-permission-options">{group.items.map(permission => <Checkbox key={permission.id} checked={value.includes(permission.id)} disabled={disabled || !permission.assignable} onChange={event => change([permission.id], event.target.checked)}><span className="role-permission-copy"><span>{permission.name}</span><small>{permission.id}</small>{permission.description && <small>{permission.description}</small>}{permission.dependencies.length > 0 && <small>依赖：{permission.dependencies.map(id => labels.get(id) ?? id).join('、')}</small>}</span></Checkbox>)}</div>
        </section>;
      })}
    </div>
    {!groups.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有匹配的权限，已选权限保持不变" />}
  </div>;
}
