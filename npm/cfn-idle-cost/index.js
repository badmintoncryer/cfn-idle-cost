// The cfn-idle-cost binary, and a CDK policy validation plugin that reports
// the idle cost at `cdk synth`.
const { spawnSync } = require('child_process');
const path = require('path');

/** The path of the platform binary installed via optionalDependencies. */
function binaryPath() {
  if (process.env.CFN_IDLE_COST_BINARY) return process.env.CFN_IDLE_COST_BINARY;
  const exe = process.platform === 'win32' ? 'cfn-idle-cost.exe' : 'cfn-idle-cost';
  const pkg = `cfn-idle-cost-${process.platform}-${process.arch}`;
  try {
    return require.resolve(`${pkg}/bin/${exe}`);
  } catch {
    throw new Error(`cfn-idle-cost: no prebuilt binary for ${process.platform}-${process.arch} (${pkg} is not installed). ` +
      'Install with optional dependencies enabled, or use `go install github.com/badmintoncryer/cfn-idle-cost@latest` and set CFN_IDLE_COST_BINARY.');
  }
}

const money = (v) => (v >= 10 ? `$${Math.round(v)}` : `$${v.toFixed(2)}`);

/**
 * A policy validation plugin: at `cdk synth`, estimates the idle cost of the
 * app's stacks and fails the synth when it is over `max` dollars a month.
 *
 * Options:
 * - max: fail the synth when the total idle cost is over this many USD/mo
 * - warnAbove: report each resource that idles at this many USD/mo or more, as a warning
 * - region: region for env-agnostic stacks (default: CDK_DEFAULT_REGION, AWS_REGION)
 * - args: more cfn-idle-cost flags, e.g. ['--parameter', 'Env=prod']
 */
class IdleCostValidation {
  constructor(options = {}) {
    this.name = 'cfn-idle-cost';
    this.options = options;
  }

  validate(context) {
    const o = this.options;
    const fallback = o.region || process.env.CDK_DEFAULT_REGION || process.env.AWS_REGION || process.env.AWS_DEFAULT_REGION;
    const stacks = context.stackTemplates
      ? context.stackTemplates.map((s) => ({ path: s.templatePath, region: s.region }))
      : context.templatePaths.map((p) => ({ path: p }));
    const byRegion = new Map();
    const violations = [];
    for (const s of stacks) {
      const region = s.region || fallback;
      if (!region) {
        violations.push(violation('idle-cost-no-region', 'error', 'The stack has no region, so its idle cost could not be estimated.',
          'Give the stack an env, or pass { region } to IdleCostValidation.', [{ templatePath: s.path }]));
        continue;
      }
      if (!byRegion.has(region)) byRegion.set(region, []);
      byRegion.get(region).push(s.path);
    }
    let total = 0;
    const resources = [];
    for (const [region, paths] of byRegion) {
      const r = spawnSync(binaryPath(), ['--format', 'json', '--region', region, ...(o.args || []), ...paths], { encoding: 'utf8' });
      if (r.error || r.status !== 0) {
        const why = r.error ? r.error.message : (r.stderr || '').trim();
        violations.push(violation('idle-cost-error', 'error', `cfn-idle-cost failed: ${why}`, undefined, paths.map((p) => ({ templatePath: p }))));
        continue;
      }
      // Plain templates are named by their file name, without the extension.
      const file = new Map(paths.map((p) => [path.basename(p, path.extname(p)), p]));
      for (const reg of JSON.parse(r.stdout).regions) {
        total += reg.monthly;
        for (const st of reg.stacks) {
          const byId = new Map();
          for (const it of st.items) {
            const e = byId.get(it.logicalId) || { logicalId: it.logicalId, path: it.path, type: it.type, monthly: 0, why: [], templatePath: file.get(st.name) || '' };
            e.monthly += it.monthly;
            e.why.push(`${money(it.monthly)} ${it.reason}`);
            byId.set(it.logicalId, e);
          }
          resources.push(...byId.values());
        }
      }
    }
    resources.sort((a, b) => b.monthly - a.monthly);
    const at = (e) => ({ resourceLogicalId: e.logicalId, templatePath: e.templatePath, locations: [e.path] });
    if (o.warnAbove !== undefined) {
      for (const e of resources.filter((x) => x.monthly >= o.warnAbove)) {
        violations.push(violation('idle-cost-resource', 'warning', `${e.path} (${e.type}) idles at ≈ ${money(e.monthly)}/mo: ${e.why.join('; ')}`,
          undefined, [at(e)]));
      }
    }
    if (o.max !== undefined && total > o.max) {
      violations.push(violation('idle-cost-budget', 'error',
        `The idle cost is ≈ $${total.toFixed(2)}/mo, over the budget of $${o.max.toFixed(2)}/mo. The most expensive resources are listed.`,
        'Remove or size down resources, or raise max.', resources.slice(0, 10).map(at)));
    }
    // eslint-disable-next-line no-console
    console.error(`cfn-idle-cost: idle cost ≈ ${money(total)}/mo${o.max !== undefined ? ` (budget $${o.max}/mo)` : ''}; nested stacks are not counted at synth`);
    return { success: violations.every((v) => v.severity !== 'error'), violations };
  }
}

function violation(ruleName, severity, description, fix, violatingResources) {
  return {
    ruleName, severity, description, fix,
    violatingResources: violatingResources.map((r) => ({ resourceLogicalId: r.resourceLogicalId || '', templatePath: r.templatePath, locations: r.locations || [] })),
  };
}

/** Adds IdleCostValidation to a CDK app (aws-cdk-lib with Validations, 2.2xx or later). */
class IdleCost {
  static apply(scope, options) {
    // The app's own aws-cdk-lib, not one next to this package.
    const { Validations } = require(require.resolve('aws-cdk-lib', { paths: [process.cwd(), __dirname] }));
    if (!Validations) {
      throw new Error('cfn-idle-cost: this aws-cdk-lib has no Validations; pass new IdleCostValidation() in new App({ policyValidationBeta1: [...] }) instead');
    }
    Validations.of(scope).addPlugins(new IdleCostValidation(options));
  }
}

module.exports = { IdleCost, IdleCostValidation, binaryPath };
