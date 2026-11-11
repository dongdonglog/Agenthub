const content = document.getElementById('content');
const statusEl = document.getElementById('status');
const statusText = document.getElementById('statusText');
const nav = document.getElementById('nav');

async function api(path, options = {}) {
  const res = await fetch('/admin/api/v1' + path, {
    headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'agenthub' },
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
  for (const h of headers) {
    const th = el('th', {}, [h]);
    th.style.whiteSpace = 'nowrap';
    hr.append(th);
  }
  thead.append(hr);
  const tbody = el('tbody');
  for (const row of rows) {
    const tr = el('tr', onClick ? { className: 'click' } : {});
    row.forEach((cell, idx) => {
      const td = el('td', {}, [cell instanceof Node ? cell : String(cell ?? '')]);
      if (idx > 0) td.style.whiteSpace = 'nowrap';
      tr.append(td);
    });
    if (onClick) tr.addEventListener('click', () => onClick(row));
    tbody.append(tr);
  }
  t.append(thead, tbody);
  return el('div', { className: 'table-wrap' }, [t]);
}

function pager(total, page, size, base) {
  const pages = Math.max(1, Math.ceil(total / size));
  const wrap = el('div', { className: 'pager' });
  const prev = el('button', { className: 'ghost' }, ['上一页']);
  prev.disabled = page <= 1;
  prev.addEventListener('click', () => { location.hash = `${base}/${page - 1}`; });
  const next = el('button', { className: 'ghost' }, ['下一页']);
  next.disabled = page >= pages;
  next.addEventListener('click', () => { location.hash = `${base}/${page + 1}`; });
  wrap.append(prev, el('span', { className: 'muted' }, [`第 ${page} / ${pages} 页 · 共 ${total} 条`]), next);
  return wrap;
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
    if (page === 'sessions') return rest[0] ? await renderSession(decodeURIComponent(rest[0])) : await renderSessions(Number(rest[0]) || 1);
    if (page === 'runs') return await renderRun(rest[0]);
    if (page === 'skills') return rest[0] ? await renderSkillDetail(decodeURIComponent(rest[0])) : await renderSkills(Number(rest[1]) || 1);
    if (page === 'history') return rest[0] ? await renderHistoryDetail(decodeURIComponent(rest[0]), rest[1]) : await renderHistory(Number(rest[1]) || 1);
    if (page === 'profiles') return await renderProfiles();
    if (page === 'audits') return await renderAudits();
    if (page === 'mcps') return rest[0] ? await renderMCPDetail(decodeURIComponent(rest[0])) : await renderMCPs(Number(rest[1]) || 1);
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
      el('div', { className: 'head' }, [el('b', {}, ['模型']), el('span', { className: 'muted' }, [`Profile：${data.active_profile || '未指定'} · `, el('a', { href: '#/settings' }, ['去配置'])])]),
      el('div', { className: 'muted' }, data.model.echo ? '未配置 API Key，当前为回显模式' : `${data.model.model} · ${data.model.base_url}`),
    ]),
    el('div', { className: 'panel' }, [
      el('div', { className: 'head' }, [el('b', {}, ['在线试跑']), el('span', { className: 'muted' }, ['验证模型与技能是否生效'])]),
      el('div', { className: 'form' }, [testInput, el('div', { className: 'actions' }, [runBtn])]),
      result,
    ]),
  );
}

async function renderSessions(page = 1) {
  const size = 20;
  const data = await api(`/sessions?page=${page}&size=${size}`);
  const rows = data.items.map((it) => {
    const name = el('div', { className: 'strong' }, [it.display_name || it.id]);
    name.dataset.id = it.id;
    return [name, it.messages || 0, it.last_active || '—', it.id];
  });
  content.replaceChildren(
    el('h2', {}, ['在线会话']),
    el('p', { className: 'lede' }, ['显示飞书群名与发送人；点击进入会话详情。']),
    rows.length
      ? table(['会话', '消息', '最近活跃', '会话 ID'], rows, (row) => {
        location.hash = `#/sessions/${encodeURIComponent(row[0].dataset.id)}`;
      })
      : empty('💬', '还没有会话。'),
    pager(data.total, page, size, '#/sessions'),
  );
}

