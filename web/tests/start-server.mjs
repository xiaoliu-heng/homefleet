import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { resolve } from "node:path";
const root = resolve("..");
mkdirSync(resolve(root, ".local"), { recursive: true, mode: 0o700 });
const data = mkdtempSync(resolve(root, ".local/e2e-"));
// An isolated, explicitly labelled fixture release; never a downloadable real Agent.
const releases=resolve(data,"releases"), fixture=Buffer.from("e2e fixture only"), name="homefleet-agent-linux-amd64";
mkdirSync(resolve(releases,"agents/0.3.0"),{recursive:true});
writeFileSync(resolve(releases,"agents/0.3.0",name),fixture);
writeFileSync(resolve(releases,"agent-release.json"),JSON.stringify({version:"0.3.0",minimum_version:"0.2.0",published_at:new Date().toISOString(),artifacts:{"linux-amd64":{name,size:fixture.length,sha256:createHash("sha256").update(fixture).digest("hex")}}}));
const child = spawn(
  resolve(root, "bin/homefleet-hub"),
  [
    "--dev",
    "--listen",
    "127.0.0.1:8091",
    "--public-url",
    "http://127.0.0.1:8091",
    "--db",
    resolve(data, "test.db"),
  ],
  {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, HOMEFLEET_RELEASES: releases, HOMEFLEET_ALLOWED_ORIGINS: "https://192.0.2.10:8443,https://198.51.100.20:8443", HOMEFLEET_ADMIN_PASSWORD: "e2e-test-password-only" },
  },
);
for (const sig of ["SIGTERM", "SIGINT"]) process.on(sig, () => child.kill(sig));
child.on("exit", (code) => process.exit(code ?? 0));
