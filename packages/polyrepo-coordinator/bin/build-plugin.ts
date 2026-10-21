import { cpSync, existsSync, mkdtempSync, readFileSync, renameSync, rmSync, statSync } from "node:fs";
import { basename, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const courierURL = "https://github.com/paulharkink/opencode-courier.git";
const courierCommit = "0b91b0c2e7a6c6c4b56e99634172a19d7e4bad72";

function run(command: string, args: string[], cwd: string) {
  const result = spawnSync(command, args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed (${result.status})`);
  return result.stdout.trim();
}

const request = JSON.parse(readFileSync(0, "utf8"));
const context = request.context ?? {};
const stateRoot = context.manager_state_dir;
if (typeof stateRoot !== "string" || !stateRoot) throw new Error("AACT manager state path is missing");
const stageRoot = context.staging_dir;
if (typeof stageRoot !== "string" || !stageRoot) throw new Error("AACT plugin staging path is missing");
const inventory = join(stateRoot, "manager", "repo-map-v1.json");
if (!existsSync(inventory)) throw new Error("run `aact polyrepo configure` and `aact polyrepo list` before installing this capability");
const inventoryData = JSON.parse(readFileSync(inventory, "utf8"));
if (inventoryData.version !== 1) throw new Error("unsupported Polyrepo Coordinator inventory version");

const inputs = request.inputs ?? {};
const localSource = typeof inputs.courier_source === "string" ? inputs.courier_source.trim() : "";
const work = mkdtempSync(join(stageRoot, ".build-"));
const source = join(work, "source");
let revision = courierCommit;
try {
  if (localSource) {
    const resolvedSource = resolve(localSource);
    if (!statSync(resolvedSource).isDirectory()) throw new Error("Courier source must be a directory");
    cpSync(resolvedSource, source, { recursive: true, filter: (path) => ![".git", "node_modules", "dist"].includes(basename(path)) });
    if (existsSync(join(resolvedSource, ".git"))) {
      try { revision = run("git", ["-C", resolvedSource, "rev-parse", "HEAD"], resolvedSource); }
      catch { revision = "local-unversioned"; }
    } else {
      revision = "local-unversioned";
    }
  } else {
    run("git", ["clone", "--no-checkout", courierURL, source], work);
    run("git", ["-C", source, "checkout", "--detach", courierCommit], work);
  }
  if (!existsSync(join(source, "package.json"))) throw new Error("Courier source must contain package.json");
  run("bun", ["install", "--frozen-lockfile"], source);
  run("bun", ["run", "build"], source);
  const built = join(source, "dist", "index.js");
  if (!existsSync(built)) throw new Error("Courier build did not produce dist/index.js");
  const id = `${Date.now()}-${process.pid}`;
  rmSync(join(source, ".git"), { recursive: true, force: true });
  const published = join(stageRoot, `courier-build-${id}`);
  renameSync(source, published);
  process.stdout.write(JSON.stringify({ reference: join(published, "dist"), options: { repoMapFile: inventory }, revision }));
} finally {
  rmSync(work, { recursive: true, force: true });
}
