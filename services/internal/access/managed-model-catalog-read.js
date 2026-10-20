async readManagedCatalog(generation) {
 const delays = [1000, 2000, 4000, 8000, 15000, 15000];
 for (let attempt = 0; ; attempt++) {
  if (generation !== this.generation) throw new Error('Model catalog generation changed');
  const response = await this.ctx.remote.session.modelCatalog();
  if (generation !== this.generation) throw new Error('Model catalog generation changed');
  // Only carrier failures may recover by retrying this read. Never retry
  // authentication, permission, argument or other business failures.
  const transient = !response.ok && response.error.code === 'gateway/internal'
   && /transport failure .*HTTP (429|502|503|504)\b|Failed to fetch|fetch failed|NetworkError|network connection|Load failed/i.test(response.error.message);
  if (!response.ok && !transient) return response;
  if (response.ok && (response.value.default?.provider !== 'platform-models' || response.value.groups.some(group => group.models.length > 0))) return response;
  if (attempt >= (transient ? 2 : delays.length)) throw new Error('所选模型暂不可用，请在控制台查看模型校验状态，或点击重试。');
  this.store.update(state => { state.error = '模型暂未就绪，正在自动检查…'; });
  await new Promise(resolve => { this.retryResolve = resolve; this.retryTimer = setTimeout(resolve, delays[attempt]); });
  if (generation !== this.generation) throw new Error('Model catalog generation changed');
  this.retryResolve = this.retryTimer = undefined;
 }
}
