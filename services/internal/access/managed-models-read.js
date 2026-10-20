// The remote web deployment is managed by the platform. Read the existing
// session catalog without requesting a local settings mirror or credentials.
if (!this.ctx.remote.$host.isLoopback) {
 let catalog;
 try { catalog = await this.readManagedCatalog(generation); }
 catch { if (generation === this.generation) this.failLoad('模型目录暂不可用，请点击刷新目录重试。'); return; }
 if (generation !== this.generation) return;
 if (!catalog.ok) { this.failLoad('模型目录暂不可用，请稍后重试。'); return; }
 this.store.update(state => {
  state.status = 'ready';
  state.error = null;
  state.writable = false;
  state.rows = [];
  state.namespaces = new Map();
  state.managedCatalog = catalog.value;
 });
 return;
}
