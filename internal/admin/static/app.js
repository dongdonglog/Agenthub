const content = document.getElementById('content');
const statusEl = document.getElementById('status');
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

function card(k, v) {
  return el('div', { className: 'card' }, [
    el('div', { className: 'k' }, [k]),
    el('div', { className: 'v' }, [v]),
  ]);
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

function toast(message, isError = false) {
  const node = el('div', { className: `toast ${isError ? 'err' : ''}` }, [message]);
  content.append(node);
  setTimeout(() => node.remove(), 4000);
}

async function render() {
  const hash = location.hash.replace(/^#\/?/, '') || 'overview';
  const [page, ...rest] = hash.split('/');
  for (const a of nav.querySelectorAll('a')) {
    a.classList.toggle('active', a.dataset.page === page);
  }
  try {
    if (page === 'overview') return await renderOverview();
    if (page === 'sessions') return rest[0] ? await renderSession(rest[0]) : await renderSessions();
    if (page === 'skills') return await renderSkills();
    if (page === 'mcps') return await renderMCPs();
    if (page === 'settings') return await renderSettings();
    content.replaceChildren(el('p', { className: 'muted' }, ['页面不存在']));
  } catch (err) {
    content.replaceChildren(el('p', { className: 'toast err' }, [err.message]));
  }
}

async function renderOverview() {
  const data = await api('/overview');
  const online = data.feishu === 'connected';
  statusEl.textContent = `飞书：${data.feishu}`;
  statusEl.className = `status ${online ? 'ok' : 'bad'}`;

  const cards = el('div', { className: 'cards' }, [
    card('会话', data.counts.conversations),
    card('运行', data.counts.runs),
    card('技能', data.counts.skills),
    card('MCP', data.counts.mcps),
  ]);
  const model = el('div', { className: 'card' }, [
    el('div', { className: 'k' }, ['模型']),
    el('div', { className: 'v' }, [data.model.echo ? '未配置（回显）' : data.model.model]),
    el('div', { className: 'muted' }, [data.model.base_url || '']),
  ]);
  content.replaceChildren(
    el('h2', {}, ['概览']),
    el('p', { className: 'muted' }, [`版本 ${data.version} · 通道 ${data.channel}/${data.transport}`]),
    cards, model,
  );
}

async function renderSessions() {
  const data = await api('/sessions');
  const rows = data.items.map((s) => [
    s.id, s.chat_id, s.sender, s.messages, s.queued,
    s.active_run_id ? badge('运行中', 'warn') : badge('空闲'),
  ]);
  content.replaceChildren(
    el('h2', {}, ['会话']),
    el('p', { className: 'muted' }, ['当前为内存存储，重启后清空；点击行查看消息与运行。']),
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
    el('p', { className: 'muted' }, [id]),
    el('h3', {}, ['消息']),
    table(['ID', '状态', '内容'], msgRows),
    el('h3', {}, ['运行']),
    table(['ID', '状态', '输出'], runRows),
    el('div', { className: 'actions' }, [
      el('a', { href: '#/sessions', className: 'muted' }, ['← 返回会话列表']),
    ]),
  );
}

async function renderSkills() {
  const data = await api('/skills');
  const nameInput = el('input', { placeholder: '技能名，例如 incident-report' });
  const textarea = el('textarea', { placeholder: '# 标题\n> 一句话描述\n\n步骤…' });
  const list = el('div');

  async function select(name) {
    if (!name) return;
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
      toast('已保存');
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

  const rows = data.items.map((s) => [el('a', { href: '#' }, [s.name]), (s.content || '').split('\n')[0]]);
  if (rows.length) {
    const t = table(['名称', '首行'], rows, async (row) => {
      await select(row[0].textContent);
    });
    list.append(t);
  } else {
    list.append(el('p', { className: 'muted' }, ['还没有技能。']));
  }

  content.replaceChildren(
    el('h2', {}, ['技能']),
    el('p', { className: 'muted' }, ['Markdown 文件保存在 data/skills/；每次运行自动加载，无需重启。']),
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

  const rows = data.items.map((m) => [m.name, m.transport, m.command || m.url || '', m.enabled ? badge('启用', 'ok') : badge('停用')]);
  const list = rows.length ? table(['名称', '传输', '目标', '状态'], rows, async (row) => {
    const item = data.items.find((m) => m.name === row[0]);
    nameInput.value = item.name;
    transport.value = item.transport;
    command.value = item.command || '';
    args.value = (item.args || []).join(' ');
    url.value = item.url || '';
    enabled.checked = item.enabled;
  }) : el('p', { className: 'muted' }, ['还没有 MCP 配置。']);

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
    el('p', { className: 'muted' }, ['配置保存在 data/mcp.yaml。当前仅保存配置，运行时接入在下一阶段。']),
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
    el('p', { className: 'muted' }, ['以下为当前生效配置，修改 .env 后重启生效。']),
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
