import type { PlannerState } from "./session.js";
import type {
  CapacityAxis,
  HandoffStep,
  PartialDeclaredCapacity,
  WebMcpCatalog,
} from "./types.js";

export type HandoffShell = "bash" | "powershell";

const HANDOFF_OPERATIONS = [
  "stackkit.init",
  "stackkit.validate",
  "stackkit.resolve",
  "stackkit.generate",
  "stackkit.plan",
] as const;

const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f\u2028\u2029]/u;
const CAPACITY_AXES: ReadonlyArray<readonly [CapacityAxis, string]> = [
  ["cpu_cores", "CPU cores"],
  ["ram_gb", "RAM GiB"],
  ["storage_gb", "free storage GiB"],
];

/**
 * Format one argv vector for a human copy action. Shell-sensitive arguments
 * are quoted; control characters are rejected before anything is rendered.
 */
export function formatCommand(argv: readonly string[], shell: HandoffShell): string {
  assertShell(shell);
  if (!Array.isArray(argv) || argv.length === 0) {
    throw new TypeError("command argv must contain at least one argument");
  }

  const values = [...argv];
  values.forEach((value, index) => {
    if (typeof value !== "string") throw new TypeError(`command argv[${index}] must be a string`);
    assertNoControlCharacters(value, `command argv[${index}]`);
  });

  const rendered = values.map((value) => quoteArgument(value, shell));
  // PowerShell only needs the call operator when the executable itself had to
  // be quoted (for example, a path containing whitespace).
  const callOperator = shell === "powershell" && rendered[0] !== values[0] ? "& " : "";
  return `${callOperator}${rendered.join(" ")}`;
}

/**
 * Render a reviewable, per-step handoff packet from one validated PlannerState.
 * This is presentation only: it does not resolve selections or infer capacity.
 */
export function formatHandoffMarkdown(
  state: PlannerState,
  provenance: Pick<WebMcpCatalog, "source_sha" | "authority_bundle_sha256" | "catalog_sha256">,
  shell: HandoffShell,
): string {
  assertShell(shell);
  const handoff = state?.handoff;
  if (!handoff?.ready) throw new Error("handoff export requires a ready handoff");
  if (!Array.isArray(handoff.steps) || handoff.steps.length !== HANDOFF_OPERATIONS.length) {
    throw new Error("handoff export requires the complete ordered handoff");
  }

  const steps = handoff.steps.map((step, index) => validateHandoffStep(step, index, shell));
  const followUp = handoff.apply_follow_up;
  if (
    followUp?.id !== "stackkit.apply"
    || followUp.mutation !== true
    || followUp.owner_approval !== true
    || followUp.executable !== false
  ) {
    throw new Error("handoff export requires a non-executable owner-approved Apply follow-up");
  }

  const selection = state.selection;
  if (!selection?.stackkit_id || !selection.module_profiles) {
    throw new Error("handoff export requires the validated selection");
  }
  const useCases = selection.use_cases ?? [];
  const capacity = state.capacity;
  const declaredCapacity = capacity?.declared_capacity ?? state.declared_capacity ?? {};
  const notices = state.last_result?.notices ?? [];
  const lines: string[] = [
    "# StackKits handoff review",
    "",
    "This review packet was generated from one validated PlannerState. It contains no executable `stackkit.apply` step and grants no blanket execution permission.",
    "",
    "## Review instructions",
    "",
    "- Review the exact argv and the rendered shell command for every step before running it.",
    "- Run steps separately, one at a time, with the appropriate approval for that step; stop immediately if a step fails.",
    "- This packet does not authorize provider actions, installation, or any command outside the listed steps.",
    "- Run target compatibility checks with `stackkit compat` before any separately owner-approved Apply action.",
    "",
    "## Selection",
    "",
    `- StackKit: ${inlineJson(selection.stackkit_id)}`,
    "",
    "### Use cases",
    "",
  ];

  if (useCases.length === 0) {
    lines.push("No explicit use-case alternatives recorded.", "");
  } else {
    for (const useCase of useCases) {
      lines.push(`- ${inlineJson(useCase.use_case_id)} → alternative ${inlineJson(useCase.alternative_id)}`);
    }
    lines.push("");
  }

  lines.push("### Module-local profiles", "");
  for (const profile of selection.module_profiles) {
    lines.push(
      `- ${inlineJson(profile.module_id)}`,
      `  - compute: ${inlineJson(profile.compute_profile)}`,
      `  - storage: ${inlineJson(profile.storage_profile ?? "not selected")}`,
      `  - accelerator: ${inlineJson(profile.accelerator_profile ?? "not selected")}`,
    );
  }
  lines.push(
    "",
    "## Declared capacity",
    "",
    `- Status: ${inlineJson(handoff.capacity_status)}`,
    `- Origin: ${inlineJson(capacity?.origin ?? "user_declared")}`,
    ...CAPACITY_AXES.map(([axis, label]) => `- ${label}: ${inlineJson(capacityValue(declaredCapacity, axis))}`),
    "",
    capacity?.checks?.length
      ? `### Capacity checks\n\n${fencedBlock("json", safeJson(capacity.checks, 2))}`
      : "No capacity checks recorded in the PlannerState.",
    "",
    "## Notices",
    "",
    notices.length > 0 ? fencedBlock("json", safeJson(notices, 2)) : "No notices recorded.",
    "",
    "## Handoff steps",
    "",
    "Each block below is a separate command. Review and run it independently; do not paste the blocks as an unconditional sequence.",
    "",
  );

  steps.forEach((step, index) => {
    const [operation, argv, mutation, idempotent, ownerApproval] = step;
    lines.push(
      `### Step ${index + 1}: ${inlineJson(operation)}`,
      "",
      `- Mutation: ${inlineJson(mutation)}`,
      `- Idempotent: ${inlineJson(idempotent)}`,
      `- Owner approval: ${inlineJson(ownerApproval)}`,
      "- Complete argv:",
      fencedBlock("json", safeJson(argv, 2)),
      `- ${shellLabel(shell)} command:`,
      fencedBlock(shell, formatCommand(argv, shell)),
      "",
    );
  });

  lines.push(
    "## Apply boundary",
    "",
    `- Follow-up: ${inlineJson(followUp.id)}`,
    `- Mutation: ${inlineJson(followUp.mutation)}`,
    `- Idempotent: ${inlineJson(followUp.idempotent)}`,
    `- Owner approval: ${inlineJson(followUp.owner_approval)}`,
    `- Executable from this packet: ${inlineJson(followUp.executable)}`,
    "",
    "`stackkit.apply` is intentionally omitted from the command blocks. Target compatibility is mandatory first, and any Apply action must be a separate, explicitly owner-approved decision.",
    "",
    "## Provenance",
    "",
    fencedBlock("json", safeJson(provenance, 2)),
    "",
  );

  return `${lines.join("\n").trimEnd()}\n`;
}