async function renderSession(id) {
  const [data, profiles] = await Promise.all([
    api(`/sessions/${encodeURIComponent(id)}`),
    api('/profiles'),
  ]);
  const msgRows = data.messages.map((m) => [m.ID || m.id, m.Status || m.status, (m.Text || m.text || '').slice(0, 60)]);
  const runRows = data.runs.map((r) => [r.ID || r.id, r.State || r.state, (r.Output || r.output || '').slice(0, 60)]);
  const profileSelect = el('select');
  for (const p of profiles.items) {
    profileSelect.append(el('option', { value: p.name }, [p.name]));
  }
  if (data.profile) profileSelect.value = data.profile;
  const applyProfile = el('button', {}, ['应用 Profile']);
  applyProfile.addEventListener('click', async () => {
    try {
      await api(`/sessions/${encodeURIComponent(id)}/profile`, {
        method: 'PUT',
        body: JSON.stringify({ name: profileSelect.value }),
      });
      toast(`已应用到 ${profileSelect.value}`);
      await renderSession(id);
    } catch (err) { toast(err.message, true); }
  });

  content.replaceChildren(
    el('h2', {}, ['会话详情']),
    el('p', { className: 'lede' }, [id, data.profile ? ` · Profile：${data.profile}` : '']),
    el('div', { className: 'panel' }, [
      el('div', { className: 'head' }, [el('b', {}, ['配置档案']), el('span', { className: 'muted' }, ['只影响当前会话代'])]),
      el('div', { className: 'actions' }, [profileSelect, applyProfile]),
    ]),
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

function formatBytes(n) {
  if (!n) return '0';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function skillStatusBadge(status) {
  if (status === 'active') return badge('可关联', 'ok');
  if (status === 'disabled') return badge('已停用');
  return badge('已归档', 'warn');
}

async function renderSkills(page = 1) {
  const size = 20;
  const data = await api(`/skills?page=${page}&size=${size}`);
  const list = el('div');

  async function changeStatus(name, status) {
    try {
      await api(`/skills/${encodeURIComponent(name)}/status`, { method: 'PUT', body: JSON.stringify({ status }) });
      toast(status === 'disabled' ? '已停用' : status === 'archived' ? '已归档' : '已启用');
      location.reload();
    } catch (err) { toast(err.message, true); }
  }

  async function remove(name) {
    if (!confirm(`删除技能 ${name}？`)) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}`, { method: 'DELETE' });
      toast('已删除');
      location.reload();
    } catch (err) { toast(err.message, true); }
  }

  const rows = data.items.map((sk) => {
    const name = el('div', {}, [
      el('div', { className: 'strong' }, [sk.name]),
      el('div', { className: 'muted small' }, [sk.description || '—']),
    ]);
    name.dataset.name = sk.name;
    const actions = el('div', { className: 'row-actions', style: 'display:flex;gap:10px;white-space:nowrap' });
    const edit = el('button', { className: 'link' }, ['编辑']);
    edit.addEventListener('click', (e) => { e.stopPropagation(); location.hash = `#/skills/${encodeURIComponent(sk.name)}`; });
    actions.append(edit);
    if (sk.status === 'active') {
      const dis = el('button', { className: 'link' }, ['停用']);
      dis.addEventListener('click', (e) => { e.stopPropagation(); changeStatus(sk.name, 'disabled'); });
      const arc = el('button', { className: 'link' }, ['归档']);
      arc.addEventListener('click', (e) => { e.stopPropagation(); changeStatus(sk.name, 'archived'); });
      actions.append(dis, arc);
    } else {
      const en = el('button', { className: 'link' }, ['启用']);
      en.addEventListener('click', (e) => { e.stopPropagation(); changeStatus(sk.name, 'active'); });
      actions.append(en);
    }
    const del = el('button', { className: 'link danger' }, ['删除']);
    del.addEventListener('click', (e) => { e.stopPropagation(); remove(sk.name); });
    actions.append(del);
    return [name, skillStatusBadge(sk.status), `${sk.files} / ${formatBytes(sk.bytes)}`, sk.executable ? '是' : '否', sk.references || 0, sk.updated_at || '—', actions];
  });

  const importInput = el('input', { type: 'file', accept: '.zip', style: 'display:none' });
  importInput.addEventListener('change', async () => {
    const file = importInput.files[0];
    if (!file) return;
    const form = new FormData();
    form.append('file', file);
    try {
      const res = await fetch('/admin/api/v1/skills/import?overwrite=1', {
        method: 'POST', body: form, headers: { 'X-Requested-With': 'agenthub' },
      });
      const body = await res.json();
      if (!res.ok) throw new Error(body.error || '导入失败');
      toast(`已导入：${body.imported.join(', ')}`);
      location.reload();
    } catch (err) { toast(err.message, true); }
  });

  const importBtn = el('button', { className: 'ghost' }, ['导入 ZIP']);
  importBtn.addEventListener('click', () => importInput.click());

  list.append(rows.length
    ? table(['名称', '状态', '文件/大小', '可执行', '引用 Profile', '更新时间', '操作'], rows)
    : empty('🧩', '还没有技能。可以导入 ZIP，或在详情页新建。'));

  content.replaceChildren(
    el('h2', {}, ['Skill']),
    el('p', { className: 'lede' }, ['技能包按目录管理；点「编辑」进入独立编辑页。']),
    el('div', { className: 'actions' }, [importBtn, importInput]),
    list,
    pager(data.total, page, size, '#/skills'),
  );
}

async function renderSkillDetail(name) {
  const skill = await api(`/skills/${encodeURIComponent(name)}`);
  const files = await api(`/skills/${encodeURIComponent(name)}/files`);
  const textarea = el('textarea', { rows: '18' });
  const fileList = el('div', { className: 'file-list' });
  const currentLabel = el('div', { className: 'muted small' }, ['当前文件：SKILL.md']);
  let currentFile = 'SKILL.md';

  async function loadFile(path) {
    try {
      const f = await api(`/skills/${encodeURIComponent(name)}/files/${encodeURI(path)}`);
      currentFile = path;
      textarea.value = f.content;
      currentLabel.textContent = `当前文件：${path}`;
    } catch (err) { toast(err.message, true); }
  }

  for (const f of files.items) {
    const chip = el('button', { className: 'chip' }, [f.path]);
    chip.addEventListener('click', () => loadFile(f.path));
    fileList.append(chip);
  }
  await loadFile('SKILL.md');

  const save = el('button', {}, ['保存文件']);
  save.addEventListener('click', async () => {
    try {
      await api(`/skills/${encodeURIComponent(name)}/files/${encodeURI(currentFile)}`, {
        method: 'PUT', body: JSON.stringify({ content: textarea.value }),
      });
      toast('已保存，下一次运行生效');
    } catch (err) { toast(err.message, true); }
  });

  const newFile = el('button', { className: 'ghost' }, ['新建文件']);
  newFile.addEventListener('click', async () => {
    const path = prompt('相对路径，例如 references/notes.md');
    if (!path) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}/files/${encodeURI(path)}`, { method: 'PUT', body: JSON.stringify({ content: '' }) });
      location.reload();
    } catch (err) { toast(err.message, true); }
  });

  const delFile = el('button', { className: 'danger' }, ['删除当前文件']);
  delFile.addEventListener('click', async () => {
    if (currentFile === 'SKILL.md') return toast('SKILL.md 不能单独删除', true);
    if (!confirm(`删除 ${currentFile}？`)) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}/files/${encodeURI(currentFile)}`, { method: 'DELETE' });
      location.reload();
    } catch (err) { toast(err.message, true); }
  });

  const back = el('a', { href: '#/skills', className: 'muted' }, ['← 返回技能列表']);

  content.replaceChildren(
    el('h2', {}, [skill.name]),
    el('p', { className: 'lede' }, [skill.description || '—']),
    el('div', { className: 'actions' }, [back]),
    el('div', { className: 'file-bar' }, [currentLabel, el('div', { className: 'actions' }, [newFile, delFile])]),
    fileList,
    el('label', {}, ['内容', textarea]),
    el('div', { className: 'actions' }, [save]),
  );
}

