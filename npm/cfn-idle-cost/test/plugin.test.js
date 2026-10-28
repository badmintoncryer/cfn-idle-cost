// node --test npm/cfn-idle-cost/test (CFN_IDLE_COST_BINARY: a built cfn-idle-cost)
const test = require('node:test');
const assert = require('node:assert');
const path = require('path');
const { IdleCostValidation } = require('..');

const testdata = path.join(__dirname, '..', '..', '..', 'testdata');
const template = path.join(testdata, 'template.yaml');

test('reports the idle cost and passes under the budget', () => {
  const r = new IdleCostValidation({ max: 100000, warnAbove: 10 }).validate({ stackTemplates: [{ templatePath: template, region: 'us-east-1' }] });
  assert.equal(r.success, true);
  assert.ok(r.violations.length > 0, 'a resource over $10/mo is a warning');
  for (const v of r.violations) {
    assert.equal(v.ruleName, 'idle-cost-resource');
    assert.equal(v.severity, 'warning');
    assert.equal(v.violatingResources[0].templatePath, template);
    assert.match(v.description, /idles at ≈ \$/);
  }
});

test('fails over the budget, listing the most expensive resources', () => {
  const r = new IdleCostValidation({ max: 1 }).validate({ stackTemplates: [{ templatePath: template, region: 'us-east-1' }] });
  assert.equal(r.success, false);
  const v = r.violations.find((x) => x.ruleName === 'idle-cost-budget');
  assert.ok(v);
  assert.ok(v.violatingResources.length > 0 && v.violatingResources.length <= 10);
  assert.ok(v.violatingResources[0].resourceLogicalId);
});

test('an env-agnostic stack needs a region', () => {
  const env = { ...process.env };
  delete process.env.CDK_DEFAULT_REGION; delete process.env.AWS_REGION; delete process.env.AWS_DEFAULT_REGION;
  try {
    const r = new IdleCostValidation().validate({ templatePaths: [template] });
    assert.equal(r.success, false);
    assert.equal(r.violations[0].ruleName, 'idle-cost-no-region');
    assert.equal(new IdleCostValidation({ region: 'us-east-1' }).validate({ templatePaths: [template] }).success, true);
  } finally {
    Object.assign(process.env, env);
  }
});
