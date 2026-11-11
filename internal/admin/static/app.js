const content = document.getElementById('content');
const statusEl = document.getElementById('status');
const statusText = document.getElementById('statusText');
const nav = document.getElementById('nav');

async function api(path, options = {}) {
  const res = await fetch('/admin/api/v1' + path, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  const text = await res.text();
  let data;
  try { data = text ? JSON.parse(text) : {}; } catch { data = { error: text }; }
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

function el(tag, props = {}, children = []) {
  const node = document.createElement(tag);
  Object.assign(node, props);
  for (const child of children) {
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

function card(k, v, hint) {
  const children = [el('div', { className: 'k' }, [k]), el('div', { className: 'v' }, [v])];
  if (hint) children.push(el('div', { className: 'hint' }, [hint]));
  return el('div', { className: 'card' }, children);
}

function table(headers, rows, onClick) {
  const t = el('table');
  const thead = el('thead');
  const hr = el('tr');
  for (const h of headers) hr.append(el('th', {}, [h]));
  thead.append(hr);
  const tbody = el('tbody');
  for (const row of rows) {
    const tr = el('tr', onClick ? { className: 'click' } : {});
    for (const cell of row) {
      tr.append(el('td', {}, [cell instanceof Node ? cell : String(cell ?? '')]));
    }
    if (onClick) tr.addEventListener('click', () => onClick(row));
    tbody.append(tr);
  }
  t.append(thead, tbody);
  return t;
}

function badge(text, kind = '') {
  return el('span', { className: `badge ${kind}` }, [text]);
}

function empty(big, text) {
  return el('div', { className: 'empty' }, [
    el('div', { className: 'big' }, [big]),
    el('div', {}, [text]),
  ]);
}

function toast(message, isError = false) {
  const node = el('div', { className: `toast ${isError ? 'err' : ''}` }, [message]);
  document.body.append(node);
  setTimeout(() => node.remove(), 3600);
}

function setStatus(text, online) {
  statusText.textContent = `飞书：${text}`;
  statusEl.className = `status ${online ? 'ok' : 'bad'}`;
}

async function render() {
  const hash = location.hash.replace(/^#\/?/, '') || 'overview';
  const [page, ...rest] = hash.split('/');
  for (const a of nav.querySelectorAll('a')) {
    a.classList.toggle('active', a.dataset.page === page);
  }
  content.replaceChildren(el('p', { className: 'muted' }, ['加载中…']));
  let overview = null;
  try {
    overview = await api('/overview');
    setStatus(overview.feishu, overview.feishu === 'connected');
  } catch { /* 状态失败不阻塞页面 */ }
  try {
    if (page === 'overview') return await renderOverview(overview);
    if (page === 'sessions') return rest[0] ? await renderSession(rest[0]) : await renderSessions();
    if (page === 'runs') return await renderRun(rest[0]);
    if (page === 'skills') return await renderSkills();
    if (page === 'mcps') return await renderMCPs();
    if (page === 'settings') return await renderSettings();
    content.replaceChildren(empty('🧭', '页面不存在'));
  } catch (err) {
    content.replaceChildren(empty('⚠️', err.message));
  }
}

async function renderOverview(data) {
  if (!data) data = await api('/overview');

  const testInput = el('textarea', { placeholder: '输入一句话，直接用当前模型试跑（会带上技能）', style: 'min-height:90px' });
  const result = el('div', { className: 'result', style: 'display:none' });
  const runBtn = el('button', {}, ['试跑']);
  runBtn.addEventListener('click', async () => {
    const text = testInput.value.trim();
    if (!text) return toast('先输入内容', true);
    runBtn.disabled = true;
    runBtn.textContent = '调用中…';
    result.style.display = 'none';
    try {
      const res = await api('/test', { method: 'POST', body: JSON.stringify({ text }) });
      result.style.display = 'block';
      result.textContent = `${res.output}\n\n（${res.latency_ms} ms）`;
    } catch (err) {
      toast(err.message, true);
    } finally {
      runBtn.disabled = false;
      runBtn.textContent = '试跑';
    }
  });

  content.replaceChildren(
    el('h2', {}, ['概览']),
    el('p', { className: 'lede' }, [`版本 ${data.version} · 通道 ${data.channel}/${data.transport}`]),
    el('div', { className: 'cards' }, [
      card('会话', data.counts.conversations),
      card('运行', data.counts.runs),
      card('技能', data.counts.skills),
      card('MCP', data.counts.mcps),
      card('工具', data.counts.tools),
    ]),
    el('div', { className: 'panel' }, [
      el('div', { className: 'head' }, [el('b', {}, ['模型'])]),
      el('div', { className: 'muted' }, data.model.echo ? '未配置 API Key，当前为回显模式' : `${data.model.model} · ${data.model.base_url}`),
    ]),
    el('div', { className: 'panel' }, [
      el('div', { className: 'head' }, [el('b', {}, ['在线试跑']), el('span', { className: 'muted' }, ['验证模型与技能是否生效'])]),
      el('div', { className: 'form' }, [testInput, el('div', { className: 'actions' }, [runBtn])]),
      result,
    ]),
  );
}

async function renderSessions() {
  const data = await api('/sessions');
  if (!data.items.length) {
    content.replaceChildren(el('h2', {}, ['会话']), empty('💬', '还没有会话。在飞书里 @机器人 发一条消息就会出现在这里。'));
    return;
  }
  const rows = data.items.map((s) => [
    s.id, s.chat_id, s.sender, s.messages, s.queued,
    s.active_run_id ? badge('运行中', 'warn') : badge('空闲'),
  ]);
  content.replaceChildren(
    el('h2', {}, ['会话']),
    el('p', { className: 'lede' }, ['当前为内存存储，重启后清空；点击行查看消息与运行。']),
    table(['会话', 'chat', '发送者', '消息', '排队', '状态'], rows, (row) => {
      location.hash = `#/sessions/${row[0]}`;
    }),
  );
}

async function renderSession(id) {
  const data = await api(`/sessions/${encodeURIComponent(id)}`);
  const msgRows = data.messages.map((m) => [m.ID || m.id, m.Status || m.status, (m.Text || m.text || '').slice(0, 60)]);
  const runRows = data.runs.map((r) => [r.ID || r.id, r.State || r.state, (r.Output || r.output || '').slice(0, 60)]);
  content.replaceChildren(
    el('h2', {}, ['会话详情']),
    el('p', { className: 'lede' }, [id]),
    el('h3', {}, ['消息']),
    msgRows.length ? table(['ID', '状态', '内容'], msgRows) : empty('📭', '没有消息'),
    el('h3', {}, ['运行']),
    runRows.length ? table(['ID', '状态', '输出'], runRows) : empty('⚙️', '没有运行记录'),
    el('div', { className: 'actions' }, [
      el('a', { href: '#/sessions', className: 'muted' }, ['← 返回会话列表']),
    ]),
  );
}

async function renderRun(id) {
  const data = await api(`/runs/${encodeURIComponent(id)}`);
  const run = data.run;
  const events = data.events.map((e) => [
    new Date(e.created_at).toLocaleTimeString(),
    badge(e.kind, e.kind === 'tool' ? 'warn' : ''),
    e.name,
    e.detail || '',
    e.duration_ms ? `${e.duration_ms} ms` : '',
  ]);
  const deliveries = data.deliveries.map((d) => [d.State || d.state, d.Attempts ?? d.attempts, d.Error || d.error || '']);
  content.replaceChildren(
    el('h2', {}, ['运行详情']),
    el('p', { className: 'lede' }, [
      `${id} · ${run.State || run.state} · 输入 ${run.InputIDs?.length ?? run.input_ids?.length ?? 0} 条`,
      ` · token ${run.InputTokens ?? run.input_tokens ?? 0}/${run.OutputTokens ?? run.output_tokens ?? 0}`,
    ]),
    el('h3', {}, ['阶段与工具']),
    events.length ? table(['时间', '类型', '名称', '结果', '耗时'], events) : empty('⏱️', '没有事件记录'),
    el('h3', {}, ['投递']),
    deliveries.length ? table(['状态', '尝试', '错误'], deliveries) : empty('📮', '没有投递记录'),
    el('div', { className: 'actions' }, [el('a', { href: '#/sessions', className: 'muted' }, ['← 返回会话列表'])]),
  );
}

async function renderSkills() {
  const data = await api('/skills');
  const nameInput = el('input', { placeholder: '技能名，例如 incident-report' });
  const textarea = el('textarea', { placeholder: '# 标题\n> 一句话描述\n\n步骤…' });
  const list = el('div');

  async function select(name) {
    const item = await api(`/skills/${encodeURIComponent(name)}`);
    nameInput.value = item.name;
    textarea.value = item.content;
  }

  const save = el('button', {}, ['保存']);
  save.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name) return toast('先填技能名', true);
    try {
      await api(`/skills/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({ content: textarea.value }),
      });
      toast('已保存，下次运行自动生效');
      await renderSkills();
    } catch (err) { toast(err.message, true); }
  });

  const del = el('button', { className: 'danger' }, ['删除']);
  del.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name || !confirm(`删除技能 ${name}？`)) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}`, { method: 'DELETE' });
      toast('已删除');
      await renderSkills();
    } catch (err) { toast(err.message, true); }
  });

  if (data.items.length) {
    const rows = data.items.map((s) => [
      el('a', { href: '#' }, [s.name]),
      (s.content || '').split('\n').find((l) => l.trim().startsWith('>'))?.replace(/^>\s*/, '') || '—',
    ]);
    list.append(table(['名称', '描述'], rows, async (row) => { await select(row[0].textContent); }));
  } else {
    list.append(empty('🧩', '还没有技能。下面填名称和内容保存即可。'));
  }

  content.replaceChildren(
    el('h2', {}, ['技能']),
    el('p', { className: 'lede' }, ['Markdown 文件保存在 data/skills/，每次运行自动注入系统提示词，无需重启。']),
    list,
    el('h3', {}, ['编辑']),
    el('div', { className: 'form' }, [
      el('label', {}, ['名称', nameInput]),
      el('label', {}, ['内容', textarea]),
      el('div', { className: 'actions' }, [save, del]),
    ]),
  );
}

async function renderMCPs() {
  const data = await api('/mcps');
  const nameInput = el('input', { placeholder: '名称' });
  const transport = el('select');
  transport.append(el('option', { value: 'stdio' }, ['stdio']), el('option', { value: 'http' }, ['http']));
  const command = el('input', { placeholder: 'stdio 命令，例如 npx' });
  const args = el('input', { placeholder: '参数，空格分隔' });
  const url = el('input', { placeholder: 'http 地址' });
  const enabled = el('input', { type: 'checkbox', checked: true });

  const list = data.items.length
    ? table(['名称', '传输', '目标', '状态'], data.items.map((m) => [
      m.name, m.transport, m.command || m.url || '', m.enabled ? badge('启用', 'ok') : badge('停用'),
    ]), async (row) => {
      const item = data.items.find((m) => m.name === row[0]);
      nameInput.value = item.name;
      transport.value = item.transport;
      command.value = item.command || '';
      args.value = (item.args || []).join(' ');
      url.value = item.url || '';
      enabled.checked = item.enabled;
    })
    : empty('🔌', '还没有 MCP 配置。');

  const save = el('button', {}, ['保存']);
  save.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name) return toast('先填名称', true);
    try {
      await api(`/mcps/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({
          transport: transport.value,
          command: command.value.trim(),
          args: args.value.trim() ? args.value.trim().split(/\s+/) : [],
          url: url.value.trim(),
          enabled: enabled.checked,
        }),
      });
      toast('已保存');
      await renderMCPs();
    } catch (err) { toast(err.message, true); }
  });

  const del = el('button', { className: 'danger' }, ['删除']);
  del.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name || !confirm(`删除 MCP ${name}？`)) return;
    try {
      await api(`/mcps/${encodeURIComponent(name)}`, { method: 'DELETE' });
      toast('已删除');
      await renderMCPs();
    } catch (err) { toast(err.message, true); }
  });

  content.replaceChildren(
    el('h2', {}, ['MCP']),
    el('p', { className: 'lede' }, ['配置保存在 data/mcp.yaml。当前仅保存配置，运行时接入在下一阶段。']),
    list,
    el('h3', {}, ['编辑']),
    el('div', { className: 'form' }, [
      el('label', {}, ['名称', nameInput]),
      el('label', {}, ['传输方式', transport]),
      el('label', {}, ['命令', command]),
      el('label', {}, ['参数', args]),
      el('label', {}, ['地址', url]),
      el('label', {}, ['启用', enabled]),
      el('div', { className: 'actions' }, [save, del]),
    ]),
  );
}

async function renderSettings() {
  const data = await api('/settings');
  content.replaceChildren(
    el('h2', {}, ['设置']),
    el('p', { className: 'lede' }, ['当前生效配置；修改 .env 后重启生效。']),
    el('h3', {}, ['模型']),
    el('pre', {}, [JSON.stringify(data.model, null, 2)]),
    el('h3', {}, ['飞书']),
    el('pre', {}, [JSON.stringify(data.feishu, null, 2)]),
    el('h3', {}, ['存储']),
    el('pre', {}, [JSON.stringify(data.storage, null, 2)]),
  );
}

window.addEventListener('hashchange', render);
render();