async function renderMCPs(page = 1) {
  const size = 20;
  const data = await api(`/mcps?page=${page}&size=${size}`);
  const rows = data.items.map((m) => {
    const name = el('div', { className: 'strong' }, [m.name]);
    name.dataset.name = m.name;
    return [name, m.transport, m.command || m.url || '', m.enabled ? badge('启用', 'ok') : badge('停用')];
  });
  const create = el('button', {}, ['新建 MCP']);
  create.addEventListener('click', () => { location.hash = '#/mcps/new'; });

  content.replaceChildren(
    el('h2', {}, ['MCP']),
    el('p', { className: 'lede' }, ['配置保存在 data/mcp.yaml；点「编辑」进入独立编辑页。']),
    el('div', { className: 'actions' }, [create]),
    rows.length
      ? table(['名称', '传输', '目标', '状态'], rows, (row) => {
        location.hash = `#/mcps/${encodeURIComponent(row[0].dataset.name)}`;
      })
      : empty('🔌', '还没有 MCP 配置。'),
    pager(data.total, page, size, '#/mcps'),
  );
}

async function renderMCPDetail(name) {
  const data = await api('/mcps?size=100');
  const existing = name === 'new' ? null : data.items.find((m) => m.name === name);
  if (name !== 'new' && !existing) {
    content.replaceChildren(empty('🔌', 'MCP 不存在'), el('a', { href: '#/mcps', className: 'muted' }, ['← 返回列表']));
    return;
  }

  const nameInput = el('input', { placeholder: '名称', value: existing ? existing.name : '', disabled: !!existing });
  const transport = el('select');
  transport.append(el('option', { value: 'stdio' }, ['stdio']), el('option', { value: 'http' }, ['http']));
  transport.value = existing ? existing.transport : 'stdio';
  const command = el('input', { placeholder: 'stdio 命令，例如 npx', value: existing?.command || '' });
  const args = el('input', { placeholder: '参数，空格分隔', value: (existing?.args || []).join(' ') });
  const url = el('input', { placeholder: 'http 地址', value: existing?.url || '' });
  const enabled = el('input', { type: 'checkbox', checked: existing ? existing.enabled : true });

  const save = el('button', {}, ['保存']);
  save.addEventListener('click', async () => {
    const key = existing ? existing.name : nameInput.value.trim();
    if (!key) return toast('先填名称', true);
    try {
      await api(`/mcps/${encodeURIComponent(key)}`, {
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
      location.hash = '#/mcps';
    } catch (err) { toast(err.message, true); }
  });

  const del = el('button', { className: 'danger' }, ['删除']);
  del.addEventListener('click', async () => {
    if (!existing || !confirm(`删除 MCP ${existing.name}？`)) return;
    try {
      await api(`/mcps/${encodeURIComponent(existing.name)}`, { method: 'DELETE' });
      toast('已删除');
      location.hash = '#/mcps';
    } catch (err) { toast(err.message, true); }
  });

  content.replaceChildren(
    el('h2', {}, [existing ? `MCP · ${existing.name}` : '新建 MCP']),
    el('a', { href: '#/mcps', className: 'muted' }, ['← 返回 MCP 列表']),
    el('div', { className: 'form' }, [
      el('label', {}, ['名称', nameInput]),
      el('label', {}, ['传输方式', transport]),
      el('label', {}, ['命令', command]),
      el('label', {}, ['参数', args]),
      el('label', {}, ['地址', url]),
      el('label', {}, ['启用', enabled]),
      el('div', { className: 'actions' }, existing ? [save, del] : [save]),
    ]),
  );
}

async function renderHistory(page = 1) {
  const size = 20;
  const data = await api(`/history?page=${page}&size=${size}`);
  if (!data.items.length) {
    content.replaceChildren(el('h2', {}, ['历史会话']), empty('🗂️', '还没有历史记录。'));
    return;
  }
  const rows = data.items.map((h) => [
    h.conversation_id, h.generation, h.messages,
    new Date(h.last_active).toLocaleString(),
  ]);
  content.replaceChildren(
    el('h2', {}, ['历史会话']),
    el('p', { className: 'lede' }, ['按会话与代聚合；/reset 后旧代仍保留在这里。']),
    table(['会话', '代', '消息数', '最近活跃'], rows, (row) => {
      location.hash = `#/history/${encodeURIComponent(row[0])}/${row[1]}`;
    }),
    pager(data.total, page, size, '#/history'),
  );
}

async function renderHistoryDetail(conv, gen) {
  const data = await api(`/history/${encodeURIComponent(conv)}/${gen}`);
  const rows = data.items.map((m) => [
    new Date(m.CreatedAt || m.created_at).toLocaleString(),
    m.Role || m.role,
    (m.Text || m.text || '').slice(0, 120),
  ]);
  content.replaceChildren(
    el('h2', {}, ['历史详情']),
    el('p', { className: 'lede' }, [`${conv} · 第 ${gen} 代 · ${data.total} 条消息`]),
    rows.length ? table(['时间', '角色', '内容'], rows) : empty('📭', '没有消息'),
    el('div', { className: 'actions' }, [el('a', { href: '#/history', className: 'muted' }, ['← 返回历史列表'])]),
  );
}

async function renderProfiles() {
  const data = await api('/profiles');
  const nameInput = el('input', { placeholder: 'Profile 名称，例如 default' });
  const descInput = el('input', { placeholder: '描述' });
  const skillsInput = el('input', { placeholder: '技能名，逗号分隔' });
  const mcpInput = el('input', { placeholder: 'MCP 名，逗号分隔' });

  const rows = data.items.map((p) => {
    const actions = el('div', { className: 'row-actions', style: 'display:flex;gap:10px' });
    if (p.name === data.active) {
      actions.append(badge('生效中', 'ok'));
    } else {
      const apply = el('button', { className: 'link' }, ['应用']);
      apply.addEventListener('click', async (e) => {
        e.stopPropagation();
        try {
          await api(`/profiles/${encodeURIComponent(p.name)}/apply`, { method: 'PUT' });
          toast(`已应用 ${p.name}`);
          await renderProfiles();
        } catch (err) { toast(err.message, true); }
      });
      actions.append(apply);
    }
    return [
      el('div', {}, [el('div', { className: 'strong' }, [p.name]), el('div', { className: 'muted small' }, [p.description || '—'])]),
      (p.skills || []).length,
      (p.mcps || []).length,
      actions,
    ];
  });
  const list = rows.length ? table(['名称', '技能数', 'MCP 数', '操作'], rows, (row) => {
    const p = data.items.find((x) => x.name === row[0].textContent);
    nameInput.value = p.name;
    descInput.value = p.description || '';
    skillsInput.value = (p.skills || []).join(', ');
    mcpInput.value = (p.mcps || []).join(', ');
  }) : empty('👤', '还没有 Profile。');

  const save = el('button', {}, ['保存']);
  save.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name) return toast('先填名称', true);
    try {
      await api(`/profiles/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({
          description: descInput.value.trim(),
          skills: skillsInput.value.split(',').map((x) => x.trim()).filter(Boolean),
          mcps: mcpInput.value.split(',').map((x) => x.trim()).filter(Boolean),
        }),
      });
      toast('已保存');
      await renderProfiles();
    } catch (err) { toast(err.message, true); }
  });

  const del = el('button', { className: 'danger' }, ['删除']);
  del.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name || !confirm(`删除 Profile ${name}？`)) return;
    try {
      await api(`/profiles/${encodeURIComponent(name)}`, { method: 'DELETE' });
      toast('已删除');
      await renderProfiles();
    } catch (err) { toast(err.message, true); }
  });

  content.replaceChildren(
    el('h2', {}, ['Profile']),
    el('p', { className: 'lede' }, ['应用后立即影响后续运行：只注入该 Profile 的技能、只暴露它列出的 MCP 工具。']),
    list,
    el('h3', {}, ['编辑']),
    el('div', { className: 'form' }, [
      el('label', {}, ['名称', nameInput]),
      el('label', {}, ['描述', descInput]),
      el('label', {}, ['技能（逗号分隔）', skillsInput]),
      el('label', {}, ['MCP（逗号分隔）', mcpInput]),
      el('div', { className: 'actions' }, [save, del]),
    ]),
  );
}

async function renderAudits() {
  const data = await api('/audits');
  if (!data.items.length) {
    content.replaceChildren(el('h2', {}, ['审计']), empty('📜', '还没有审计记录。修改技能、MCP 或 Profile 后会记录在这里。'));
    return;
  }
  const rows = data.items.map((a) => [
    new Date(a.created_at).toLocaleString(), a.actor || '—', a.action, a.target, a.detail || '',
  ]);
  content.replaceChildren(
    el('h2', {}, ['审计']),
    el('p', { className: 'lede' }, ['管理面写操作只追加记录，用于追溯。']),
    table(['时间', '操作者', '动作', '对象', '详情'], rows),
  );
}

async function renderSettings() {
  const [data, model] = await Promise.all([api('/settings'), api('/model')]);

  const baseURL = el('input', { value: model.base_url || '', placeholder: 'https://api.openai.com/v1' });
  const modelName = el('input', { value: model.model || '', placeholder: 'gpt-4.1-mini' });
  const fallback = el('input', { value: model.fallback_model || '', placeholder: '可选，主模型失败时使用' });
  const vision = el('input', { value: model.vision_model || '', placeholder: '可选，图片消息使用' });
  const apiKey = el('input', { type: 'password', placeholder: model.has_key ? `已配置（${model.key_hint}），留空保持不变` : '未配置' });


  const save = el('button', {}, ['保存模型配置']);
  save.addEventListener('click', async () => {
    try {
      await api('/model', {
        method: 'PUT',
        body: JSON.stringify({
          base_url: baseURL.value.trim(),
          model: modelName.value.trim(),
          fallback_model: fallback.value.trim(),
          vision_model: vision.value.trim(),
          api_key: apiKey.value.trim(),
        }),
      });
      toast('已保存，下一次运行生效');
      await renderSettings();
    } catch (err) { toast(err.message, true); }
  });

  content.replaceChildren(
    el('h2', {}, ['设置']),
    el('p', { className: 'lede' }, ['模型配置可在下方直接修改，保存后下一次运行生效；其余配置仍由 .env 控制。']),
    el('h3', {}, ['模型']),
    el('div', { className: 'form' }, [
      el('label', {}, ['Base URL', baseURL]),
      el('label', {}, ['模型', modelName]),
      el('label', {}, ['备用模型', fallback]),
      el('label', {}, ['视觉模型', vision]),
      el('label', {}, ['API Key', apiKey]),
      el('div', { className: 'actions' }, [save]),
    ]),
    el('h3', {}, ['飞书']),
    el('pre', {}, [JSON.stringify(data.feishu, null, 2)]),
    el('h3', {}, ['存储']),
    el('pre', {}, [JSON.stringify(data.storage, null, 2)]),
  );
}

window.addEventListener('hashchange', render);
render();
