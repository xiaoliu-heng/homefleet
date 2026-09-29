export async function api<T = unknown>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const r = await fetch("/api/v1" + path, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-HomeFleet-Request": "1" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || "请求失败，请检查连接");
  return data as T;
}
export function storage(v: number | null | undefined) {
  if (v == null) return "—";
  if (v === 0) return "0 B";
  const units = ["B", "KiB", "GiB", "TiB", "PiB"];
  const i = Math.min(4, Math.floor(Math.log(v) / Math.log(1024)));
  const real = ["B", "KiB", "MiB", "GiB", "TiB"];
  return (v / 1024 ** i).toFixed(i >= 2 ? 1 : 0) + " " + (real[i] || units[i]);
}
export const date = (s?: string) =>
  !s || s.startsWith("0001")
    ? "尚无记录"
    : new Date(s).toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      });
export const percent = (v: number | null | undefined) =>
  v == null ? "—" : v.toFixed(1) + "%";
export const stateLabel: Record<string, string> = {
  queued: "等待设备",
  running: "执行中",
  succeeded: "已完成",
  failed: "失败",
  blocked: "未通过检查",
  unknown: "待核实",
  cancelled: "已取消",
  partial: "部分完成",
};
export const operationLabel: Record<string, string> = {
  self_update: "更新 Agent",
  install: "安装软件",
  upgrade: "更新软件",
  upgrade_all: "更新全部软件",
  deploy: "部署项目",
  refresh: "刷新软件清单",
  start: "启动应用",
  stop: "停止应用",
  update: "更新应用",
  status: "查看状态",
  logs: "读取日志",
};