function validateHandoffStep(step: HandoffStep, index: number, shell: HandoffShell): HandoffStep {
  if (!Array.isArray(step) || step.length !== 5) throw new TypeError(`handoff step ${index} is malformed`);
  const [operation, argv] = step;
  const expected = HANDOFF_OPERATIONS[index];
  if (operation !== expected || !Array.isArray(argv) || argv.length < 2 || argv[0] !== "stackkit") {
    throw new Error("handoff export encountered a non-copyable operation or executable");
  }
  if (argv[1] !== operation.slice("stackkit.".length)) {
    throw new Error("handoff export encountered a command-operation mismatch");
  }
  formatCommand(argv, shell);
  return [operation, [...argv], step[2], step[3], step[4]];
}

function capacityValue(capacity: PartialDeclaredCapacity, axis: CapacityAxis): number | "not declared" {
  const value = capacity[axis];
  return typeof value === "number" && Number.isFinite(value) ? value : "not declared";
}

function quoteArgument(value: string, shell: HandoffShell): string {
  // Keep ordinary flags, IDs, assignments, domains, and paths readable while
  // excluding shell operators, expansion syntax, wildcard characters, and whitespace.
  const safe = /^[A-Za-z0-9_./:=+\-]+$/u.test(value);
  if (safe) return value;
  if (shell === "powershell") return `'${value.replace(/'/g, "''")}'`;
  return `'${value.replace(/'/g, "'\"'\"'")}'`;
}

function assertShell(shell: HandoffShell): void {
  if (shell !== "bash" && shell !== "powershell") throw new TypeError("unsupported handoff shell");
}

function assertNoControlCharacters(value: string, label: string): void {
  if (CONTROL_CHARACTERS.test(value)) throw new TypeError(`${label} contains unsupported control characters`);
}

function shellLabel(shell: HandoffShell): string {
  return shell === "powershell" ? "PowerShell" : "Bash";
}

function inlineJson(value: unknown): string {
  return safeJson(value, 0).replace(/`/g, "\\u0060");
}

function safeJson(value: unknown, spacing?: number): string {
  const encoded = JSON.stringify(value, null, spacing);
  if (encoded === undefined) throw new TypeError("handoff export encountered non-serializable state");
  return encoded.replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
}

function fencedBlock(language: string, body: string): string {
  const maxRun = Math.max(0, ...(body.match(/`+/gu) ?? []).map((run) => run.length));
  const fence = "`".repeat(Math.max(3, maxRun + 1));
  return `${fence}${language}\n${body}\n${fence}`;
}
