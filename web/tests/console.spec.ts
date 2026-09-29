import {
  test,
  expect,
  request as requestFactory,
  type APIRequestContext,
  type Page,
} from "@playwright/test";
const base = "http://127.0.0.1:8091";
const headers = { "X-HomeFleet-Request": "1" };
let admin: APIRequestContext;
let cookies: any[];
let hostA: { id: string; token: string }, hostB: { id: string; token: string };
async function post(path: string, data: any) {
  const r = await admin.post("/api/v1" + path, { data, headers });
  expect(r.ok(), await r.text()).toBeTruthy();
  return r.json();
}
async function agent(host: { token: string }, path: string, data?: any) {
  const r = await admin.fetch("/agent/v1" + path, {
    method: data === undefined ? "GET" : "POST",
    data,
    headers: { Authorization: "Bearer " + host.token },
  });
  expect(r.ok(), await r.text()).toBeTruthy();
  return r.status() === 204 ? null : r.json();
}
const sample = {
  cpu: 24,
  memory_percent: 48,
  memory_total: 17179869184,
  memory_available: 8933531975,
  errors: {},
  gpus: [
    {
      id: "test-gpu",
      name: "NVIDIA · 自动化测试数据",
      vendor: "NVIDIA",
      source: "test fixture",
      utilization: 35,
      memory_used: 1073741824,
      memory_total: 8589934592,
      temperature: 45,
      unified_memory: false,
    },
  ],
  disks: [
    {
      mount: "/",
      device: "test-disk",
      filesystem: "ext4",
      total: 1000000000000,
      free: 600000000000,
      used_percent: 40,
    },
  ],
};
const device = (name: string) => ({
  name,
  os: "linux",
  platform: "Arch · 自动化测试设备",
  kind: "agent",
  addresses: [
    { interface: "eth0", address: "192.0.2.10/24" },
    { interface: "eth1", address: "198.51.100.20/24" },
  ],
  capabilities: {
    monitor: { available: true },
    packages: { available: true, reason: "pacman" },
    pacman_cached_install: { available: true },
    projects: { available: true },
    docker: { available: true },
    user: { available: true },
  },
});
async function register(name: string) {
  const token = await post("/enrollment", {});
  const r = await admin.post("/agent/v1/register", {
    data: { token: token.token, device: device(name) },
  });
  expect(r.ok()).toBeTruthy();
  const host = await r.json();
  await agent(host, "/heartbeat", { device: device(name), sample });
  return host;
}
async function overview(page: Page) {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "设备总览", exact: true }),
  ).toBeVisible();
}
const plan = {
  steps: [
    {
      name: "安装 git（使用已有软件源索引）",
      program: "pacman",
      args: ["-S", "--noconfirm", "--needed", "--", "git"],
      identity: "system",
      package_transaction: true,
      health: false,
    },
  ],
  warnings: ["使用本机已有的软件源索引，仅安装目标包及必需依赖。"],
  expires_at: new Date(Date.now() + 300000).toISOString(),
};
test.beforeAll(async () => {
  admin = await requestFactory.newContext({ baseURL: base });
  const r = await admin.post("/api/v1/login", {
    data: { password: "e2e-test-password-only" },
    headers,
  });
  expect(r.ok()).toBeTruthy();
  cookies = (await admin.storageState()).cookies;
});
test.afterAll(async () => admin.dispose());
test.beforeEach(async ({ context, page }) => {
  await context.addCookies(cookies);
  page.on("pageerror", (e) => {
    throw e;
  });
});

