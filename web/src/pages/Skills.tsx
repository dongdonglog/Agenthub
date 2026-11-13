import { useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, uploadZip, type Paged, type Skill } from "../api/client";
import { Badge, Breadcrumbs, Button, Card, Input, PageHeader, Skeleton, Tabs, useAsync, useConfirm, useToast } from "../components/ui";

const statusLabel: Record<string, { label: string; kind: string }> = {
  active: { label: "可关联", kind: "ok" },
  disabled: { label: "已停用", kind: "" },
  archived: { label: "已归档", kind: "warn" },
};

const formatBytes = (bytes: number) => {
  if (!bytes) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
};

export function Skills() {
  const { data, loading, error, reload } = useAsync<Paged<Skill>>(() => api("/skills?page=1&size=100"), []);
  const [filter, setFilter] = useState<"all" | "active" | "disabled" | "archived">("all");
  const fileRef = useRef<HTMLInputElement>(null);
  const { show } = useToast();
  const confirm = useConfirm();

  const setStatus = async (name: string, status: string) => {
    try {
      await api(`/skills/${encodeURIComponent(name)}/status`, { method: "PUT", body: JSON.stringify({ status }) });
      show(status === "active" ? "已启用" : status === "disabled" ? "已停用" : "已归档");
      reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const remove = async (name: string) => {
    if (!(await confirm({ title: `删除技能 ${name}？`, description: "文件会从数据目录删除，不可恢复。", danger: true, confirmText: "删除" }))) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}`, { method: "DELETE" });
      show("已删除");
      reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const items = (data?.items ?? []).filter((skill) => filter === "all" || skill.status === filter);

  return (
    <div className="stack">
      <PageHeader
        title="Skill"
        description="技能在部署内共享；编辑进入独立页面，支持文件树与 ZIP 导入。"
        actions={
          <>
            <Button onClick={() => fileRef.current?.click()}>导入 ZIP</Button>
            <Link className="btn primary" to="/skills/new">新建技能</Link>
          </>
        }
      />
      <input
        ref={fileRef}
        type="file"
        accept=".zip"
        style={{ display: "none" }}
        onChange={async (event) => {
          const file = event.target.files?.[0];
          if (!file) return;
          try {
            const result = await uploadZip(file);
            show(`已导入：${result.imported.join(", ")}`);
            reload();
          } catch (err) {
            show((err as Error).message, true);
          } finally {
            event.target.value = "";
          }
        }}
      />
      <Card>
        <Tabs
          value={filter}
          onChange={setFilter}
          items={[
            { value: "all", label: "全部" },
            { value: "active", label: "可关联" },
            { value: "disabled", label: "已停用" },
            { value: "archived", label: "已归档" },
          ]}
        />
        {loading && !data ? <Skeleton height={200} /> : null}
        {error ? <div className="danger-text">{error}</div> : null}
        {!loading && items.length === 0 ? <div className="empty"><div className="empty-icon">🧩</div><div>没有技能</div></div> : null}
        {items.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>名称</th><th>状态</th><th>文件 / 大小</th><th>可执行</th><th>引用</th><th>更新时间</th><th>操作</th></tr></thead>
              <tbody>
                {items.map((skill) => (
                  <tr key={skill.name}>
                    <td>
                      <div className="cell-main">{skill.name}</div>
                      <div className="cell-sub">{skill.description || "—"}</div>
                    </td>
                    <td><Badge kind={statusLabel[skill.status]?.kind}>{statusLabel[skill.status]?.label ?? skill.status}</Badge></td>
                    <td className="nowrap">{skill.files} / {formatBytes(skill.bytes)}</td>
                    <td>{skill.executable ? "是" : "否"}</td>
                    <td>{skill.references}</td>
                    <td className="nowrap muted">{skill.updated_at || "—"}</td>
                    <td>
                      <div className="row" style={{ gap: 10, flexWrap: "nowrap" }}>
                        <Link className="btn link" to={`/skills/${encodeURIComponent(skill.name)}`}>编辑</Link>
                        {skill.status === "active" ? (
                          <>
                            <button className="btn link" onClick={() => setStatus(skill.name, "disabled")}>停用</button>
                            <button className="btn link" onClick={() => setStatus(skill.name, "archived")}>归档</button>
                          </>
                        ) : (
                          <button className="btn link" onClick={() => setStatus(skill.name, "active")}>启用</button>
                        )}
                        <button className="btn link danger-text" onClick={() => remove(skill.name)}>删除</button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </Card>
    </div>
  );
}

type SkillFile = { path: string; size: number };

export function SkillDetail() {
  const { name = "" } = useParams();
  const isNew = name === "new";
  const { data, loading, error, reload } = useAsync<Skill | null>(
    () => (isNew ? Promise.resolve(null) : api(`/skills/${encodeURIComponent(name)}`)),
    [name],
  );
  const files = useAsync<Paged<SkillFile>>(
    () => (isNew ? Promise.resolve({ items: [], total: 0 }) : api(`/skills/${encodeURIComponent(name)}/files`)),
    [name],
  );
  const [current, setCurrent] = useState("SKILL.md");
  const [content, setContent] = useState("");
  const [draftName, setDraftName] = useState("");
  const [draftContent, setDraftContent] = useState("");
  const { show } = useToast();
  const confirm = useConfirm();
  const loaded = useRef<string | null>(null);

  const activeName = isNew ? draftName : name.toUpperCase() === "NEW" ? "" : name;

  if (!isNew && data && loaded.current !== data.name) {
    loaded.current = data.name;
    setContent(data.content);
    setCurrent("SKILL.md");
  }
  if (isNew && loaded.current !== "new") {
    loaded.current = "new";
  }

  const openFile = async (path: string) => {
    try {
      const file = await api<{ content: string }>(`/skills/${encodeURIComponent(name)}/files/${encodeURI(path)}`);
      setCurrent(path);
      setContent(file.content);
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const save = async () => {
    try {
      if (isNew) {
        if (!draftName.trim()) return show("先填技能名", true);
        await api(`/skills/${encodeURIComponent(draftName.trim())}`, { method: "PUT", body: JSON.stringify({ content: draftContent }) });
        show("已创建");
        window.location.hash = `#/skills/${encodeURIComponent(draftName.trim())}`;
        return;
      }
      await api(`/skills/${encodeURIComponent(name)}/files/${encodeURI(current)}`, {
        method: "PUT",
        body: JSON.stringify({ content }),
      });
      show("已保存，下一次运行生效");
      reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const newFile = async () => {
    const path = window.prompt("相对路径，例如 references/notes.md");
    if (!path) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}/files/${encodeURI(path)}`, { method: "PUT", body: JSON.stringify({ content: "" }) });
      files.reload();
      await openFile(path);
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const deleteFile = async () => {
    if (current === "SKILL.md") return show("SKILL.md 不能单独删除", true);
    if (!(await confirm({ title: `删除 ${current}？`, danger: true, confirmText: "删除" }))) return;
    try {
      await api(`/skills/${encodeURIComponent(name)}/files/${encodeURI(current)}`, { method: "DELETE" });
      files.reload();
      await openFile("SKILL.md");
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  if (loading && !isNew) return <Skeleton height={300} />;
  if (error) return <Card title="加载失败"><p className="danger-text">{error}</p></Card>;

  return (
    <div className="stack">
      <Breadcrumbs items={[{ label: "Skill", to: "/skills" }, { label: isNew ? "新建" : name }]} />
      <PageHeader
        title={isNew ? "新建技能" : data?.name ?? name}
        description={isNew ? "保存后会出现在技能列表。" : data?.description || "—"}
        actions={<Link className="btn" to="/skills">返回列表</Link>}
      />
      <Card>
        {isNew ? (
          <div className="form-grid">
            <div className="field"><label>名称</label><Input value={draftName} onChange={(event) => setDraftName(event.target.value)} placeholder="例如 incident-report" /></div>
            <div className="field"><label>内容</label><textarea className="textarea" rows={14} value={draftContent} onChange={(event) => setDraftContent(event.target.value)} /></div>
            <div className="form-actions"><Button className="primary" onClick={save}>创建</Button></div>
          </div>
        ) : (
          <div className="stack">
            <div className="file-chips">
              {(files.data?.items ?? []).map((file) => (
                <button key={file.path} className={`chip ${file.path === current ? "active" : ""}`} onClick={() => openFile(file.path)}>
                  {file.path}
                </button>
              ))}
            </div>
            <div className="row" style={{ justifyContent: "space-between" }}>
              <span className="muted small">当前文件：{current}</span>
              {data?.flat ? null : (
                <div className="row">
                  <Button onClick={newFile}>新建文件</Button>
                  <Button className="danger" onClick={deleteFile}>删除当前文件</Button>
                </div>
              )}
            </div>
            <textarea className="textarea" rows={18} value={content} onChange={(event) => setContent(event.target.value)} />
            <div className="form-actions"><Button className="primary" onClick={save}>保存文件</Button></div>
          </div>
        )}
      </Card>
    </div>
  );
}
