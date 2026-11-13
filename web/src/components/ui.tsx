import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { Link } from "react-router-dom";

/* ---------- toast ---------- */

type Toast = { id: number; message: string; error?: boolean };
const ToastContext = createContext<{ show: (message: string, error?: boolean) => void }>({ show: () => {} });

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const seq = useRef(0);

  const show = useCallback((message: string, error = false) => {
    const id = ++seq.current;
    setToasts((items) => [...items, { id, message, error }]);
    window.setTimeout(() => setToasts((items) => items.filter((item) => item.id !== id)), 3600);
  }, []);

  return (
    <ToastContext.Provider value={{ show }}>
      {children}
      <div className="toast-wrap">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast ${toast.error ? "error" : ""}`}>
            <span className="dot" />
            <span>{toast.message}</span>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export const useToast = () => useContext(ToastContext);

/* ---------- confirm ---------- */

type ConfirmOptions = { title: string; description?: string; confirmText?: string; danger?: boolean };
const ConfirmContext = createContext<(options: ConfirmOptions) => Promise<boolean>>(async () => false);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<(ConfirmOptions & { resolve: (value: boolean) => void }) | null>(null);

  const confirm = useCallback((options: ConfirmOptions) => {
    return new Promise<boolean>((resolve) => setState({ ...options, resolve }));
  }, []);

  const finish = (value: boolean) => {
    state?.resolve(value);
    setState(null);
  };

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      {state ? (
        <div className="modal-mask" onClick={() => finish(false)}>
          <div className="modal" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true">
            <h3>{state.title}</h3>
            {state.description ? <p>{state.description}</p> : null}
            <div className="row" style={{ justifyContent: "flex-end" }}>
              <Button onClick={() => finish(false)}>取消</Button>
              <Button className={state.danger ? "primary danger" : "primary"} onClick={() => finish(true)}>
                {state.confirmText ?? "确认"}
              </Button>
            </div>
          </div>
        </div>
      ) : null}
    </ConfirmContext.Provider>
  );
}

export const useConfirm = () => useContext(ConfirmContext);

/* ---------- primitives ---------- */

export function PageHeader({ title, description, actions }: { title: string; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-header">
      <div>
        <h1 className="page-title">{title}</h1>
        {description ? <p className="page-desc">{description}</p> : null}
      </div>
      {actions ? <div className="toolbar-right">{actions}</div> : null}
    </div>
  );
}

export function Breadcrumbs({ items }: { items: { label: string; to?: string }[] }) {
  return (
    <div className="breadcrumbs">
      {items.map((item, index) => (
        <span key={`${item.label}-${index}`}>
          {index > 0 ? <span> / </span> : null}
          {item.to ? <Link to={item.to}>{item.label}</Link> : <span>{item.label}</span>}
        </span>
      ))}
    </div>
  );
}

export function Card({ title, actions, children }: { title?: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      {title || actions ? (
        <div className="card-head">
          <div className="card-title">{title}</div>
          {actions}
        </div>
      ) : null}
      {children}
    </section>
  );
}

export function Button({ className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: string }) {
  return <button {...props} className={`btn ${className}`} />;
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {hint ? <div className="muted small">{hint}</div> : null}
    </div>
  );
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`input ${props.className ?? ""}`} />;
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={`select ${props.className ?? ""}`} />;
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={`textarea ${props.className ?? ""}`} />;
}

export function Badge({ children, kind = "" }: { children: ReactNode; kind?: string }) {
  return <span className={`badge ${kind}`}>{children}</span>;
}

export function Tabs<T extends string>({
  value,
  onChange,
  items,
}: {
  value: T;
  onChange: (value: T) => void;
  items: { value: T; label: string; count?: number }[];
}) {
  return (
    <div className="tabs" role="tablist">
      {items.map((item) => (
        <button
          key={item.value}
          role="tab"
          aria-selected={value === item.value}
          className={`tab ${value === item.value ? "active" : ""}`}
          onClick={() => onChange(item.value)}
        >
          {item.label}
          {item.count !== undefined ? <span className="muted"> · {item.count}</span> : null}
        </button>
      ))}
    </div>
  );
}

export function Empty({ icon = "🗂", text, action }: { icon?: string; text: string; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-icon">{icon}</div>
      <div>{text}</div>
      {action ? <div style={{ marginTop: 14 }}>{action}</div> : null}
    </div>
  );
}

export function Skeleton({ height = 16, width = "100%" }: { height?: number; width?: string | number }) {
  return <div className="skeleton" style={{ height, width }} />;
}

export function Pagination({
  page,
  size,
  total,
  onChange,
}: {
  page: number;
  size: number;
  total: number;
  onChange: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / size));
  return (
    <div className="pagination">
      <Button disabled={page <= 1} onClick={() => onChange(page - 1)}>上一页</Button>
      <span>第 {page} / {pages} 页 · 共 {total} 条</span>
      <Button disabled={page >= pages} onClick={() => onChange(page + 1)}>下一页</Button>
    </div>
  );
}

export function CopyButton({ value }: { value: string }) {
  const { show } = useToast();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  return (
    <button
      className="btn link"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
        } catch {
          show("复制失败，请手动选择", true);
        }
      }}
    >
      {copied ? "已复制" : "复制"}
    </button>
  );
}

export function useAsync<T>(loader: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    loader()
      .then((value) => {
        if (alive) {
          setData(value);
          setError(null);
        }
      })
      .catch((err: Error) => {
        if (alive) setError(err.message);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  return useMemo(
    () => ({ data, error, loading, reload: () => setTick((value) => value + 1) }),
    [data, error, loading],
  );
}
