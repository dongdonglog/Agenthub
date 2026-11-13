import { useState } from "react";
import { api } from "../api/client";
import { Badge, Button, Card, Input, PageHeader, Select, Tabs, useAsync, useToast } from "../components/ui";

type Provider = { name: string; base_url: string; api_key?: string };
type Model = { id: string; provider: string; model: string; capabilities: string[]; context_window: number; max_tokens: number };
type Route = { name: string; candidates: string[]; retry: { max_attempts: number; backoff_ms: number }; circuit: { failures: number; cooldown_seconds: number } };
type Config = { version: number; providers: Provider[]; models: Model[]; routes: Route[] };
type Health = { route: string; candidate: string; open: boolean; failures: number; last_error: string; last_latency_ms: number };

type Payload = { config: Config; health: Health[] };

const CAPS = ["text", "vision", "tools"];

export function Settings() {
  const { data, loading, reload } = useAsync<Payload>(() => api("/model"), []);
  const [tab, setTab] = useState<"providers" | "models" | "routes">("providers");
  const [config, setConfig] = useState<Config | null>(null);
  const [dirty, setDirty] = useState(false);
  const { show } = useToast();

  if (data?.config && !config) setConfig(data.config);

  const update = (next: Config) => {
    setConfig(next);
    setDirty(true);
  };

  const save = async () => {
    if (!config) return;
    try {
      await api("/model", { method: "PUT", body: JSON.stringify(config) });
      show("已保存，下一次运行生效");
      setDirty(false);
      reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  const testRoute = async (route: string) => {
    try {
      await api("/model/test", { method: "POST", body: JSON.stringify({ route }) });
      show(`线路 ${route} 测试通过`);
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  if (loading && !config) return <PageHeader title="设置" />;
  if (!config) return <Card title="加载失败"><Button onClick={reload}>重试</Button></Card>;

  return (
    <div className="stack">
      <PageHeader
        title="设置"
        description="多供应商 + 多模型 + 有序线路；主候选失败时自动切换到备选。"
        actions={<Button className="primary" onClick={save} disabled={!dirty}>{dirty ? "保存配置" : "已保存"}</Button>}
      />

      <Card>
        <Tabs
          value={tab}
          onChange={setTab}
          items={[
            { value: "providers", label: "供应商" },
            { value: "models", label: "模型" },
            { value: "routes", label: "线路" },
          ]}
        />

        {tab === "providers" ? (
          <div className="stack">
            {config.providers.map((provider, index) => (
              <div className="card" key={index} style={{ boxShadow: "none" }}>
                <div className="grid grid-3">
                  <div className="field"><label>名称</label><Input value={provider.name} onChange={(event) => {
                    const providers = [...config.providers];
                    providers[index] = { ...provider, name: event.target.value };
                    update({ ...config, providers });
                  }} /></div>
                  <div className="field"><label>Base URL</label><Input value={provider.base_url} onChange={(event) => {
                    const providers = [...config.providers];
                    providers[index] = { ...provider, base_url: event.target.value };
                    update({ ...config, providers });
                  }} /></div>
                  <div className="field"><label>API Key</label><Input type="password" placeholder="留空保持不变" value={provider.api_key ?? ""} onChange={(event) => {
                    const providers = [...config.providers];
                    providers[index] = { ...provider, api_key: event.target.value };
                    update({ ...config, providers });
                  }} /></div>
                </div>
                <div className="form-actions">
                  <Button className="danger" onClick={() => update({ ...config, providers: config.providers.filter((_, i) => i !== index) })}>删除供应商</Button>
                </div>
              </div>
            ))}
            <div>
              <Button onClick={() => update({ ...config, providers: [...config.providers, { name: `provider-${config.providers.length + 1}`, base_url: "", api_key: "" }] })}>新增供应商</Button>
            </div>
          </div>
        ) : null}

        {tab === "models" ? (
          <div className="stack">
            {config.models.map((model, index) => (
              <div className="card" key={index} style={{ boxShadow: "none" }}>
                <div className="grid grid-4">
                  <div className="field"><label>模型 ID</label><Input value={model.id} onChange={(event) => {
                    const models = [...config.models];
                    models[index] = { ...model, id: event.target.value };
                    update({ ...config, models });
                  }} /></div>
                  <div className="field"><label>供应商</label>
                    <Select value={model.provider} onChange={(event) => {
                      const models = [...config.models];
                      models[index] = { ...model, provider: event.target.value };
                      update({ ...config, models });
                    }}>
                      {config.providers.map((provider) => <option key={provider.name} value={provider.name}>{provider.name}</option>)}
                    </Select>
                  </div>
                  <div className="field"><label>上游模型</label><Input value={model.model} onChange={(event) => {
                    const models = [...config.models];
                    models[index] = { ...model, model: event.target.value };
                    update({ ...config, models });
                  }} /></div>
                  <div className="field"><label>上下文 / 输出上限</label>
                    <div className="row">
                      <Input type="number" value={model.context_window} onChange={(event) => {
                        const models = [...config.models];
                        models[index] = { ...model, context_window: Number(event.target.value) };
                        update({ ...config, models });
                      }} />
                      <Input type="number" value={model.max_tokens} onChange={(event) => {
                        const models = [...config.models];
                        models[index] = { ...model, max_tokens: Number(event.target.value) };
                        update({ ...config, models });
                      }} />
                    </div>
                  </div>
                </div>
                <div className="row" style={{ gap: 14, marginTop: 8 }}>
                  {CAPS.map((cap) => (
                    <label key={cap} className="row" style={{ gap: 6 }}>
                      <input type="checkbox" checked={model.capabilities.includes(cap)} onChange={(event) => {
                        const caps = event.target.checked ? [...model.capabilities, cap] : model.capabilities.filter((item) => item !== cap);
                        const models = [...config.models];
                        models[index] = { ...model, capabilities: caps };
                        update({ ...config, models });
                      }} />
                      {cap}
                    </label>
                  ))}
                  <Button className="link danger-text" onClick={() => update({ ...config, models: config.models.filter((_, i) => i !== index) })}>删除模型</Button>
                </div>
              </div>
            ))}
            <div>
              <Button onClick={() => update({ ...config, models: [...config.models, { id: `model-${config.models.length + 1}`, provider: config.providers[0]?.name ?? "", model: "", capabilities: ["text"], context_window: 128000, max_tokens: 8192 }] })}>新增模型</Button>
            </div>
          </div>
        ) : null}

        {tab === "routes" ? (
          <div className="stack">
            {config.routes.map((route, index) => (
              <div className="card" key={index} style={{ boxShadow: "none" }}>
                <div className="grid grid-3">
                  <div className="field"><label>线路名</label><Input value={route.name} onChange={(event) => {
                    const routes = [...config.routes];
                    routes[index] = { ...route, name: event.target.value };
                    update({ ...config, routes });
                  }} /></div>
                  <div className="field"><label>重试次数 / 退避 ms</label>
                    <div className="row">
                      <Input type="number" value={route.retry.max_attempts} onChange={(event) => {
                        const routes = [...config.routes];
                        routes[index] = { ...route, retry: { ...route.retry, max_attempts: Number(event.target.value) } };
                        update({ ...config, routes });
                      }} />
                      <Input type="number" value={route.retry.backoff_ms} onChange={(event) => {
                        const routes = [...config.routes];
                        routes[index] = { ...route, retry: { ...route.retry, backoff_ms: Number(event.target.value) } };
                        update({ ...config, routes });
                      }} />
                    </div>
                  </div>
                  <div className="field"><label>熔断阈值 / 冷却秒</label>
                    <div className="row">
                      <Input type="number" value={route.circuit.failures} onChange={(event) => {
                        const routes = [...config.routes];
                        routes[index] = { ...route, circuit: { ...route.circuit, failures: Number(event.target.value) } };
                        update({ ...config, routes });
                      }} />
                      <Input type="number" value={route.circuit.cooldown_seconds} onChange={(event) => {
                        const routes = [...config.routes];
                        routes[index] = { ...route, circuit: { ...route.circuit, cooldown_seconds: Number(event.target.value) } };
                        update({ ...config, routes });
                      }} />
                    </div>
                  </div>
                </div>
                <div className="field" style={{ marginTop: 10 }}>
                  <label>候选（按顺序）</label>
                  <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
                    {route.candidates.map((id, position) => (
                      <span key={`${id}-${position}`} className="chip active" style={{ display: "inline-flex", gap: 6, alignItems: "center" }}>
                        {position + 1}. {id}
                        <button className="btn link" onClick={() => {
                          const candidates = route.candidates.filter((_, i) => i !== position);
                          const routes = [...config.routes];
                          routes[index] = { ...route, candidates };
                          update({ ...config, routes });
                        }}>×</button>
                      </span>
                    ))}
                    <Select value="" onChange={(event) => {
                      if (!event.target.value) return;
                      const routes = [...config.routes];
                      routes[index] = { ...route, candidates: [...route.candidates, event.target.value] };
                      update({ ...config, routes });
                    }}>
                      <option value="">添加候选…</option>
                      {config.models.filter((model) => !route.candidates.includes(model.id)).map((model) => (
                        <option key={model.id} value={model.id}>{model.id}</option>
                      ))}
                    </Select>
                  </div>
                </div>
                <div className="form-actions">
                  <Button onClick={() => testRoute(route.name)}>测试线路</Button>
                  <Button className="danger" onClick={() => update({ ...config, routes: config.routes.filter((_, i) => i !== index) })}>删除线路</Button>
                </div>
              </div>
            ))}
            <div>
              <Button onClick={() => update({ ...config, routes: [...config.routes, { name: `route-${config.routes.length + 1}`, candidates: [], retry: { max_attempts: 1, backoff_ms: 200 }, circuit: { failures: 3, cooldown_seconds: 30 } }] })}>新增线路</Button>
            </div>
          </div>
        ) : null}
      </Card>

      <Card title="候选健康">
        {data?.health && data.health.length > 0 ? (
          <div className="table-wrap">
            <table>
              <thead><tr><th>线路</th><th>候选</th><th>状态</th><th>连续失败</th><th>最近延迟</th><th>最近错误</th></tr></thead>
              <tbody>
                {data.health.map((item, index) => (
                  <tr key={`${item.route}-${item.candidate}-${index}`}>
                    <td>{item.route}</td>
                    <td className="mono">{item.candidate}</td>
                    <td><Badge kind={item.open ? "danger" : "ok"}>{item.open ? "熔断" : "正常"}</Badge></td>
                    <td>{item.failures}</td>
                    <td>{item.last_latency_ms ? `${item.last_latency_ms} ms` : "—"}</td>
                    <td className="muted">{item.last_error || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : <div className="muted">还没有请求记录</div>}
      </Card>
    </div>
  );
}
