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
    if (page === 'history') return await renderHistory();
    if (page === 'profiles') return await renderProfiles();
    if (page === 'audits') return await renderAudits();
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
      el('div', { className: 'head' }, [el('b', {}, ['模型']), el('span', { className: 'muted' }, [`Profile：${data.active_profile || '未指定'}`])]),
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

async function renderSkills() {
  const data = await api('/skills');
  const nameInput = el('input', { placeholder: '技能名，例如 incident-report' });
  const textarea = el('textarea', { placeholder: '# 标题\n> 一句话描述\n\n步骤…' });
  const fileList = el('div', { className: 'file-list' });
  const fileLabel = el('div', { className: 'muted small' }, ['当前文件：—']);
  const list = el('div');
  let currentSkill = '';
  let currentFile = 'SKILL.md';

  async function loadFile(path) {
    if (!currentSkill) return;
    try {
      const f = await api(`/skills/${encodeURIComponent(currentSkill)}/files/${encodeURI(path)}`);
      currentFile = path;
      textarea.value = f.content;
      fileLabel.textContent = `当前文件：${path}`;
    } catch (err) { toast(err.message, true); }
  }

  async function select(name) {
    try {
      const item = await api(`/skills/${encodeURIComponent(name)}`);
      currentSkill = name;
      nameInput.value = item.name;
      const files = await api(`/skills/${encodeURIComponent(name)}/files`);
      fileList.replaceChildren();
      for (const f of files.items) {
        const b = el('button', { className: 'chip' + (f.path === 'SKILL.md' ? ' primary' : '') }, [f.path]);
        b.addEventListener('click', () => loadFile(f.path));
        fileList.append(b);
      }
      await loadFile('SKILL.md');
    } catch (err) { toast(err.message, true); }
  }

  async function changeStatus(name, status) {
    try {
      await api(`/skills/${encodeURIComponent(name)}/status`, {
        method: 'PUT',
        body: JSON.stringify({ status }),
      });
      toast(status === 'disabled' ? '已停用' : status === 'archived' ? '已归档' : '已启用');
      await renderSkills();
    } catch (err) { toast(err.message, true); }
  }

  async function remove(name) {
    if (!confirm(`删除技能 ${name}？`)) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}`, { method: 'DELETE' });
      toast('已删除');
      await renderSkills();
    } catch (err) { toast(err.message, true); }
  }

  const rows = data.items.map((s) => {
    const actions = el('div', { className: 'row-actions', style: 'display:flex;gap:10px;white-space:nowrap' });
    const edit = el('button', { className: 'link' }, ['编辑']);
    edit.addEventListener('click', (e) => { e.stopPropagation(); select(s.name); });
    actions.append(edit);
    if (s.status === 'active') {
      const dis = el('button', { className: 'link' }, ['停用']);
      dis.addEventListener('click', (e) => { e.stopPropagation(); changeStatus(s.name, 'disabled'); });
      actions.append(dis);
      const arc = el('button', { className: 'link' }, ['归档']);
      arc.addEventListener('click', (e) => { e.stopPropagation(); changeStatus(s.name, 'archived'); });
      actions.append(arc);
    } else {
      const en = el('button', { className: 'link' }, ['启用']);
      en.addEventListener('click', (e) => { e.stopPropagation(); changeStatus(s.name, 'active'); });
      actions.append(en);
    }
    const del = el('button', { className: 'link danger' }, ['删除']);
    del.addEventListener('click', (e) => { e.stopPropagation(); remove(s.name); });
    actions.append(del);

    return [
      el('div', {}, [el('div', { className: 'strong' }, [s.name]), el('div', { className: 'muted small' }, [s.description || '—'])]),
      skillStatusBadge(s.status),
      `${s.files} / ${formatBytes(s.bytes)}`,
      s.executable ? '是' : '否',
      s.references || 0,
      s.updated_at || '—',
      actions,
    ];
  });

  if (rows.length) {
    list.append(table(['名称', '状态', '文件/大小', '可执行', '引用 Profile', '更新时间', '操作'], rows));
  } else {
    list.append(empty('🧩', '还没有技能。下面填名称和内容保存即可。'));
  }

  const save = el('button', {}, ['保存']);
  save.addEventListener('click', async () => {
    const name = nameInput.value.trim();
    if (!name) return toast('先填技能名', true);
    try {
      if (!currentSkill || currentSkill !== name) {
        await api(`/skills/${encodeURIComponent(name)}`, {
          method: 'PUT',
          body: JSON.stringify({ content: textarea.value }),
        });
      } else {
        await api(`/skills/${encodeURIComponent(currentSkill)}/files/${encodeURI(currentFile)}`, {
          method: 'PUT',
          body: JSON.stringify({ content: textarea.value }),
        });
      }
      toast('已保存，下次运行生效');
      await renderSkills();
    } catch (err) { toast(err.message, true); }
  });

  const newFile = el('button', { className: 'ghost' }, ['新建文件']);
  newFile.addEventListener('click', async () => {
    if (!currentSkill) return toast('先选择一个技能', true);
    const path = prompt('相对路径，例如 references/notes.md');
    if (!path) return;
    try {
      await api(`/skills/${encodeURIComponent(currentSkill)}/files/${encodeURI(path)}`, {
        method: 'PUT',
        body: JSON.stringify({ content: '' }),
      });
      await select(currentSkill);
    } catch (err) { toast(err.message, true); }
  });

  const delFile = el('button', { className: 'danger' }, ['删除当前文件']);
  delFile.addEventListener('click', async () => {
    if (!currentSkill || currentFile === 'SKILL.md') return toast('SKILL.md 不能单独删除', true);
    if (!confirm(`删除 ${currentFile}？`)) return;
    try {
      await api(`/skills/${encodeURIComponent(currentSkill)}/files/${encodeURI(currentFile)}`, { method: 'DELETE' });
      await select(currentSkill);
    } catch (err) { toast(err.message, true); }
  });

  content.replaceChildren(
    el('h2', {}, ['Skill']),
    el('p', { className: 'lede' }, ['技能存于 data/skills/：可关联的会注入提示词，停用/归档的保留文件但不参与运行。']),
    list,
    el('h3', {}, ['编辑']),
    el('div', { className: 'form' }, [
      el('label', {}, ['名称', nameInput]),
      el('div', { className: 'file-bar' }, [fileLabel, el('div', { className: 'actions' }, [newFile, delFile])]),
      fileList,
      el('label', {}, ['内容', textarea]),
      el('div', { className: 'actions' }, [save]),
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

async function renderHistory() {
  const data = await api('/history');
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
    table(['会话', '代', '消息数', '最近活跃'], rows),
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
    new Date(a.created_at).toLocaleString(), a.action, a.target, a.detail || '',
  ]);
  content.replaceChildren(
    el('h2', {}, ['审计']),
    el('p', { className: 'lede' }, ['管理面写操作只追加记录，用于追溯。']),
    table(['时间', '动作', '对象', '详情'], rows),
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
