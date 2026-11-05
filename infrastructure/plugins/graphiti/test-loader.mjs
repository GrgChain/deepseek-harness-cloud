// Test only: use this checkout's pinned DSH peers without installing dependencies.
const runtime = new URL('../../runtime/package.json', import.meta.url).href;
export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith('@deepseek-ai/'))
    return nextResolve(specifier, { ...context, parentURL: runtime });
  return nextResolve(specifier, context);
}
