// Types for index.js. Structural, so that they need neither aws-cdk-lib nor
// constructs to type-check, and an IdleCostValidation fits both
// IPolicyValidationPlugin (Validations.of(app).addPlugins) and
// IPolicyValidationPluginBeta1 (new App({ policyValidationBeta1: [...] })).

export interface IdleCostOptions {
  /** Fail the synth when the total idle cost is over this many USD a month. */
  readonly max?: number;
  /** Report each resource that idles at this many USD a month or more, as a warning. */
  readonly warnAbove?: number;
  /** Region for env-agnostic stacks (default: CDK_DEFAULT_REGION, then AWS_REGION). */
  readonly region?: string;
  /** More cfn-idle-cost flags, e.g. ['--parameter', 'Env=prod']. */
  readonly args?: string[];
}

export interface IdleCostValidationContext {
  readonly templatePaths: string[];
  readonly stackTemplates?: ReadonlyArray<{ readonly templatePath: string; readonly region?: string }>;
}

export interface IdleCostViolatingResource {
  readonly resourceLogicalId: string;
  readonly templatePath: string;
  readonly locations: string[];
}

export interface IdleCostViolation {
  readonly ruleName: string;
  readonly severity?: string;
  readonly description: string;
  readonly fix?: string;
  readonly violatingResources: IdleCostViolatingResource[];
}

export interface IdleCostValidationReport {
  readonly success: boolean;
  readonly violations: IdleCostViolation[];
}

/** A CDK policy validation plugin that reports the idle cost at `cdk synth`. */
export declare class IdleCostValidation {
  readonly name: string;
  readonly options: IdleCostOptions;
  constructor(options?: IdleCostOptions);
  validate(context: IdleCostValidationContext): IdleCostValidationReport;
}

/** Adds IdleCostValidation to a CDK app (aws-cdk-lib with Validations, 2.2xx or later). */
export declare class IdleCost {
  static apply(scope: object, options?: IdleCostOptions): void;
}

/** The path of the platform binary installed via optionalDependencies. */
export declare function binaryPath(): string;
