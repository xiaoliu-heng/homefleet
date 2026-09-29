import React, {
  useState,
  useEffect,
  useCallback,
  useMemo,
  useRef,
} from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  ArrowDownToLine,
  ArrowLeft,
  ArrowRight,
  ArrowUpRight,
  Box,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleHelp,
  Clock3,
  Code2,
  Copy,
  Cpu,
  Download,
  ExternalLink,
  FolderGit2,
  HardDrive,
  LayoutDashboard,
  Loader2,
  LogOut,
  Monitor,
  MoreHorizontal,
  Network,
  Package as PackageIcon,
  Plus,
  Radio,
  RefreshCw,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Terminal,
  TriangleAlert,
  Wifi,
  X,
  XCircle,
} from "lucide-react";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
} from "recharts";
import { api, date, storage, percent, stateLabel, operationLabel } from "./api";
import { installationCommand, installationOrigin } from "./installation";
import type {
  Action,
  AgentRelease,
  Catalog,
  Device,
  Inventory,
  Job,
  Log,
  Project,
  Sample,
  Target,
} from "./types";
import "./style.css";

const nav = [
  { id: "overview", name: "设备总览", icon: LayoutDashboard },
  { id: "software", name: "软件管理", icon: PackageIcon },
  { id: "projects", name: "项目与应用", icon: FolderGit2 },
  { id: "agents", name: "Agent 更新", icon: Download },
  { id: "jobs", name: "任务中心", icon: Activity },
];
function Logo() {
  return (
    <div className="brand">
      <span className="brand-symbol">
        <Network size={22} />
      </span>
      <span>
        HomeFleet<small>家庭设备控制台</small>
      </span>
    </div>
  );
}
function Badge({ state }: { state: string }) {
  return (
    <span className={"badge " + state}>
      <i />
      {stateLabel[state] || state}
    </span>
  );
}
function Empty({
  icon: Icon = Monitor,
  title,
  text,
  children,
}: {
  icon?: typeof Monitor;
  title: string;
  text: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="empty">
      <span className="empty-icon">
        <Icon size={30} />
      </span>
      <h3>{title}</h3>
      <p>{text}</p>
      {children}
    </div>
  );
}
function Modal({
  title,
  subtitle,
  children,
  onClose,
  wide = false,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const old = document.activeElement as HTMLElement;
    ref.current?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeRef.current();
      if (e.key === "Tab") {
        const els = ref.current?.querySelectorAll<HTMLElement>(
          "button:not(:disabled), input, select, textarea, a[href]",
        );
        if (!els?.length) return;
        const first = els[0],
          last = els[els.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("keydown", key);
      old?.focus();
    };
  }, []);
  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={"modal " + (wide ? "wide" : "")}
      >
        <div className="modal-head">
          <div>
            <h2>{title}</h2>
            {subtitle && <p>{subtitle}</p>}
          </div>
          <button className="icon-button" aria-label="关闭" onClick={onClose}>
            <X size={20} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  );
}
function Bar({ value }: { value: number | null | undefined }) {
  return (
    <div className={"bar " + ((value || 0) > 85 ? "high" : "")}>
      <span style={{ width: Math.min(100, Math.max(0, value || 0)) + "%" }} />
    </div>
  );
}
function primaryAddress(device: Device) {
  const addresses = device.addresses;
  return (
    (
      addresses.find(
        (a) => /^\d+\./.test(a.address) && !a.address.startsWith("169.254."),
      ) ||
      addresses.find((a) => !a.address.startsWith("fe80:")) ||
      addresses[0]
    )?.address.replace(/\/\d+$/, "") || "—"
  );
}
function OSIcon({ os }: { os: string }) {
  return (
    <span className={"os-icon " + os}>
      {os === "network" ? (
        <Wifi size={22} />
      ) : os === "linux" ? (
        <Terminal size={21} />
      ) : (
        <Monitor size={22} />
      )}
    </span>
  );
}
function App() {
  const [auth, setAuth] = useState<boolean | null>(null),
    [password, setPassword] = useState(""),
    [tab, setTab] = useState("overview"),
    [devices, setDevices] = useState<Device[]>([]),
    [jobs, setJobs] = useState<Job[]>([]),
    [projects, setProjects] = useState<Project[]>([]),
    [catalog, setCatalog] = useState<Catalog[]>([]);
  const [error, setError] = useState(""),
    [hubVersion, setHubVersion] = useState(""),
    [agentRelease, setAgentRelease] = useState<{release: AgentRelease | null; reason?: string}>({release:null}),
    [busy, setBusy] = useState(false),
    [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all"),
    [selected, setSelected] = useState<string[]>([]),
    [add, setAdd] = useState(false),
    [detail, setDetail] = useState<Device | null>(null),
    [editProject, setEditProject] = useState<Project | null>(null),
    [activeJob, setActiveJob] = useState<string | null>(null),
    [stream, setStream] = useState(false);
  const report = useCallback(
    (e: unknown) => setError(e instanceof Error ? e.message : String(e)),
    [],
  );
  const refresh = useCallback(async () => {
    try {
      const [ds, js, ps, release] = await Promise.all([
        api<Device[]>("/devices"),
        api<Job[]>("/jobs"),
        api<Project[]>("/projects"),
        api<{release: AgentRelease | null; reason?: string}>("/agent-release"),
      ]);
      setDevices(ds);
      setJobs(js);
      setProjects(ps);
      setAgentRelease(release);
    } catch (e) {
      report(e);
    }
  }, [report]);
  useEffect(() => {
    api<{version:string}>("/me")
      .then((me) => { setHubVersion(me.version); setAuth(true); })
      .catch(() => setAuth(false));
  }, []);
  useEffect(() => {
    if (!auth) return;
    api<{version:string}>("/me").then((me)=>setHubVersion(me.version)).catch(report);
    refresh();
    api<Catalog[]>("/catalog").then(setCatalog).catch(report);
    const es = new EventSource("/api/v1/events");
    let timeout: ReturnType<typeof setTimeout> | undefined;
    es.onopen = () => setStream(true);
    es.onerror = () => setStream(false);
    es.onmessage = () => {
      if (!timeout)
        timeout = setTimeout(() => {
          refresh();
          timeout = undefined;
        }, 300);
    };
    const interval = setInterval(refresh, 10000);
    return () => {
      es.close();
      clearTimeout(timeout);
      clearInterval(interval);
    };
  }, [auth, refresh, report]);
  const run = async (fn: () => Promise<unknown>) => {
    setError("");
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      report(e);
    } finally {
      setBusy(false);
    }
  };
  const preview = async (action: Action, ids = selected) => {
    if (!ids.length) {
      setError("请先选择要操作的主机");
      return;
    }
    await run(async () => {
      const j = await api<Job>("/jobs/preview", "POST", {
        device_ids: ids,
        action,
      });
      setActiveJob(j.id);
      await refresh();
    });
  };
  const toggle = (id: string) =>
    setSelected((s) =>
      s.includes(id) ? s.filter((x) => x !== id) : [...s, id],
    );
  const visible = devices.filter(
    (d) =>
      (filter === "all" ||
        (filter === "online" && d.online) ||
        (filter === "offline" && !d.online) ||
        (filter === "appliance" && d.kind === "appliance")) &&
      [d.name, d.platform, d.group, ...d.addresses.map((a) => a.address)]
        .join(" ")
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  const job = jobs.find((j) => j.id === activeJob);
  if (auth === null)
    return (
      <div className="loading-screen">
        <Logo />
        <Loader2 className="spin" size={26} />
      </div>
    );
  if (!auth)
    return (
      <div className="login-page">
        <div className="login-story">
          <Logo />
          <div>
            <span className="eyebrow">ONE HOME. ONE CONTROL CENTER.</span>
            <h1>
              每台设备，
              <br />
              都在掌握之中。
            </h1>
            <p>
              从资源状态到软件更新，
              <br />
              让家里的设备，一起井然有序。
            </p>
            <div className="orbit">
              <span>
                <Server size={32} />
              </span>
              <span>
                <Monitor size={25} />
              </span>
              <span>
                <Wifi size={25} />
              </span>
              <span>
                <Terminal size={24} />
              </span>
              <div className="orbit-core">
                <Network size={52} />
              </div>
            </div>
          </div>
          <small>LOCAL FIRST · BUILT FOR YOUR HOME</small>
        </div>
        <form
          className="login-form"
          onSubmit={(e) => {
            e.preventDefault();
            run(async () => {
              await api("/login", "POST", { password });
              setPassword("");
              setAuth(true);
            });
          }}
        >
          <span className="eyebrow">欢迎回来</span>
          <h2>登录你的控制台</h2>
          <p>使用初始化时设置的管理员密码。</p>
          <label>
            管理员密码
            <input
              autoFocus
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              placeholder="输入密码"
            />
          </label>
          {error && (
            <div role="alert" className="error-inline">
              {error}
            </div>
          )}
          <button className="button primary" disabled={busy}>
            {busy ? <Loader2 size={17} className="spin" /> : null}进入控制台
            <ArrowRight size={18} />
          </button>
          <div className="login-note">
            <ShieldCheck size={18} /> 自托管 · 数据保存在你自己的设备上
          </div>
        </form>
      </div>
    );
  return (
    <div className="shell">
      <aside className="sidebar">
        <Logo />
        <span className="nav-caption">工作空间</span>
        <nav>
          {nav.map((n) => (
            <button
              key={n.id}
              aria-label={n.name}
              className={tab === n.id ? "active" : ""}
              onClick={() => setTab(n.id)}
            >
              <n.icon size={19} />
              <span>{n.name}</span>
              {n.id === "jobs" && jobs.some((j) => j.status === "running") && (
                <span className="nav-count">
                  {jobs.filter((j) => j.status === "running").length}
                </span>
              )}
            </button>
          ))}
        </nav>
        <div className="sidebar-tip">
          <div>
            <Radio size={17} />
            <b>本地掌控，安心管理</b>
          </div>
          <p>
            先检查，再执行。
            <br />
            每台设备的结果都有记录。
          </p>
        </div>
        <div className="sidebar-footer">
          <span className={"connection-dot " + (stream ? "" : "off")} />
          <span>
            {stream ? "控制台已连接" : "正在重新连接"}
            <small>HomeFleet{hubVersion ? " · v" + hubVersion : ""}</small>
          </span>
          <button
            className="icon-button"
            aria-label="退出登录"
            onClick={() =>
              run(async () => {
                await api("/logout", "POST", {});
                setAuth(false);
              })
            }
          >
            <LogOut size={17} />
          </button>
        </div>
      </aside>
      <main>
        <header className="topbar">
          <div>
            我的空间 <ChevronRight size={14} />
            <strong>{nav.find((n) => n.id === tab)?.name}</strong>
          </div>
          <div>
            <span className="private-label">
              <ShieldCheck size={14} /> 私有控制台
            </span>
            <span className="avatar">家</span>
          </div>
        </header>
        <div className="content">
          {error && (
            <div className="error-banner" role="alert">
              <TriangleAlert size={18} />
              <span>{error}</span>
              <button
                className="icon-button"
                aria-label="关闭错误提示"
                onClick={() => setError("")}
              >
                <X size={17} />
              </button>
            </div>
          )}
          <div className="page-heading">
            <div>
              <div className="eyebrow">YOUR HOME, CONNECTED</div>
              <h1>{nav.find((n) => n.id === tab)?.name}</h1>
              <p>
                {tab === "overview"
                  ? "设备状态一目了然，管理操作触手可及。"
                  : tab === "software"
                    ? "选择主机和软件，检查通过后统一安装或更新。"
                    : tab === "projects"
                      ? "把代码、容器与部署流程，放在同一个地方。"
                      : tab === "agents"
                        ? "查看各设备版本，预览后手动更新 Agent。"
                      : "每次操作，从预检查到执行结果，都清晰可查。"}
              </p>
            </div>
            <div className="heading-actions">
              <button
                className="button"
                onClick={() => refresh()}
                aria-label="刷新"
              >
                <RefreshCw size={16} />
              </button>
              {tab === "overview" && (
                <button className="button primary" onClick={() => setAdd(true)}>
                  <Plus size={17} />
                  接入设备
                </button>
              )}
              {tab === "projects" && (
                <button
                  className="button primary"
                  onClick={() => setEditProject(blankProject())}
                >
                  <Plus size={17} />
                  登记项目
                </button>
              )}
            </div>
          </div>
          {tab === "overview" && (
            <>
              <div className="stats">
                <Stat
                  label="已接入设备"
                  value={String(devices.length)}
                  detail="全部主机与网络设备"
                  icon={Monitor}
                />
                <Stat
                  label="当前在线"
                  value={String(devices.filter((d) => d.online).length)}
                  detail={devices.filter((d) => !d.online).length + " 台离线"}
                  icon={Radio}
                  green
                />
                <Stat
                  label="资源需关注"
                  value={String(
                    devices.filter(
                      (d) =>
                        d.online &&
                        ((d.latest?.cpu || 0) > 85 ||
                          (d.latest?.memory_percent || 0) > 85 ||
                          d.latest?.disks.some((x) => x.used_percent > 90)),
                    ).length,
                  )}
                  detail="CPU / 内存 >85% · 磁盘 >90%"
                  icon={Activity}
                />
                <Stat
                  label="执行中的任务"
                  value={String(
                    jobs.filter((j) => j.status === "running").length,
                  )}
                  detail="最多并行操作 3 台设备"
                  icon={Terminal}
                />
              </div>
              <div className="section-panel">
                <div className="table-toolbar">
                  <div className="tabs">
                    {[
                      ["all", "全部设备"],
                      ["online", "在线"],
                      ["offline", "离线"],
                      ["appliance", "网络设备"],
                    ].map(([v, l]) => (
                      <button
                        className={filter === v ? "active" : ""}
                        key={v}
                        onClick={() => setFilter(v)}
                      >
                        {l}
                        {v === "all" && <span>{devices.length}</span>}
                      </button>
                    ))}
                  </div>
                  <div className="search">
                    <Search size={16} />
                    <input
                      aria-label="搜索设备"
                      placeholder="搜索名称、IP 或分组"
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                    />
                  </div>
                </div>
                {devices.length === 0 ? (
                  <Empty
                    title="给你的设备，一个共同的家"
                    text="接入第一台主机，自动获取 IP、资源占用和软件管理能力。"
                  >
                    <button
                      className="button primary"
                      onClick={() => setAdd(true)}
                    >
                      <Plus size={17} />
                      接入第一台设备
                    </button>
                    <div className="onboarding-steps">
                      <span>
                        <b>01</b> 生成接入令牌
                      </span>
                      <ArrowRight size={14} />
                      <span>
                        <b>02</b> 安装 Agent
                      </span>
                      <ArrowRight size={14} />
                      <span>
                        <b>03</b> 开始统一管理
                      </span>
                    </div>
                  </Empty>
                ) : (
                  <div className="table-scroll">
                    <table className="device-table">
                      <thead>
                        <tr>
                          <th>
                            <input
                              aria-label="选择当前列表全部主机"
                              type="checkbox"
                              checked={
                                visible.filter((d) => d.kind === "agent")
                                  .length > 0 &&
                                visible
                                  .filter((d) => d.kind === "agent")
                                  .every((d) => selected.includes(d.id))
                              }
                              onChange={(e) =>
                                setSelected(
                                  e.target.checked
                                    ? [
                                        ...new Set([
                                          ...selected,
                                          ...visible
                                            .filter((d) => d.kind === "agent")
                                            .map((d) => d.id),
                                        ]),
                                      ]
                                    : selected.filter(
                                        (id) =>
                                          !visible.some((d) => d.id === id),
                                      ),
                                )
                              }
                            />
                          </th>
                          <th>设备</th>
                          <th>状态 / IP 地址</th>
                          <th>CPU</th>
                          <th>内存</th>
                          <th>GPU</th>
                          <th>磁盘可用</th>
                          <th />
                        </tr>
                      </thead>
                      <tbody>
                        {visible.map((d) => {
                          const g = d.latest?.gpus.find(
                              (g) => g.utilization != null,
                            ),
                            disks = d.latest?.disks;
                          return (
                            <tr
                              key={d.id}
                              className={
                                selected.includes(d.id) ? "selected" : ""
                              }
                            >
                              <td>
                                <input
                                  aria-label={"选择 " + d.name}
                                  type="checkbox"
                                  disabled={d.kind !== "agent"}
                                  checked={selected.includes(d.id)}
                                  onChange={() => toggle(d.id)}
                                />
                              </td>
                              <td>
                                <button
                                  className="device-name"
                                  onClick={() => setDetail(d)}
                                >
                                  <OSIcon os={d.os} />
                                  <span>
                                    <b>{d.name}</b>
                                    <small>
                                      {d.platform || d.os}
                                      {d.group ? " · " + d.group : ""}
                                      {d.kind === "agent" && d.agent_version ? " · Agent " + d.agent_version : ""}
                                    </small>
                                  </span>
                                </button>
                              </td>
                              <td>
                                <div
                                  className={
                                    "online-label " +
                                    (d.online ? "" : "offline")
                                  }
                                >
                                  <i />
                                  {d.revoked
                                    ? "已撤销"
                                    : d.online
                                      ? "在线"
                                      : "离线"}
                                </div>
                                <code className="ip">
                                  {primaryAddress(d)}
                                </code>
                              </td>
                              <td>
                                <div className={d.online ? "" : "stale"}>
                                  {percent(d.latest?.cpu)}
                                  <Bar value={d.latest?.cpu} />
                                </div>
                              </td>
                              <td>
                                <div className={d.online ? "" : "stale"}>
                                  {percent(d.latest?.memory_percent)}
                                  <Bar value={d.latest?.memory_percent} />
                                </div>
                              </td>
                              <td>
                                <div className={d.online ? "" : "stale"}>
                                  {percent(g?.utilization)}
                                  <small className="muted">
                                    {g?.vendor ||
                                      (d.kind === "appliance"
                                        ? "不适用"
                                        : "查看采集状态")}
                                  </small>
                                </div>
                              </td>
                              <td>
                                {disks?.length ? (
                                  <>
                                    <b className="number">
                                      {storage(disks[0].free)}
                                    </b>
                                    <small className="muted">
                                      {disks.length > 1
                                        ? "共 " +
                                          disks.length +
                                          " 个卷 · 查看详情"
                                        : disks[0].mount}
                                    </small>
                                  </>
                                ) : (
                                  "—"
                                )}
                              </td>
                              <td>
                                <button
                                  className="icon-button"
                                  aria-label={"查看 " + d.name}
                                  onClick={() => setDetail(d)}
                                >
                                  <ArrowUpRight size={17} />
                                </button>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                    {visible.length === 0 && (
                      <Empty
                        title="没有匹配的设备"
                        text="试试其他名称、地址或筛选条件。"
                      />
                    )}
                  </div>
                )}
                <div className="table-footer">
                  <span>
                    <span className="connection-dot" /> 每 10 秒采集 · 超过 45
                    秒未上报标记离线
                  </span>
                  <span>首批计划：8 台主机 / 3 台爱快设备</span>
                </div>
              </div>
              <div className="bottom-grid">
                <div className="section-panel info-panel">
                  <div className="panel-title">
                    <h3>
                      <Clock3 size={17} />
                      最近操作
                    </h3>
                    <button
                      className="text-button"
                      onClick={() => setTab("jobs")}
                    >
                      全部任务
                      <ArrowRight size={14} />
                    </button>
                  </div>
                  {jobs.slice(0, 3).map((j) => (
                    <button
                      className="recent-job"
                      key={j.id}
                      onClick={() => setActiveJob(j.id)}
                    >
                      <span className="soft-icon">
                        <Terminal size={17} />
                      </span>
                      <span>
                        <b>{jobTitle(j)}</b>
                        <small>
                          {j.targets.length} 台设备 · {date(j.created_at)}
                        </small>
                      </span>
                      <Badge state={j.status} />
                    </button>
                  ))}
                  {!jobs.length && (
                    <p className="quiet">
                      还没有操作记录。接入设备后，可以从一次只读清单刷新开始。
                    </p>
                  )}
                </div>
                <div className="section-panel info-panel">
                  <div className="panel-title">
                    <h3>
                      <ShieldCheck size={17} />
                      按设备能力管理
                    </h3>
                  </div>
                  <p className="quiet">
                    主机安装 Agent 后，自动报告可用能力。路由器和 AP
                    通过外部探测接入，并保留原管理页面入口。
                  </p>
                  <div className="platform-chips">
                    <span>Windows</span>
                    <span>Linux</span>
                    <span>macOS</span>
                    <span>iKuai</span>
                  </div>
                </div>
              </div>
            </>
          )}
          {tab === "agents" && (
            <AgentUpdates devices={devices} selected={selected} toggle={toggle} select={setSelected} release={agentRelease.release} reason={agentRelease.reason} busy={busy} preview={preview} install={()=>setAdd(true)} />
          )}
          {tab === "software" && (
            <Software
              devices={devices}
              selected={selected}
              toggle={toggle}
              catalog={catalog}
              preview={preview}
              busy={busy}
              report={report}
              jobs={jobs}
            />
          )}
          {tab === "projects" && (
            <Projects
              projects={projects}
              devices={devices}
              selected={selected}
              toggle={toggle}
              preview={preview}
              edit={setEditProject}
              busy={busy}
            />
          )}
          {tab === "jobs" && (
            <div className="section-panel">
              {!jobs.length ? (
                <Empty
                  icon={Activity}
                  title="每次操作，都有迹可循"
                  text="提交一次预检查后，这里会显示设备检查结果、执行范围和完整日志。"
                />
              ) : (
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>任务</th>
                        <th>阶段</th>
                        <th>目标设备</th>
                        <th>状态</th>
                        <th>创建时间</th>
                        <th />
                      </tr>
                    </thead>
                    <tbody>
                      {jobs.map((j) => (
                        <tr key={j.id}>
                          <td>
                            <button
                              className="text-button strong"
                              onClick={() => setActiveJob(j.id)}
                            >
                              {jobTitle(j)}
                            </button>
                            <small className="muted mono">
                              {j.id.slice(0, 10)}
                            </small>
                          </td>
                          <td>
                            {j.mode === "preview"
                              ? "预检查"
                              : j.mode === "inspect"
                                ? "只读查询"
                                : "执行"}
                          </td>
                          <td>{j.targets.length} 台</td>
                          <td>
                            <Badge state={j.status} />
                          </td>
                          <td className="muted">{date(j.created_at)}</td>
                          <td>
                            <button
                              className="icon-button"
                              aria-label={"查看任务 " + j.id}
                              onClick={() => setActiveJob(j.id)}
                            >
                              <ChevronRight size={17} />
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          )}
        </div>
        <footer className="page-footer">
          <span>HOMEFLEET</span> 你的设备，你的控制中心。
          <span>数据保留 7 天 · 本地存储</span>
        </footer>
      </main>
      {selected.length > 0 && tab === "overview" && (
        <div className="selection-bar">
          <span>
            <CheckCircle2 size={18} />
            已选择 <b>{selected.length}</b> 台主机
          </span>
          <button className="text-button" onClick={() => setSelected([])}>
            取消选择
          </button>
          <button
            className="button"
            onClick={() => preview({ kind: "inventory", operation: "refresh" })}
          >
            <RefreshCw size={15} />
            刷新软件清单
          </button>
          <button className="button primary" onClick={() => setTab("software")}>
            <PackageIcon size={16} />
            管理软件
            <ArrowRight size={15} />
          </button>
        </div>
      )}
      {add && (
        <AddDevice
          onClose={() => setAdd(false)}
          onDone={refresh}
          report={report}
        />
      )}
      {detail && (
        <DeviceDetail
          device={devices.find((d) => d.id === detail.id) || detail}
          onClose={() => setDetail(null)}
          refresh={refresh}
          report={report}
        />
      )}
      {editProject && (
        <ProjectEditor
          project={editProject}
          onClose={() => setEditProject(null)}
          onSave={async (p) => {
            await api("/projects", "POST", p);
            await refresh();
            setEditProject(null);
          }}
        />
      )}
      {job && (
        <JobDetail
          job={job}
          onClose={() => setActiveJob(null)}
          refresh={refresh}
          onNew={setActiveJob}
          report={report}
        />
      )}
    </div>
  );
}
function Stat({
  label,
  value,
  detail,
  icon: Icon,
  green = false,
}: {
  label: string;
  value: string;
  detail: string;
  icon: typeof Monitor;
  green?: boolean;
}) {
  return (
    <div className={"stat " + (green ? "green" : "")}>
      <div>
        <span>{label}</span>
        <Icon size={18} />
      </div>
      <strong>
        {value}
        <small>台{label.includes("任务") ? " / 任务" : ""}</small>
      </strong>
      <p>{detail}</p>
    </div>
  );
}
function TargetPicker({
  devices,
  selected,
  toggle,
  capability,
}: {
  devices: Device[];
  selected: string[];
  toggle: (id: string) => void;
  capability?: string;
}) {
  return (
    <div className="section-panel target-picker">
      <div className="panel-title">
        <h3>目标主机</h3>
        <span className="count">{selected.length} 已选择</span>
      </div>
      {devices
        .filter((d) => d.kind === "agent")
        .map((d) => (
          <label
            key={d.id}
            className={selected.includes(d.id) ? "checked" : ""}
          >
            <input
              type="checkbox"
              checked={selected.includes(d.id)}
              onChange={() => toggle(d.id)}
            />
            <OSIcon os={d.os} />
            <span>
              <b>{d.name}</b>
              <small>
                {capability && !d.capabilities[capability]?.available
                  ? d.capabilities[capability]?.reason || "该操作暂不可用"
                  : d.platform || d.os}
              </small>
            </span>
            <span className={"connection-dot " + (d.online ? "" : "off")} />
          </label>
        ))}
      {!devices.some((d) => d.kind === "agent") && (
        <p className="quiet">请先在设备总览接入主机。</p>
      )}
    </div>
  );
}
function AgentUpdates({devices, selected, toggle, select, release, reason, busy, preview, install}: {
  devices: Device[]; selected: string[]; toggle: (id:string)=>void; select: (ids:string[])=>void;
  release: AgentRelease | null; reason?: string; busy: boolean;
  preview: (a:Action, ids?:string[])=>Promise<void>; install: ()=>void;
}) {
  const hosts = devices.filter(d=>d.kind === "agent");
  const ids = selected.filter(id=>hosts.some(d=>d.id===id));
  const newer = (d:Device) => {
    if (!release || !/^\d+\.\d+\.\d+$/.test(d.agent_version)) return false;
    const current=d.agent_version.split(".").map(Number), latest=release.version.split(".").map(Number);
    for(let i=0;i<3;i++){if(latest[i]!==current[i]) return latest[i]>current[i];}
    return false;
  };
  return <div className="section-panel agent-updates">
    <div className="panel-title"><div><h3>可发布版本 {release ? "v"+release.version : "—"}</h3><p className="quiet">{release ? "发布于 "+date(release.published_at)+" · 更新后保留设备身份和配置" : reason || "尚未加载发布信息"}</p></div><button className="button" onClick={install}><ArrowDownToLine size={16}/>安装 / 手动更新</button></div>
    {hosts.some(d=>!d.capabilities.agent_update) && <div className="callout warning"><TriangleAlert size={17}/><span>旧版 Agent 需要先用安装命令手动更新一次，之后即可在此更新。重复安装会保留已有设备身份。</span></div>}
    <div className="table-toolbar"><span>{ids.length} 台已选择 · 默认最多同时更新 3 台</span><button className="text-button" onClick={()=>select(hosts.filter(d=>d.online&&!d.revoked&&d.capabilities.agent_update?.available&&newer(d)).map(d=>d.id))}>选择可升级设备</button></div>
    <div className="table-scroll"><table className="agent-version-table"><thead><tr><th/><th>设备</th><th>当前版本</th><th>目标版本</th><th>后台更新</th><th>最后上报</th></tr></thead><tbody>{hosts.map(d=><tr key={d.id} className={ids.includes(d.id)?"selected":""}>
      <td><input type="checkbox" aria-label={"更新 "+d.name} checked={ids.includes(d.id)} onChange={()=>toggle(d.id)} disabled={d.revoked}/></td>
      <td><b>{d.name}</b><small className="version-device-meta">{d.platform||d.os} · {d.arch} · {d.revoked?"已撤销":d.online?"在线":"离线"}</small></td>
      <td><code>{d.agent_version||"未上报"}</code></td><td><code>{release?.version||"—"}</code>{release&&d.agent_version===release.version&&<small className="version-device-meta">已是当前版本</small>}</td>
      <td className="version-capability">{d.capabilities.agent_update?.available ? "支持" : d.capabilities.agent_update?.reason || "需先手动更新一次"}</td><td>{date(d.last_seen)}</td>
    </tr>)}</tbody></table></div>
    {!hosts.length&&<Empty title="尚无 Agent 设备" text="接入主机后，在这里查看和更新版本。"/>}
    <div className="agent-update-actions"><p className="quiet">先预检查，再确认执行。新版本成功回连后才标记完成。</p><button className="button primary" disabled={busy||!release||!ids.length} onClick={()=>release&&preview({kind:"agent",operation:"self_update",agent_version:release.version},ids)}><ShieldCheck size={16}/>预览 Agent 更新</button></div>
  </div>;
}
function Software({
  devices,
  selected,
  toggle,
  catalog,
  preview,
  busy,
  report,
  jobs,
}: {
  devices: Device[];
  selected: string[];
  toggle: (id: string) => void;
  catalog: Catalog[];
  preview: (a: Action, ids?: string[]) => Promise<void>;
  busy: boolean;
  report: (e: unknown) => void;
  jobs: Job[];
}) {
  const [pkg, setPkg] = useState(""),
    [catalogID, setCatalogID] = useState("git"),
    [operation, setOperation] = useState("install"),
    [scope, setScope] = useState("machine"),
    [device, setDevice] = useState(""),
    [inventory, setInventory] = useState<Inventory | null>(null),
    [query, setQuery] = useState("");
  const active = device || devices.find((d) => d.kind === "agent")?.id || "";
  useEffect(() => {
    if (active)
      api<Inventory>("/devices/" + active + "/inventory")
        .then(setInventory)
        .catch(report);
  }, [active, jobs, report]);
  return (
    <div className="workspace-grid">
      <TargetPicker {...{ devices, selected, toggle }} capability="packages" />
      <div className="stack">
        <div className="section-panel padded">
          <div className="panel-title">
            <h3>
              <PackageIcon size={18} />
              软件操作
            </h3>
            <span className="subtle-tag">先预检查，后执行</span>
          </div>
          <div className="catalog-grid">
            {catalog.map((c) => (
              <button
                key={c.id}
                className={
                  "catalog-card " + (catalogID === c.id ? "chosen" : "")
                }
                onClick={() => {
                  setCatalogID(c.id);
                  setPkg("");
                }}
              >
                <Code2 size={22} />
                <strong>{c.name}</strong>
                <small>{c.description}</small>
                {catalogID === c.id && (
                  <CheckCircle2 size={17} className="chosen-mark" />
                )}
              </button>
            ))}
          </div>
          <div className="form-grid">
            <label>
              或填写准确的包名称
              <input
                placeholder="例如：htop / Microsoft.VisualStudioCode"
                value={pkg}
                onChange={(e) => {
                  setPkg(e.target.value);
                  setCatalogID("");
                }}
              />
            </label>
            <label>
              操作
              <select
                value={operation}
                onChange={(e) => setOperation(e.target.value)}
              >
                <option value="install">安装软件</option>
                <option value="upgrade">更新指定软件</option>
                <option value="upgrade_all">更新全部软件</option>
              </select>
            </label>
            <label>
              Windows 安装范围
              <select value={scope} onChange={(e) => setScope(e.target.value)}>
                <option value="machine">系统级（SYSTEM）</option>
                <option value="user">当前用户（需要保持登录）</option>
              </select>
            </label>
          </div>
          <div className="callout">
            <CircleHelp size={18} />
            <span>
              {operation === "install"
                ? "Arch 安装使用已有软件源索引，不刷新索引、不自动全量升级；索引与已安装版本不同步时会停止。"
                : "Arch 的更新操作包含完整系统升级，预检查会说明影响范围。"}
            </span>
          </div>
          <div className="form-actions">
            <span className="muted">将检查 {selected.length} 台设备</span>
            <button
              className="button primary"
              disabled={
                busy ||
                !selected.some(
                  (id) =>
                    devices.find((d) => d.id === id)?.capabilities.packages
                      ?.available,
                )
              }
              onClick={() =>
                preview({
                  kind: "package",
                  operation,
                  scope,
                  package: pkg,
                  catalog_id: catalogID,
                })
              }
            >
              预检查操作
              <ArrowRight size={17} />
            </button>
          </div>
        </div>
        <div className="section-panel">
          <div className="inventory-toolbar">
            <h3>设备软件清单</h3>
            <select
              aria-label="软件清单设备"
              value={active}
              onChange={(e) => setDevice(e.target.value)}
            >
              {devices
                .filter((d) => d.kind === "agent")
                .map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
            </select>
            <button
              className="button"
              disabled={!active || busy}
              onClick={() =>
                preview({ kind: "inventory", operation: "refresh", scope }, [
                  active,
                ])
              }
            >
              <RefreshCw size={15} />
              刷新清单
            </button>
          </div>
          <div className="inventory-meta">
            <span>最后查询：{date(inventory?.at)}</span>
            <input
              aria-label="搜索软件"
              placeholder="搜索软件名称"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {inventory?.error && (
            <div className="callout warning">{inventory.error}</div>
          )}
          {inventory?.packages.length ? (
            <div className="table-scroll inventory-table">
              <table>
                <thead>
                  <tr>
                    <th>软件</th>
                    <th>已安装版本</th>
                    <th>可用更新</th>
                    <th>来源</th>
                  </tr>
                </thead>
                <tbody>
                  {inventory.packages
                    .filter((p) =>
                      (p.name + " " + p.id)
                        .toLowerCase()
                        .includes(query.toLowerCase()),
                    )
                    .map((p) => (
                      <tr key={p.id}>
                        <td>
                          <b>{p.name}</b>
                        </td>
                        <td className="mono">{p.version}</td>
                        <td>
                          {p.available_version ? (
                            <span className="update-version">
                              {p.available_version}
                            </span>
                          ) : (
                            "—"
                          )}
                        </td>
                        <td>
                          <span className="subtle-tag">{p.manager}</span>
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty
              icon={PackageIcon}
              title="还没有软件清单"
              text="选择一台主机并刷新，查询不会安装或更新软件。"
            />
          )}
        </div>
      </div>
    </div>
  );
}
function Projects({
  projects,
  devices,
  selected,
  toggle,
  preview,
  edit,
  busy,
}: {
  projects: Project[];
  devices: Device[];
  selected: string[];
  toggle: (id: string) => void;
  preview: (a: Action) => Promise<void>;
  edit: (p: Project) => void;
  busy: boolean;
}) {
  return (
    <div className="workspace-grid">
      <TargetPicker {...{ devices, selected, toggle }} capability="projects" />
      <div className="stack">
        {!projects.length ? (
          <div className="section-panel">
            <Empty
              icon={FolderGit2}
              title="让部署成为一个清晰的流程"
              text="登记 Git 仓库、各系统的运行目录和部署脚本，之后即可批量预检查与部署。"
            >
              <button
                className="button primary"
                onClick={() => edit(blankProject())}
              >
                <Plus size={17} />
                登记第一个项目
              </button>
            </Empty>
          </div>
        ) : (
          projects.map((p) => (
            <div className="section-panel project-card" key={p.id}>
              <div className="panel-title">
                <span className="project-symbol">
                  <FolderGit2 size={23} />
                </span>
                <div>
                  <h3>{p.name}</h3>
                  <p>
                    模板 v{p.version} · {p.ref}
                  </p>
                </div>
                <button
                  className="icon-button"
                  aria-label={"编辑 " + p.name}
                  onClick={() => edit(p)}
                >
                  <Settings2 size={19} />
                </button>
              </div>
              <code>{p.repository || "使用现有目录的 origin"}</code>
              <div className="platform-chips">
                {Object.entries(p.platforms)
                  .filter(([, c]) => c.steps.length)
                  .map(([os]) => (
                    <span key={os}>{os}</span>
                  ))}
                {p.compose_file && (
                  <span>
                    <Box size={13} />
                    Docker Compose
                  </span>
                )}
              </div>
              <div className="project-actions">
                <button
                  className="button primary"
                  disabled={
                    busy ||
                    !selected.some(
                      (id) =>
                        devices.find((d) => d.id === id)?.capabilities.projects
                          ?.available,
                    )
                  }
                  onClick={() =>
                    preview({
                      kind: "project",
                      operation: "deploy",
                      project_id: p.id,
                    })
                  }
                >
                  <ArrowUpRight size={16} />
                  预检查部署
                </button>
                {p.compose_file && (
                  <select
                    aria-label={p.name + " 容器操作"}
                    value=""
                    disabled={
                      busy ||
                      !selected.some(
                        (id) =>
                          devices.find((d) => d.id === id)?.capabilities.docker
                            ?.available &&
                          devices.find((d) => d.id === id)?.capabilities
                            .projects?.available,
                      )
                    }
                    onChange={(e) => {
                      if (e.target.value)
                        preview({
                          kind: "compose",
                          operation: e.target.value,
                          project_id: p.id,
                        });
                    }}
                  >
                    <option value="">容器操作…</option>
                    {[
                      ["status", "查看状态"],
                      ["logs", "读取日志"],
                      ["start", "启动应用"],
                      ["stop", "停止应用"],
                      ["update", "更新应用"],
                    ].map(([v, l]) => (
                      <option key={v} value={v}>
                        {l}
                      </option>
                    ))}
                  </select>
                )}
                <span className="muted">
                  {p.secret_keys?.length || 0} 个加密变量
                </span>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
const blankProject = (): Project => ({
  name: "",
  repository: "",
  ref: "main",
  directory: "",
  directories: { linux: "", darwin: "", windows: "" },
  platforms: {
    linux: { steps: [], health_check: "" },
    darwin: { steps: [], health_check: "" },
    windows: { steps: [], health_check: "" },
  },
  env: {},
  compose_file: "",
});
function ProjectEditor({
  project,
  onClose,
  onSave,
}: {
  project: Project;
  onClose: () => void;
  onSave: (p: Project) => Promise<void>;
}) {
  const [p, setP] = useState(project),
    [os, setOS] = useState("linux"),
    [env, setEnv] = useState(
      Object.entries(project.env || {})
        .map(([k, v]) => k + "=" + v)
        .join("\n"),
    ),
    [secrets, setSecrets] = useState(""),
    [error, setError] = useState(""),
    [saving, setSaving] = useState(false);
  const field = (k: keyof Project, v: unknown) =>
    setP((old) => ({ ...old, [k]: v }));
  const platform = p.platforms[os] || { steps: [], health_check: "" };
  const envMap = (text: string) =>
    Object.fromEntries(
      text
        .split("\n")
        .filter((l) => l.trim())
        .map((l) => {
          const i = l.indexOf("=");
          if (i < 1) throw new Error("环境变量格式为 KEY=value");
          return [l.slice(0, i).trim(), l.slice(i + 1)];
        }),
    );
  return (
    <Modal
      title={p.id ? "编辑项目" : "登记项目"}
      subtitle="部署脚本以你的普通用户账号执行，保存后通过预检查确认范围。"
      onClose={onClose}
      wide
    >
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setSaving(true);
          setError("");
          try {
            await onSave({
              ...p,
              env: envMap(env),
              secret_env: envMap(secrets),
            });
          } catch (e) {
            setError((e as Error).message);
          } finally {
            setSaving(false);
          }
        }}
      >
        <div className="form-grid">
          <label>
            项目名称
            <input
              required
              value={p.name}
              onChange={(e) => field("name", e.target.value)}
              placeholder="例如：家庭相册"
            />
          </label>
          <label>
            Git 引用
            <input
              value={p.ref}
              onChange={(e) => field("ref", e.target.value)}
              placeholder="main / tag / commit SHA"
            />
          </label>
        </div>
        <label>
          Git 仓库地址
          <input
            value={p.repository}
            onChange={(e) => field("repository", e.target.value)}
            placeholder="https://… 或 git@…（接管现有目录时可留空）"
          />
        </label>
        <div className="tabs os-tabs">
          {[
            ["linux", "Linux"],
            ["darwin", "macOS"],
            ["windows", "Windows"],
          ].map(([v, l]) => (
            <button
              type="button"
              key={v}
              className={os === v ? "active" : ""}
              onClick={() => setOS(v)}
            >
              {l}
            </button>
          ))}
        </div>
        <label>
          {os} 项目目录
          <input
            value={p.directories?.[os] || p.directory || ""}
            onChange={(e) =>
              field("directories", { ...p.directories, [os]: e.target.value })
            }
            placeholder={
              os === "windows"
                ? "C:\\Users\\you\\projects\\app"
                : "/home/you/projects/app"
            }
          />
        </label>
        <label>
          部署步骤{" "}
          <small>多步骤之间用独立一行 --- 分隔；该系统不部署时留空</small>
          <textarea
            className="code-input"
            rows={5}
            value={platform.steps.join("\n---\n")}
            onChange={(e) =>
              field("platforms", {
                ...p.platforms,
                [os]: {
                  ...platform,
                  steps: e.target.value ? e.target.value.split("\n---\n") : [],
                },
              })
            }
            placeholder={
              os === "windows"
                ? "npm ci\n---\nnpm run build"
                : "npm ci\n---\nnpm run build"
            }
          />
        </label>
        <label>
          健康检查 <small>检查命令退出码为 0 才视为健康</small>
          <input
            className="code-input"
            value={platform.health_check}
            onChange={(e) =>
              field("platforms", {
                ...p.platforms,
                [os]: { ...platform, health_check: e.target.value },
              })
            }
            placeholder={
              os === "windows"
                ? "Invoke-WebRequest http://localhost:3000/health"
                : "curl --fail http://localhost:3000/health"
            }
          />
        </label>
        <div className="form-grid">
          <label>
            普通环境变量
            <textarea
              rows={3}
              className="code-input"
              value={env}
              onChange={(e) => setEnv(e.target.value)}
              placeholder="NODE_ENV=production"
            />
          </label>
          <label>
            机密环境变量 <small>加密保存；KEY= 删除该变量</small>
            <textarea
              rows={3}
              autoComplete="off"
              className="code-input"
              value={secrets}
              onChange={(e) => setSecrets(e.target.value)}
              placeholder="TOKEN=…"
            />
            {p.secret_keys?.length ? (
              <small>已保存：{p.secret_keys.join("、")}（不填写则保留）</small>
            ) : null}
          </label>
        </div>
        <label>
          Compose 文件路径 <small>相对项目目录；没有容器应用可留空</small>
          <input
            value={p.compose_file}
            onChange={(e) => field("compose_file", e.target.value)}
            placeholder="compose.yaml"
          />
        </label>
        {error && (
          <div className="error-inline" role="alert">
            {error}
          </div>
        )}
        <div className="form-actions">
          <button type="button" className="button" onClick={onClose}>
            取消
          </button>
          <button className="button primary" disabled={saving}>
            {saving ? (
              <Loader2 className="spin" size={16} />
            ) : (
              <Check size={16} />
            )}
            保存项目
          </button>
        </div>
      </form>
    </Modal>
  );
}
function jobTitle(j: Job) {
  return (
    (operationLabel[j.action.operation] || j.action.operation) +
    (j.action.agent_version
      ? " · " + j.action.agent_version
      : j.action.project
      ? " · " + j.action.project.name
      : j.action.package
        ? " · " + j.action.package
        : j.action.catalog_id
          ? " · " + j.action.catalog_id
          : "")
  );
}
function JobDetail({
  job,
  onClose,
  refresh,
  onNew,
  report,
}: {
  job: Job;
  onClose: () => void;
  refresh: () => Promise<void>;
  onNew: (id: string) => void;
  report: (e: unknown) => void;
}) {
  const [targetID, setTargetID] = useState(""),
    [logs, setLogs] = useState<Log[]>([]),
    [busy, setBusy] = useState(false),
    [excluded, setExcluded] = useState<string[]>([]),
    [resolve, setResolve] = useState(false),
    [note, setNote] = useState(""),
    [outcome, setOutcome] = useState("failed");
  const target = job.targets.find((t) => t.id === targetID) || job.targets[0];
  const ready = job.targets.filter(
    (t) =>
      t.state === "succeeded" &&
      t.plan &&
      new Date(t.plan.expires_at) > new Date() &&
      !excluded.includes(t.device_id),
  );
  useEffect(() => {
    if (!target) return;
    let disposed = false;
    let after = 0;
    let loading = false;
    setLogs([]);
    const load = async () => {
      if (loading || disposed) return;
      loading = true;
      try {
        const rows = await api<Log[]>(
          "/targets/" + target.id + "/logs?after=" + after,
        );
        if (!disposed && rows.length) {
          after = rows[rows.length - 1].seq;
          setLogs((old) => [...old, ...rows]);
        }
      } catch (e) {
        if (!disposed) report(e);
      } finally {
        loading = false;
      }
    };
    load();
    const timer = setInterval(load, 1500);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, [target?.id, report]);
  const action = async (path: string, body: unknown = {}) => {
    setBusy(true);
    try {
      const result = await api<Job>(path, "POST", body);
      await refresh();
      if (result.id) onNew(result.id);
    } catch (e) {
      report(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title={jobTitle(job)}
      subtitle={
        (job.mode === "preview"
          ? "预检查任务"
          : job.mode === "inspect"
            ? "只读查询"
            : "执行任务") +
        " · " +
        date(job.created_at)
      }
      onClose={onClose}
      wide
    >
      <div className="job-summary">
        <Badge state={job.status} />
        <span>{job.targets.length} 台目标设备</span>
        <code>{job.id.slice(0, 12)}</code>
      </div>
      <div className="job-workspace">
        <div className="target-list">
          {job.targets.map((t) => (
            <div key={t.id} className={target?.id === t.id ? "active" : ""}>
              {job.mode === "preview" && t.state === "succeeded" && (
                <input
                  type="checkbox"
                  aria-label={"确认执行 " + t.device_name}
                  checked={!excluded.includes(t.device_id)}
                  onChange={(e) =>
                    setExcluded((s) =>
                      e.target.checked
                        ? s.filter((id) => id !== t.device_id)
                        : [...s, t.device_id],
                    )
                  }
                />
              )}
              <button onClick={() => setTargetID(t.id)}>
                <strong>{t.device_name}</strong>
                <Badge state={t.state} />
              </button>
            </div>
          ))}
        </div>
        <div className="target-content">
          {target?.reason && (
            <div
              className={
                "callout " + (target.state === "succeeded" ? "" : "warning")
              }
            >
              {target.reason}
            </div>
          )}
          {target?.plan && (
            <>
              <div className="plan-meta">
                <span>操作计划</span>
                <small>有效至 {date(target.plan.expires_at)}</small>
              </div>
              {target.plan.warnings.map((w, i) => (
                <div className="callout warning" key={i}>
                  <TriangleAlert size={16} />
                  <span>{w}</span>
                </div>
              ))}
              {target.plan.resolved_commit && (
                <p className="commit-line">
                  确认版本 <code>{target.plan.resolved_commit}</code>
                </p>
              )}
              {target.plan.agent_update && (
                <div className="agent-plan-meta">
                  <b>Agent {target.plan.agent_update.from_version} → {target.plan.agent_update.version}</b>
                  <small>{target.plan.agent_update.artifact.name} · {storage(target.plan.agent_update.artifact.size)}</small>
                  <code>SHA-256: {target.plan.agent_update.artifact.sha256}</code>
                </div>
              )}
              <ol className="plan-steps">
                {target.plan.steps.map((s, i) => (
                  <li key={i}>
                    <span>{i + 1}</span>
                    <div>
                      <b>{s.name}</b>
                      <small>
                        {s.identity === "user" ? "本人账号" : "系统权限"}
                        {s.health ? " · 健康检查" : ""}
                      </small>
                      <pre>
                        {s.script || [s.program, ...(s.args || [])].join(" ")}
                      </pre>
                      {s.directory && <small>目录：{s.directory}</small>}
                    </div>
                  </li>
                ))}
              </ol>
            </>
          )}
          {target?.result?.commit && (
            <p className="commit-line">
              设备当前 commit <code>{target.result.commit}</code>
            </p>
          )}
          {target?.result?.agent_version && <p className="commit-line">执行结果版本 <code>{target.result.agent_version}</code></p>}
          {target?.result?.health && (
            <p className="health-result">
              健康检查：
              {target.result.health === "healthy"
                ? "通过"
                : target.result.health === "failed"
                  ? "失败"
                  : "未配置，服务健康状态未验证"}
            </p>
          )}
          <div className="terminal-head">
            <Terminal size={15} /> 执行日志 <span>{logs.length} 条</span>
          </div>
          <pre className="log-view">
            {logs.length ? (
              logs.map((l) => (
                <div className={"log " + l.stream} key={l.seq}>
                  <span>
                    {new Date(l.at).toLocaleTimeString("zh-CN", {
                      hour12: false,
                    })}
                  </span>
                  {l.text}
                </div>
              ))
            ) : (
              <span className="log-placeholder">等待设备返回日志…</span>
            )}
          </pre>
          {target?.state === "unknown" && (
            <div className="resolve-box">
              <button
                className="text-button"
                onClick={() => setResolve(!resolve)}
              >
                已在设备上核实实际结果
              </button>
              {resolve && (
                <>
                  <p className="muted">
                    确认原进程已结束后，记录真实结果。结果未知时不会自动重跑。
                  </p>
                  <select
                    value={outcome}
                    onChange={(e) => setOutcome(e.target.value)}
                  >
                    <option value="failed">核实为失败</option>
                    <option value="succeeded">核实为成功</option>
                  </select>
                  <input
                    placeholder="填写核实方式及结果（至少 4 个字）"
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                  />
                  <button
                    className="button"
                    disabled={note.length < 4 || busy}
                    onClick={() =>
                      action("/targets/" + target.id + "/resolve", {
                        state: outcome,
                        note,
                      })
                    }
                  >
                    记录核实结果
                  </button>
                </>
              )}
            </div>
          )}
        </div>
      </div>
      <div className="form-actions sticky-actions">
        <span className="muted">
          {job.mode === "preview"
            ? "只执行勾选且通过检查的设备；预览 5 分钟内有效。"
            : "取消会停止后续步骤，正在进行的软件事务会继续完成。"}
        </span>
        {["queued", "running"].includes(job.status) && (
          <button
            className="button"
            disabled={busy}
            onClick={() => action("/jobs/" + job.id + "/cancel")}
          >
            停止后续操作
          </button>
        )}
        {job.targets.some((t) => ["failed", "blocked"].includes(t.state)) && (
          <button
            className="button"
            disabled={busy}
            onClick={() => action("/jobs/" + job.id + "/retry")}
          >
            <RefreshCw size={15} />
            重新检查失败目标
          </button>
        )}
        {job.mode === "preview" && (
          <button
            className="button primary"
            disabled={
              busy ||
              !ready.length ||
              job.status === "running" ||
              job.status === "queued"
            }
            onClick={() =>
              action("/jobs/" + job.id + "/execute", {
                device_ids: ready.map((t) => t.device_id),
              })
            }
          >
            <Check size={16} />
            确认执行 {ready.length} 台
          </button>
        )}
      </div>
    </Modal>
  );
}
function AddDevice({
  onClose,
  onDone,
  report,
}: {
  onClose: () => void;
  onDone: () => Promise<void>;
  report: (e: unknown) => void;
}) {
  const [kind, setKind] = useState("agent"),
    [enrollment, setEnrollment] = useState<{
      token: string;
      expires_in: number;
      hub_url: string;
      requires_ca?: boolean;
    } | null>(null),
    [os, setOS] = useState("linux-amd64"),
    [username, setUsername] = useState(""),
    [addressOptions, setAddressOptions] = useState<{default_url:string;addresses:{url:string;label:string;interface?:string;requires_ca:boolean}[]} | null>(null),
    [customAddress, setCustomAddress] = useState(false),
    [installURL, setInstallURL] = useState<string | null>(null),
    [customCA, setCustomCA] = useState<boolean | null>(null),
    [caFile, setCAFile] = useState("./homefleet-ca.crt"),
    [name, setName] = useState(""),
    [group, setGroup] = useState("家庭网络"),
    [type, setType] = useState("http"),
    [target, setTarget] = useState(""),
    [url, setURL] = useState(""),
    [busy, setBusy] = useState(false),
    [copied, setCopied] = useState(false),
    [error, setError] = useState("");
  const create = async () => {
    setBusy(true);
    try {
      setEnrollment(await api("/enrollment", "POST", {}));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    let active=true;
    api<{default_url:string;addresses:{url:string;label:string;interface?:string;requires_ca:boolean}[]}>("/enrollment-addresses")
      .then(value=>{if(active)setAddressOptions(value)})
      .catch(()=>{if(active)setError("可选地址加载失败，可以选择自定义地址手动填写。")});
    return ()=>{active=false};
  },[]);
  const address = installURL ?? enrollment?.hub_url ?? addressOptions?.default_url ?? window.location.origin;
  const origin = installationOrigin(address);
  const choices = addressOptions?.addresses || [];
  const selectedAddress = choices.find(option=>option.url===origin);
  const needsCA = customCA ?? selectedAddress?.requires_ca ?? enrollment?.requires_ca ?? false;
  const command = enrollment && origin && (!needsCA || caFile.trim())
    ? installationCommand({ windows: os.startsWith("windows"), address, token: enrollment.token, username, caFile: needsCA ? caFile.trim() : undefined })
    : "";
  useEffect(() => setCopied(false), [command]);
  return (
    <Modal
      title="接入设备"
      subtitle="电脑使用 Agent 主动连接，网络设备通过只读探测接入。"
      onClose={onClose}
    >
      <div className="tabs os-tabs">
        <button
          className={kind === "agent" ? "active" : ""}
          onClick={() => setKind("agent")}
        >
          <Monitor size={16} />
          电脑 / 服务器
        </button>
        <button
          className={kind === "appliance" ? "active" : ""}
          onClick={() => setKind("appliance")}
        >
          <Wifi size={16} />
          路由器 / AP
        </button>
      </div>
      {kind === "agent" ? (
        <>
          <div className="callout">
            <ShieldCheck size={19} />
            <span>
              每个令牌只接入一台设备，15
              分钟内有效。安装时指定你用于项目部署的普通用户账号。
            </span>
          </div>
          <div className="form-grid">
            <label>
              系统与架构
              <select value={os} onChange={(e) => setOS(e.target.value)}>
                <option value="linux-amd64">Linux · x86_64</option>
                <option value="linux-arm64">Linux · ARM64</option>
                <option value="darwin-arm64">macOS · Apple Silicon</option>
                <option value="windows-amd64">Windows · x86_64</option>
              </select>
            </label>
            <label>
              项目运行账号
              <input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="留空使用运行命令的当前账号"
              />
            </label>
          </div>
          <label>
            接入地址（下载与连接）
            <select
              value={customAddress ? "custom" : address}
              onChange={(e)=>{
                setCopied(false);
                if(e.target.value==="custom"){setCustomAddress(true);setInstallURL(address);return;}
                setCustomAddress(false);
                setInstallURL(e.target.value);
                setCustomCA(choices.find(option=>option.url===e.target.value)?.requires_ca ?? false);
              }}
            >
              {!choices.some(option=>option.url===address) && !customAddress && <option value={address}>当前接入地址 · {address}</option>}
              {choices.map(option=><option value={option.url} key={option.url}>{option.label} · {option.url}</option>)}
              <option value="custom">自定义域名或 IP…</option>
            </select>
          </label>
          {customAddress && <label>
            自定义接入地址
            <input
              value={address}
              onChange={(e) => {
                setInstallURL(e.target.value);
                setCopied(false);
                try {
                  const host = new URL(e.target.value).hostname;
                  setCustomCA(choices.find(option=>option.url===installationOrigin(e.target.value))?.requires_ca ?? (host.startsWith("[") || /^\d+\.\d+\.\d+\.\d+$/.test(host)));
                } catch { /* Keep the certificate choice while the URL is incomplete. */ }
              }}
              placeholder="https://域名或IP:端口"
              spellCheck={false}
            />
          </label>}
          {!origin && <p role="alert" className="error">请输入完整的 HTTPS 域名或 IP 地址，可带端口，不含路径。</p>}
          <label className="checkbox-line">
            <input type="checkbox" checked={needsCA} onChange={(e) => { setCustomCA(e.target.checked); setCopied(false); }} />
            使用自建 CA 证书
          </label>
          {needsCA && <label>CA 证书文件<input value={caFile} onChange={(e) => { setCAFile(e.target.value); setCopied(false); }} placeholder="目标设备上的 CA 文件路径" /></label>}
          <div className="download-row">
            <a
              className="button"
              href={
                (origin || "") + "/downloads/homefleet-agent-" +
                os +
                (os.startsWith("windows") ? ".exe" : "")
              }
            >
              <Download size={16} />
              下载 Agent
            </a>
            <a
              className="button"
              href={
                (origin || "") + "/downloads/" +
                (os.startsWith("windows")
                  ? "install-windows.ps1"
                  : "install-unix.sh")
              }
            >
              <ArrowDownToLine size={16} />
              安装脚本
            </a>
          </div>
          {!enrollment ? (
            <button
              className="button primary full"
              onClick={create}
              disabled={busy}
            >
              <Plus size={16} />
              生成一次性接入令牌
            </button>
          ) : (
            <>
              <label>
                一键安装命令<pre className="command-block">{command || "请先填写有效的接入地址和证书路径。"}</pre>
              </label>
              <button
                className="button"
                disabled={!command}
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(command);
                    setCopied(true);
                  } catch (e) {
                    report(e);
                  }
                }}
              >
                {copied ? <Check size={16} /> : <Copy size={16} />}复制命令
              </button>
              <p className="muted">
                自动下载并校验文件，Linux/macOS 自动识别架构；Windows 请使用管理员 PowerShell。
                {needsCA
                  ? "先将控制台的 CA 证书放到目标设备填写的路径。"
                  : "使用系统信任的 HTTPS 证书，无需另行导入 CA。"}
              </p>
            </>
          )}
        </>
      ) : (
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError("");
            try {
              await api("/appliances", "POST", {
                name,
                group,
                management_url: url,
                probe: {
                  type,
                  target,
                  latency_ms: 0,
                  checked_at: "0001-01-01T00:00:00Z",
                },
              });
              await onDone();
              onClose();
            } catch (e) {
              setError((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <div className="form-grid">
            <label>
              设备名称
              <input
                required
                placeholder="例如：客厅 AP"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <label>
              分组
              <input value={group} onChange={(e) => setGroup(e.target.value)} />
            </label>
          </div>
          <label>
            探测方式
            <select value={type} onChange={(e) => setType(e.target.value)}>
              <option value="http">HTTP / HTTPS</option>
              <option value="tcp">TCP 端口</option>
              <option value="icmp">ICMP Ping（IPv4）</option>
            </select>
          </label>
          <label>
            探测目标
            <input
              required
              placeholder={
                type === "http"
                  ? "http://192.168.1.1"
                  : type === "tcp"
                    ? "192.168.1.1:80"
                    : "192.168.1.1"
              }
              value={target}
              onChange={(e) => setTarget(e.target.value)}
            />
          </label>
          <label>
            管理页面地址
            <input
              type="url"
              placeholder="http://192.168.1.1（AP 可填 AC 地址）"
              value={url}
              onChange={(e) => setURL(e.target.value)}
            />
          </label>
          <div className="form-actions">
            <button className="button primary" disabled={busy}>
              <Plus size={16} />
              添加网络设备
            </button>
          </div>
        </form>
      )}
      {error && (
        <div role="alert" className="error-inline">
          {error}
        </div>
      )}
    </Modal>
  );
}
function DeviceDetail({
  device: d,
  onClose,
  refresh,
  report,
}: {
  device: Device;
  onClose: () => void;
  refresh: () => Promise<void>;
  report: (e: unknown) => void;
}) {
  const [history, setHistory] = useState<Sample[]>([]),
    [editing, setEditing] = useState(false),
    [name, setName] = useState(d.name),
    [group, setGroup] = useState(d.group),
    [managementURL, setManagementURL] = useState(d.management_url || ""),
    [revoke, setRevoke] = useState(false);
  const [trend, setTrend] = useState("resources");
  const trendData = history.map((sample) => {
    const diskFree = sample.disks.find((d) => d.mount === trend.slice(5))?.free;
    return {
      ...sample,
      extra: trend.startsWith("gpu:")
        ? (sample.gpus.find((g) => g.id === trend.slice(4))?.utilization ??
          null)
        : trend.startsWith("disk:")
          ? diskFree == null
            ? null
            : diskFree / 1073741824
          : null,
    };
  });
  useEffect(() => {
    api<Sample[]>("/devices/" + d.id + "/metrics")
      .then(setHistory)
      .catch(report);
  }, [d.id, d.latest?.at, report]);
  return (
    <Modal
      title={d.name}
      subtitle={d.platform + " · " + (d.arch || "网络设备")}
      onClose={onClose}
      wide
    >
      <div className="device-detail-top">
        <span className={"online-label " + (d.online ? "" : "offline")}>
          <i />
          {d.revoked ? "凭据已撤销" : d.online ? "在线" : "离线"}
        </span>
        <span>最近上报：{date(d.last_seen)}</span>
        <button className="text-button" onClick={() => setEditing(!editing)}>
          <Settings2 size={15} />
          设备设置
        </button>
        {d.management_url && (
          <a
            className="button"
            href={d.management_url}
            target="_blank"
            rel="noreferrer"
          >
            管理页面
            <ExternalLink size={15} />
          </a>
        )}
      </div>
      {editing && (
        <form
          className="edit-device"
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              await api("/devices/" + d.id, "PATCH", {
                name,
                group,
                management_url: managementURL,
              });
              await refresh();
              setEditing(false);
            } catch (e) {
              report(e);
            }
          }}
        >
          <div className="form-grid">
            <label>
              名称
              <input value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            <label>
              分组
              <input value={group} onChange={(e) => setGroup(e.target.value)} />
            </label>
          </div>
          <label>
            管理链接
            <input
              value={managementURL}
              onChange={(e) => setManagementURL(e.target.value)}
            />
          </label>
          <button className="button primary">保存</button>
        </form>
      )}
      <details className="address-details">
        <summary>
          网络地址 · {primaryAddress(d)}{" "}
          <span className="muted">（{d.addresses.length} 个地址）</span>
        </summary>
        <div className="address-grid">
          {d.addresses.map((a, i) => (
            <div key={i}>
              <span>
                <Network size={14} />
                {a.interface}
              </span>
              <code>{a.address}</code>
            </div>
          ))}
        </div>
      </details>
      {d.probe?.last_error && (
        <div className="callout warning">{d.probe.last_error}</div>
      )}
      {d.latest && (
        <>
          <div className="detail-stats">
            <div>
              <small>CPU 使用率</small>
              <strong>{percent(d.latest.cpu)}</strong>
            </div>
            <div>
              <small>内存使用率</small>
              <strong>{percent(d.latest.memory_percent)}</strong>
            </div>
            <div>
              <small>可用内存 / 总内存</small>
              <strong className="compact">
                {storage(d.latest.memory_available)} /{" "}
                {storage(d.latest.memory_total)}
              </strong>
            </div>
          </div>
          <div className="chart-title">
            <h3>资源趋势</h3>
            <span>
              {trend === "resources" ? (
                <>
                  <i className="legend cpu" />
                  CPU
                  <i className="legend memory" />
                  内存 · 最近 7 天
                </>
              ) : (
                "最近 7 天"
              )}
            </span>
            <select
              aria-label="趋势指标"
              value={trend}
              onChange={(e) => setTrend(e.target.value)}
            >
              <option value="resources">CPU / 内存</option>
              {d.latest.gpus
                .filter((g) => g.id)
                .map((g) => (
                  <option key={g.id} value={"gpu:" + g.id}>
                    {g.name} 利用率
                  </option>
                ))}
              {d.latest.disks.map((disk) => (
                <option key={disk.mount} value={"disk:" + disk.mount}>
                  {disk.mount} 可用空间
                </option>
              ))}
            </select>
          </div>
          <div className="chart">
            <ResponsiveContainer width="100%" height={205}>
              <AreaChart data={trendData}>
                <defs>
                  <linearGradient id="cpu-fill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#338361" stopOpacity={0.25} />
                    <stop offset="100%" stopColor="#338361" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid
                  strokeDasharray="3 4"
                  vertical={false}
                  stroke="#e8ece8"
                />
                <XAxis
                  dataKey="at"
                  tickFormatter={(s) =>
                    new Date(s).toLocaleTimeString("zh-CN", {
                      hour: "2-digit",
                      minute: "2-digit",
                    })
                  }
                  minTickGap={50}
                  tick={{ fontSize: 10 }}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis
                  domain={trend.startsWith("disk:") ? [0, "auto"] : [0, 100]}
                  unit={trend.startsWith("disk:") ? " GiB" : "%"}
                  tick={{ fontSize: 10 }}
                  axisLine={false}
                  tickLine={false}
                />
                <Tooltip labelFormatter={(v) => date(String(v))} />
                {trend === "resources" ? (
                  <>
                    <Area
                      type="monotone"
                      dataKey="cpu"
                      name="CPU %"
                      stroke="#338361"
                      fill="url(#cpu-fill)"
                      connectNulls={false}
                    />
                    <Area
                      type="monotone"
                      dataKey="memory_percent"
                      name="内存 %"
                      stroke="#aab385"
                      fill="none"
                      connectNulls={false}
                    />
                  </>
                ) : (
                  <Area
                    type="monotone"
                    dataKey="extra"
                    name={trend.startsWith("disk:") ? "可用空间 GiB" : "GPU %"}
                    stroke="#338361"
                    fill="url(#cpu-fill)"
                    connectNulls={false}
                  />
                )}
              </AreaChart>
            </ResponsiveContainer>
          </div>
          <h3 className="detail-section-title">
            <Cpu size={17} />
            GPU
          </h3>
          <div className="gpu-grid">
            {d.latest.gpus.map((g, i) => (
              <div className="gpu-card" key={g.id || i}>
                <span className="subtle-tag">
                  {g.vendor || "未检测到"} · {g.source}
                </span>
                <h4>{g.name || "GPU 采集状态"}</h4>
                <strong>{percent(g.utilization)}</strong>
                <Bar value={g.utilization} />
                {g.unified_memory ? (
                  <p>统一内存 · 不计为独立显存</p>
                ) : g.memory_note ? (
                  <p>{g.memory_note}</p>
                ) : (
                  <p>
                    显存 {storage(g.memory_used)} / {storage(g.memory_total)}
                  </p>
                )}
                {g.error && <small className="warning-text">{g.error}</small>}
              </div>
            ))}
          </div>
          <h3 className="detail-section-title">
            <HardDrive size={17} />
            磁盘与卷
          </h3>
          <div className="disk-list">
            {d.latest.disks.map((disk) => (
              <div key={disk.mount}>
                <div>
                  <b>{disk.mount}</b>
                  <small>
                    {disk.filesystem} · {disk.device}
                  </small>
                </div>
                <div>
                  <b>可用 {storage(disk.free)}</b>
                  <small>总计 {storage(disk.total)}</small>
                  <Bar value={disk.used_percent} />
                </div>
              </div>
            ))}
          </div>
          {Object.entries(d.latest.errors).map(([k, v]) => (
            <div className="callout warning" key={k}>
              {k}: {v}
            </div>
          ))}
        </>
      )}
      {d.kind === "agent" && (
        <>
          <h3 className="detail-section-title">
            <ShieldCheck size={17} />
            设备能力
          </h3>
          <div className="capability-list">
            {Object.entries(d.capabilities).map(([key, v]) => (
              <div key={key}>
                {v.available ? (
                  <CheckCircle2 size={16} />
                ) : (
                  <CircleHelp size={16} />
                )}
                <span>
                  {{
                    monitor: "资源监控",
                    user: "用户执行环境",
                    packages: "软件管理",
                    projects: "项目部署",
                    docker: "Docker 引擎",
                    agent_update: "Agent 后台更新",
                  }[key] || key}
                </span>
                <small>{v.available ? "可用" : v.reason || "暂不可用"}</small>
              </div>
            ))}
          </div>
          <p className="muted">
            项目运行用户：{d.run_user || "未设置"} · Agent {d.agent_version}
          </p>
          {!d.revoked && (
            <div className="revoke-area">
              {revoke ? (
                <>
                  <span>撤销后设备将停止接收新任务，重新接入需要新令牌。</span>
                  <button
                    className="button danger"
                    onClick={async () => {
                      try {
                        await api("/devices/" + d.id + "/revoke", "POST", {});
                        await refresh();
                        setRevoke(false);
                      } catch (e) {
                        report(e);
                      }
                    }}
                  >
                    确认撤销
                  </button>
                </>
              ) : (
                <button className="text-button" onClick={() => setRevoke(true)}>
                  撤销设备凭据
                </button>
              )}
            </div>
          )}
        </>
      )}
    </Modal>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
