export const pluginCategories = ['会话与记忆', '开发工具', '协作与任务', '输入与界面', '集成扩展'] as const;
export type PluginCategory = typeof pluginCategories[number];
export type PluginSource = 'official' | 'community';
export interface CatalogPlugin {
 id: string;
 name: string;
 packageName: string;
 version: string;
 source: PluginSource;
 author: string;
 category: PluginCategory;
 summary: string;
 description: string;
 keywords: string[];
 repository: string;
 documentation: string;
 compatibility: string;
 bundled: boolean;
}

// Curated metadata only. Official entries were checked against the locked
// runtime package manifests and READMEs; community entries against npm metadata.
// A bundle declaration verifies the package form, not runtime compatibility.
export const catalogCheckedAt = '2026-10-27';
const officialRepository = 'https://github.com/deepseek-ai/deepseek-harness';
function official(id: string, name: string, category: PluginCategory, summary: string, description: string, keywords: string[], directory = id): CatalogPlugin {
 return {
  id, name, packageName: '@deepseek-ai/dsh-experimental-' + id, version: '0.2.1-alpha.2',
  source: 'official', author: 'DeepSeek', category, summary, description, keywords,
  repository: officialRepository,
  documentation: officialRepository + '/tree/master/packages/experimental/' + directory,
  compatibility: '未声明 DSH 版本范围，需由 DSH 检查依赖兼容性。', bundled: true,
 };
}

export const pluginCatalog: CatalogPlugin[] = [
 official('session-search', '会话搜索', '会话与记忆', '让智能体搜索和读取当前工作区的历史会话。', '为智能体添加历史会话搜索与读取工具，适合延续之前的工作、查找讨论和复用已有结论。它与侧边栏的会话搜索分别工作。', ['搜索', '历史', 'session', 'search']),
 {
  id: 'openviking-memory', name: 'OpenViking 记忆', packageName: '@openviking/dsh-memory-plugin', version: '0.5.29',
  source: 'community', author: 'OpenViking / volcengine', category: '会话与记忆',
  summary: '将 OpenViking 的记忆和上下文能力接入 DSH。',
  description: '由 OpenViking 项目提供的 DSH 组合包。使用前需按项目文档准备 OpenViking 服务及相关配置；具体连接能力和授权范围以该版本文档为准。',
  keywords: ['记忆', '上下文', 'memory', 'OpenViking'],
  repository: 'https://github.com/volcengine/OpenViking',
  documentation: 'https://github.com/volcengine/OpenViking/tree/main/examples/dsh-memory-plugin',
  compatibility: '未声明 DSH 版本范围；Node.js 要求 ^22.19.0 或 >=24。', bundled: false,
 },
 official('terminal-bundle', '持久终端', '开发工具', '让 Shell 进程在多次工具调用之间保持运行。', '提供持久终端工具，适合持续运行开发服务或需要保留进程状态的终端任务。终端由发起任务的智能体管理。', ['终端', 'Shell', 'terminal', 'bash']),
 {
  id: 'dsh-ssh', name: 'DSH SSH', packageName: '@linxin666/dsh-ssh', version: '0.4.11',
  source: 'community', author: 'zhu1090093659 / linxin666', category: '开发工具',
  summary: '在 DSH Web 中使用 SSH 终端、SFTP 和远程主机工具。',
  description: '为 DSH Web 提供远程 SSH 操作，包括主机配置、持久连接、终端、文件传输和端口转发。使用时需要自行配置主机与认证，并满足运行环境的网络策略。',
  keywords: ['SSH', 'SFTP', '远程', '终端'],
  repository: 'https://github.com/zhu1090093659/dsh-web', documentation: 'https://github.com/zhu1090093659/dsh-web#readme',
  compatibility: '包声明 DSH >=0.2.1-rc.3；Node.js ^22.19.0 或 >=24.0.0。', bundled: false,
 },
 official('agent-team-profile', '智能体团队', '协作与任务', '为团队协作提供成员、任务看板和成员会话。', '在一个实验性组合包中启用 Agent Teams 工具与 Web 界面。启用后可组织多个成员协同处理任务；具体委派行为与现有子代理配置的关系见官方说明。', ['Agent', '团队', '协作', 'team']),
 official('voice-input-bundle', '语音输入', '输入与界面', '通过麦克风录音，将语音转为可编辑的输入文字。', '组合语音服务、本地 SenseVoice 识别器和浏览器麦克风控件。首次使用可能需要准备运行依赖与模型文件，录音权限和下载准备由原生页面处理。', ['语音', '输入', 'voice', 'SenseVoice']),
 {
  id: 'dshmarket', name: 'DSH Market', packageName: 'dshmarket', version: '1.71.1',
  source: 'community', author: 'dsh-market', category: '集成扩展',
  summary: '社区维护的 DSH 插件发现与浏览工具。',
  description: '该第三方组合包在 DSH 中提供社区目录的浏览和搜索。它由独立社区维护，目录收录规则及所需的 DSH 版本请查看项目文档。',
  keywords: ['市场', '目录', 'marketplace', '发现'],
  repository: 'https://github.com/dsh-market/dsh-market', documentation: 'https://github.com/dsh-market/dsh-market#readme',
  compatibility: '未声明 DSH 版本范围，需由 DSH 检查依赖兼容性。', bundled: false,
 },
 official('session-titles-bundle', '会话标题', '会话与记忆', '随着新的用户消息更新会话标题，方便整理任务。', '为会话添加自动标题能力，帮助在多个任务之间识别和查找会话。标题生成行为以原生设置和所用模型能力为准。', ['标题', '会话', 'title', 'session']),
 official('tool-worktree', 'Git Worktree', '开发工具', '为智能体添加创建和进入 Git 工作树的工具。', '在独立 Git 工作树中开展任务，适合需要隔离工作目录的开发流程。使用前应确认当前仓库、分支状态以及原生工具的权限要求。', ['Git', 'worktree', '分支', '工作树']),
 official('ralph-bundle', 'Ralph 循环任务', '协作与任务', '通过新智能体执行用户明确要求的重复工作。', '为重复执行的工作提供实验性任务工具。适合有明确终止条件的迭代任务，具体运行规则和资源限制以官方说明及当前实例设置为准。', ['Ralph', '循环', '任务', '自动化']),
 official('inspector-profile', '会话检查器', '开发工具', '查看原始会话日志和对话节点，辅助诊断问题。', '为 Web 提供实验性会话检查能力，方便开发者观察原始日志与对话节点。它用于诊断，不改变平台权限和数据归属。', ['调试', '日志', 'inspector', 'debug']),
 official('badge-skill-bundle', 'DSH 徽章技能', '输入与界面', '提供可发现的 powered-by-DSH 徽章技能。', '将 DSH 徽章能力作为可发现技能提供。具体使用方式、资源与展示效果见官方插件说明。', ['徽章', '技能', 'badge', 'skill']),
];

export function filterPlugins(search: string, category: string, source: string) {
 const words = search.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
 return pluginCatalog.filter(plugin => (category === 'all' || plugin.category === category) && (source === 'all' || plugin.source === source) && words.every(word => [plugin.name, plugin.packageName, plugin.summary, plugin.description, plugin.author, plugin.category, ...plugin.keywords].join(' ').toLocaleLowerCase().includes(word)));
}
