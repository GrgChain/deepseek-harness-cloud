const injected = () => {
 const root = '/data/workspace';
 const inside = path => path === root || path.startsWith(root + '/');
 const checked = (path = root) => {
  if (typeof path !== 'string' || !path.startsWith('/') || path.includes('\0') || path.includes('\\')) throw new Error('请在 workspace 目录内新建或选择工作区。');
  const parts = [];
  for (const part of path.split('/')) {
   if (part === '..') parts.pop();
   else if (part && part !== '.') parts.push(part);
  }
  const normalized = '/' + parts.join('/');
  if (!inside(normalized)) throw new Error('请在 workspace 目录内新建或选择工作区。');
  return normalized;
 };
 return {
  listDirectory: async (path, signal) => {
   const listing = await ctx.uiWorkspace.listDirectory(checked(path), signal);
   checked(listing.path);
   return {...listing, home:root,
    crumbs:listing.crumbs.filter(crumb => inside(crumb.path)),
    entries:listing.entries.filter(entry => inside(entry.path))};
  },
  createDirectory: async (path, name) => {
   const parent = checked(path);
   if (typeof name !== 'string' || !name.trim() || name === '.' || name === '..' || /[/\\\0]/.test(name)) throw new Error('请输入有效的文件夹名称。');
   return ctx.uiWorkspace.createDirectory(parent, name);
  },
  t: (key, ...args) => key === 'browser.home' ? 'workspace' : ctx.locale.bind(LOCALE_NS)(key, ...args)
 };
};
