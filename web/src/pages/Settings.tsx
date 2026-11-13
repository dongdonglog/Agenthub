import { useState } from "react";
import { api } from "../api/client";
import { Button, Card, Input, PageHeader, Skeleton, useAsync, useToast } from "../components/ui";

type ModelSettings = {
  base_url: string;
  model: string;
  fallback_model: string;
  vision_model: string;
  has_key: boolean;
  key_hint: string;
};

type SettingsData = { model: unknown; feishu: unknown; storage: unknown };

export function Settings() {
  const model = useAsync<ModelSettings>(() => api("/model"), []);
  const settings = useAsync<SettingsData>(() => api("/settings"), []);
  const [form, setForm] = useState<ModelSettings | null>(null);
  const [apiKey, setApiKey] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const { show } = useToast();

  if (model.data && !form) setForm({ ...model.data });

  const save = async () => {
    if (!form) return;
    try {
      await api("/model", {
        method: "PUT",
        body: JSON.stringify({
          base_url: form.base_url,
          model: form.model,
          fallback_model: form.fallback_model,
          vision_model: form.vision_model,
          api_key: apiKey,
          clear_key: clearKey,
        }),
      });
      show("已保存，下一次运行生效");
      setApiKey("");
      setClearKey(false);
      model.reload();
    } catch (err) {
      show((err as Error).message, true);
    }
  };

  return (
    <div className="stack">
      <PageHeader title="设置" description="模型配置可在下方修改；其余配置仍由 .env 控制。" />
      <Card title="模型">
        {!form ? <Skeleton height={220} /> : (
          <div className="form-grid">
            <div className="field"><label>Base URL</label><Input value={form.base_url} onChange={(event) => setForm({ ...form, base_url: event.target.value })} /></div>
            <div className="field"><label>模型</label><Input value={form.model} onChange={(event) => setForm({ ...form, model: event.target.value })} /></div>
            <div className="field"><label>备用模型</label><Input value={form.fallback_model} onChange={(event) => setForm({ ...form, fallback_model: event.target.value })} /></div>
            <div className="field"><label>视觉模型</label><Input value={form.vision_model} onChange={(event) => setForm({ ...form, vision_model: event.target.value })} /></div>
            <div className="field">
              <label>API Key</label>
              <Input type="password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder={form.has_key ? `已配置（${form.key_hint}），留空保持不变` : "未配置"} />
            </div>
            <label className="row"><input type="checkbox" checked={clearKey} onChange={(event) => setClearKey(event.target.checked)} /> 清除已保存的 Key</label>
            <div className="form-actions"><Button className="primary" onClick={save}>保存模型配置</Button></div>
          </div>
        )}
      </Card>
      <Card title="飞书">
        <pre className="code-block">{JSON.stringify(settings.data?.feishu ?? {}, null, 2)}</pre>
      </Card>
      <Card title="存储">
        <pre className="code-block">{JSON.stringify(settings.data?.storage ?? {}, null, 2)}</pre>
      </Card>
    </div>
  );
}