test("login, wrong password, empty console and logout", async ({
  page,
  context,
}) => {
  await context.clearCookies();
  await page.goto("/");
  await page.getByLabel("管理员密码").fill("incorrect");
  await page.getByRole("button", { name: "进入控制台" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await page.getByLabel("管理员密码").fill("e2e-test-password-only");
  await page.getByRole("button", { name: "进入控制台" }).click();
  await expect(page.getByText("给你的设备，一个共同的家")).toBeVisible();
  await page.getByRole("button", { name: "退出登录" }).click();
  await expect(
    page.getByRole("heading", { name: "登录你的控制台" }),
  ).toBeVisible();
});
test("enrollment contains real installer commands and keyboard dismissal", async ({
  page,
}) => {
  await overview(page);
  await page.getByRole("button", { name: "接入设备", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("接入地址（下载与连接）").selectOption("custom");
  await dialog.getByLabel("系统与架构").selectOption("windows-amd64");
  await dialog.getByLabel("自定义接入地址").fill("https://homefleet.example.test");
  await dialog.getByLabel("项目运行账号").fill("homeuser");
  await dialog.getByRole("button", { name: "生成一次性接入令牌" }).click();
  await expect(dialog.locator(".command-block")).toContainText(
    "$HF_URL/install.ps1",
  );
  await expect(dialog.locator(".command-block")).toContainText(
    "-RunUser 'homeuser'",
  );
  await expect(dialog.getByRole("link", { name: "下载 Agent" })).toHaveAttribute("href", "https://homefleet.example.test/downloads/homefleet-agent-windows-amd64.exe");
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});
test("network device is registered through an explicit probe and management link", async ({
  page,
}) => {
  await overview(page);
  await page.getByRole("button", { name: "接入设备", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "路由器 / AP" }).click();
  await dialog.getByLabel("设备名称").fill("测试 AP · 仅回环探测");
  await dialog.getByLabel("探测方式").selectOption("tcp");
  await dialog.getByLabel("探测目标").fill("127.0.0.1:8091");
  await dialog.getByLabel("管理页面地址").fill("http://192.0.2.1");
  await dialog.getByRole("button", { name: "添加网络设备" }).click();
  await expect(
    page.getByRole("button", { name: "查看 测试 AP · 仅回环探测" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "查看 测试 AP · 仅回环探测" }).click();
  await expect(page.getByRole("link", { name: "管理页面" })).toHaveAttribute(
    "href",
    "http://192.0.2.1",
  );
});
test("public certificate enrollment uses its domain without manual CA installation", async ({ page }) => {
  await page.route("**/api/v1/enrollment", async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    await route.fulfill({ response, json: { ...data, hub_url: "https://homefleet.example.test", requires_ca: false } });
  });
  await overview(page);
  await page.getByRole("button", { name: "接入设备", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "生成一次性接入令牌" }).click();
  for (const os of ["linux-amd64", "windows-amd64", "darwin-arm64"]) {
    await dialog.getByLabel("系统与架构").selectOption(os);
    await expect(dialog.locator(".command-block")).toContainText("https://homefleet.example.test");
    await expect(dialog.locator(".command-block")).not.toContainText("homefleet-ca.crt");
  }
  await expect(dialog.getByText(/无需另行导入 CA/)).toBeVisible();
});
test("one-click installation switches domain and IP for both download and enrollment", async ({ page }) => {
  await overview(page);
  await page.getByRole("button", { name: "接入设备", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("接入地址（下载与连接）").selectOption("custom");
  await dialog.getByLabel("自定义接入地址").fill("https://fleet.example.test");
  await dialog.getByRole("button", { name: "生成一次性接入令牌" }).click();
  for (const choice of ["https://192.0.2.10:8443", "https://198.51.100.20:8443"]) {
    await dialog.getByLabel("接入地址（下载与连接）").selectOption(choice);
    await expect(dialog.getByLabel("自定义接入地址")).toHaveCount(0);
    await expect(dialog.getByLabel("使用自建 CA 证书")).toBeChecked();
    await expect(dialog.locator(".command-block")).toContainText(`HF_URL='${choice}'`);
    await expect(dialog.getByRole("link", {name:"下载 Agent"})).toHaveAttribute("href",choice+"/downloads/homefleet-agent-linux-amd64");
  }
  await dialog.getByLabel("接入地址（下载与连接）").selectOption("custom");
  for (const address of ["https://fleet.example.test", "https://192.0.2.10:8443", "https://198.51.100.20:8443", "https://[2001:db8::20]:8443"]) {
    await dialog.getByLabel("自定义接入地址").fill(address);
    await dialog.getByLabel("系统与架构").selectOption("linux-amd64");
    await expect(dialog.locator(".command-block")).toContainText(`HF_URL='${address}'`);
    await expect(dialog.locator(".command-block")).toContainText('$HF_URL/install.sh');
    await expect(dialog.locator(".command-block")).toContainText('--hub "$HF_URL"');
    await expect(dialog.getByRole("link", { name: "下载 Agent" })).toHaveAttribute("href", address + "/downloads/homefleet-agent-linux-amd64");
    if (!address.includes("example.test")) {
      await expect(dialog.getByLabel("使用自建 CA 证书")).toBeChecked();
      await expect(dialog.locator(".command-block")).toContainText('--cacert "$HF_CA"');
      await expect(dialog.locator(".command-block")).toContainText('--ca "$HF_CA"');
    }
    await dialog.getByLabel("系统与架构").selectOption("windows-amd64");
    await expect(dialog.locator(".command-block")).toContainText(`$HF_URL = '${address}'`);
    await expect(dialog.locator(".command-block")).toContainText('$HF_URL/install.ps1');
    await expect(dialog.locator(".command-block")).toContainText('-Hub $HF_URL');
  }
});
test("installer refuses invalid addresses and quotes account names", async ({ page }) => {
  await overview(page);
  await page.getByRole("button", { name: "接入设备", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("接入地址（下载与连接）").selectOption("custom");
  await dialog.getByRole("button", { name: "生成一次性接入令牌" }).click();
  for (const address of ["http://192.0.2.10", "https://example.test/path", "https://user:password@example.test", "https://example.test?token=secret"]) {
    await dialog.getByLabel("自定义接入地址").fill(address);
    await expect(dialog.getByRole("alert")).toBeVisible();
    await expect(dialog.getByRole("button", { name: "复制命令" })).toBeDisabled();
  }
  await dialog.getByLabel("自定义接入地址").fill("https://fleet.example.test");
  await dialog.getByLabel("项目运行账号").fill("home'user");
  await expect(dialog.locator(".command-block")).toContainText(`--run-user 'home'"'"'user'`);
  await dialog.getByLabel("系统与架构").selectOption("windows-amd64");
  await expect(dialog.locator(".command-block")).toContainText("-RunUser 'home''user'");
  await expect(dialog.getByRole("button", { name: "复制命令" })).toBeEnabled();
});
test("project templates preserve encrypted secrets and version changes", async ({
  page,
}) => {
  await overview(page);
  await page.getByRole("button", { name: "项目与应用", exact: true }).click();
  await page.getByRole("button", { name: "登记项目", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("项目名称").fill("浏览器测试项目");
  await dialog
    .getByLabel("Git 仓库地址")
    .fill("https://example.test/project.git");
  await dialog.getByLabel("linux 项目目录").fill("/srv/test-project");
  await dialog.getByLabel("部署步骤").fill("echo deploy");
  await dialog
    .getByLabel("健康检查")
    .fill("curl --fail http://127.0.0.1:3000/health");
  await dialog
    .getByLabel("机密环境变量")
    .fill("TOKEN=browser-test-private-value");
  await dialog.getByLabel("Compose 文件路径").fill("compose.yaml");
  await dialog.getByRole("button", { name: "保存项目" }).click();
  await expect(
    page.getByRole("heading", { name: "浏览器测试项目" }),
  ).toBeVisible();
  const response = await admin.get("/api/v1/projects");
  expect(await response.text()).not.toContain("browser-test-private-value");
  await page.getByRole("button", { name: "编辑 浏览器测试项目" }).click();
  await expect(page.getByRole("dialog").getByLabel("机密环境变量")).toHaveValue(
    "",
  );
  await page
    .getByRole("dialog")
    .getByLabel("项目名称")
    .fill("浏览器测试项目 v2");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存项目" })
    .click();
  const projects = await (await admin.get("/api/v1/projects")).json();
  expect(projects[0].version).toBe(2);
  expect(projects[0].secret_keys).toEqual(["TOKEN"]);
});
test("real API batch preview submits only compatible targets and streams logs", async ({
  page,
}) => {
  hostA = await register("测试 Arch A");
  hostB = await register("测试 Arch B");
  await overview(page);
  await page.getByRole("button", { name: "软件管理", exact: true }).click();
  await page.getByRole("checkbox", { name: /测试 Arch A/ }).check();
  await page.getByRole("checkbox", { name: /测试 Arch B/ }).check();
  await expect(page.getByText(/Arch 安装使用已有软件源索引/)).toBeVisible();
  await page.getByRole("combobox", { name: /^操作/ }).selectOption("upgrade");
  await expect(page.getByText(/Arch 的更新操作包含完整系统升级/)).toBeVisible();
  await page.getByRole("combobox", { name: /^操作/ }).selectOption("install");
  const pending = page.waitForResponse(
    (r) => r.url().endsWith("/jobs/preview") && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "预检查操作" }).click();
  const preview = await (await pending).json();
  const a = await agent(hostA, "/claim"),
    b = await agent(hostB, "/claim");
  expect(a.mode).toBe("preview");
  expect(await (await admin.get("/api/v1/jobs")).json()).not.toContainEqual(
    expect.objectContaining({ mode: "execute" }),
  );
  await agent(hostA, "/targets/" + a.target_id + "/result", {
    state: "succeeded",
    plan,
  });
  await agent(hostB, "/targets/" + b.target_id + "/result", {
    state: "blocked",
    reason: "需要管理员权限",
  });
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByText("使用本机已有的软件源索引，仅安装目标包及必需依赖。"),
  ).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "确认执行 1 台" }),
  ).toBeEnabled();
  await page.setViewportSize({ width: 1280, height: 720 });
  await expect(dialog.getByRole("button", { name: "确认执行 1 台" })).toBeInViewport({ ratio: 1 });
  const submitted = page.waitForResponse((r) => r.url().endsWith("/execute"));
  await dialog.getByRole("button", { name: "确认执行 1 台" }).click();
  const job = await (await submitted).json();
  expect(job.targets.map((t: any) => t.device_id)).toEqual([hostA.id]);
  const assigned = await agent(hostA, "/claim");
  expect(assigned.mode).toBe("execute");
  expect(assigned.plan.steps[0].args).toEqual(["-S", "--noconfirm", "--needed", "--", "git"]);
  const row = {
    seq: 1,
    at: new Date().toISOString(),
    stream: "stdout",
    text: "测试代理：事务已完成",
  };
  await agent(hostA, "/targets/" + assigned.target_id + "/logs", [row]);
  await agent(hostA, "/targets/" + assigned.target_id + "/logs", [row]);
  await agent(hostA, "/targets/" + assigned.target_id + "/result", {
    state: "succeeded",
    exit_code: 0,
    health: "not_configured",
  });
  await expect(dialog.locator(".log-view")).toContainText(row.text);
  expect(
    await (
      await admin.get("/api/v1/targets/" + assigned.target_id + "/logs")
    ).json(),
  ).toHaveLength(1);
  const same = await post("/jobs/" + preview.id + "/execute", {
    device_ids: [hostA.id],
  });
  expect(same.id).toBe(job.id);
  await page.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("button", { name: "设备总览", exact: true }).click();
  await page.getByRole("button", { name: "查看 测试 Arch A" }).click();
  await page.getByText(/网络地址 ·/).click();
  await expect(page.getByText("198.51.100.20/24")).toBeVisible();
  await page.getByLabel("趋势指标").selectOption("gpu:test-gpu");
  await expect(
    page.getByText("NVIDIA · 自动化测试数据", { exact: true }),
  ).toBeVisible();
});
test("unknown outcome requires explicit verification before retry", async ({
  page,
}) => {
  const p = await post("/jobs/preview", {
    device_ids: [hostA.id],
    action: { kind: "package", operation: "install", package: "git" },
  });
  const a = await agent(hostA, "/claim");
  await agent(hostA, "/targets/" + a.target_id + "/result", {
    state: "succeeded",
    plan,
  });
  const execution = await post("/jobs/" + p.id + "/execute", {
    device_ids: [hostA.id],
  });
  const run = await agent(hostA, "/claim");
  await agent(hostA, "/targets/" + run.target_id + "/result", {
    state: "unknown",
    reason: "测试：连接中断，需核实",
  });
  await overview(page);
  await page.getByRole("button", { name: "任务中心", exact: true }).click();
  await page.getByRole("button", { name: "查看任务 " + execution.id }).click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("button", { name: "重新检查失败目标" }),
  ).toHaveCount(0);
  await dialog.getByRole("button", { name: "已在设备上核实实际结果" }).click();
  await dialog
    .getByPlaceholder("填写核实方式及结果（至少 4 个字）")
    .fill("测试核实：原进程已退出且操作失败");
  await dialog.getByRole("button", { name: "记录核实结果" }).click();
  await expect(
    dialog.getByRole("button", { name: "重新检查失败目标" }),
  ).toBeEnabled();
  const response = page.waitForResponse((r) => r.url().endsWith("/retry"));
  await dialog.getByRole("button", { name: "重新检查失败目标" }).click();
  const retry = await (await response).json();
  expect(retry.mode).toBe("preview");
  expect(retry.targets).toHaveLength(1);
  await post("/jobs/" + retry.id + "/cancel", {});
  expect(
    (await (await admin.get("/api/v1/jobs/" + execution.id)).json()).targets[0]
      .state,
  ).toBe("failed");
});
test("mobile layout keeps navigation and controls within viewport", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await overview(page);
  await expect(page.locator('.sidebar nav button[aria-current="page"]')).toHaveText("设备总览");
  await expect(page.locator(".sidebar nav button span").filter({ hasText: "软件管理" })).toBeVisible();
  const deviceRows = page.locator(".device-table tbody tr");
  await expect(deviceRows.first().locator('[data-label="CPU"]')).toBeVisible();
  await expect(deviceRows.first().locator('[data-label="磁盘可用"]')).toBeVisible();
  const width = await page.evaluate(() => ({
    body: document.documentElement.scrollWidth,
    view: window.innerWidth,
  }));
  expect(width.body).toBeLessThanOrEqual(width.view);
  await page.getByRole("button", { name: "查看 测试 Arch B", exact: true }).scrollIntoViewIfNeeded();
  expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
  await page.getByRole("button", { name: "项目与应用", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "项目与应用", exact: true }),
  ).toBeInViewport({ ratio: 1 });
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  await page.getByRole("button", { name: "登记项目", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
});

test("dialogs contain keyboard focus and return it to their trigger", async ({ page }) => {
  await overview(page);
  const trigger = page.getByRole("button", { name: "接入设备", exact: true });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "接入设备", exact: true });
  await expect(dialog).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(dialog.getByRole("button", { name: "生成一次性接入令牌" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("button", { name: "关闭", exact: true })).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(dialog.getByRole("button", { name: "生成一次性接入令牌" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: /^全部设备/ })).toBeFocused();
});

test("empty search recovers the device list and selection reports mixed state", async ({ page }) => {
  await overview(page);
  const deviceNames = await page.locator(".device-name b").allTextContents();
  await page.getByLabel("搜索设备").fill("no-such-device-for-ui-test");
  await expect(page.getByRole("heading", { name: "没有匹配的设备" })).toBeVisible();
  await page.getByRole("button", { name: "清除筛选" }).click();
  await expect(page.getByLabel("搜索设备")).toHaveValue("");
  await expect(page.locator(".device-name b")).toHaveText(deviceNames);
  await page.getByRole("checkbox", { name: "选择 测试 Arch A", exact: true }).check();
  await expect(page.getByRole("checkbox", { name: "选择当前列表全部主机" })).toHaveJSProperty("indeterminate", true);
});

test("Agent versions, pinned manual update and old-Agent migration", async ({page})=>{
  const current=await register("Agent 更新测试主机");
  const old=await register("Agent 旧版测试主机");
  const currentDevice={...device("Agent 更新测试主机"),arch:"amd64",agent_version:"0.2.0",capabilities:{...device("").capabilities,agent_update:{available:true}}};
  await agent(current,"/heartbeat",{device:currentDevice,sample});
  await agent(old,"/heartbeat",{device:{...device("Agent 旧版测试主机"),arch:"amd64",agent_version:"0.1.0"},sample});
  await overview(page);
  await page.getByRole("button",{name:"Agent 更新",exact:true}).click();
  await expect(page.getByRole("heading",{name:"可发布版本 v0.3.0"})).toBeVisible();
  const oldRow=page.getByRole("row").filter({hasText:"Agent 旧版测试主机"});
  await expect(oldRow).toContainText("0.1.0");await expect(oldRow).toContainText("需先手动更新一次");
  await page.getByRole("button",{name:"选择可升级设备"}).click();
  await expect(page.getByRole("checkbox",{name:"更新 Agent 更新测试主机",exact:true})).toBeChecked();
  await expect(page.getByRole("checkbox",{name:"更新 Agent 旧版测试主机",exact:true})).not.toBeChecked();
  const response=page.waitForResponse(r=>r.url().endsWith("/jobs/preview")&&r.request().method()==="POST");
  await page.getByRole("button",{name:"预览 Agent 更新"}).click();
  const preview=await(await response).json();expect(preview.mode).toBe("preview");
  const check=await agent(current,"/claim");expect(check.action.agent_release.version).toBe("0.3.0");
  const updatePlan={steps:[],warnings:["仅在手动确认后替换 Agent"],agent_update:{from_version:"0.2.0",version:"0.3.0",artifact:check.action.agent_release.artifacts["linux-amd64"]}};
  await agent(current,"/targets/"+check.target_id+"/result",{state:"succeeded",plan:updatePlan});
  const dialog=page.getByRole("dialog");
  await expect(dialog.getByText("Agent 0.2.0 → 0.3.0",{exact:true})).toBeVisible();
  await expect(dialog.getByText(/SHA-256:/)).toBeVisible();
  const submit=page.waitForResponse(r=>r.url().endsWith("/execute"));
  await dialog.getByRole("button",{name:"确认执行 1 台"}).click();
  const job=await(await submit).json();expect(job.targets).toHaveLength(1);
  const run=await agent(current,"/claim");expect(run.plan.agent_update.version).toBe("0.3.0");
  await agent(current,"/targets/"+run.target_id+"/result",{state:"succeeded",agent_version:"0.3.0",health:"healthy",reason:"测试代理：新版已经回连"});
  await expect(dialog.getByText("执行结果版本")).toContainText("0.3.0");
  await dialog.getByRole("button",{name:"关闭",exact:true}).click();
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)).toBe(false);
});
