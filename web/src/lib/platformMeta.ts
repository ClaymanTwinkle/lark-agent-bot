export interface FieldDef {
  key: string;
  labelKey: string;
  required?: boolean;
  type?: 'text' | 'password' | 'number' | 'boolean' | 'select';
  placeholder?: string;
  hintKey?: string;
  group?: 'basic' | 'advanced';
  options?: string[];
  showWhen?: Record<string, string[]>;
}

export interface PlatformMeta {
  label: string;
  fields: FieldDef[];
}

// Manual-form metadata for platforms that are configured by filling in
// credentials. Feishu / Lark is onboarded via QR (see PlatformSetupQR), so
// no manual entries are registered here.
export const platformMeta: Record<string, PlatformMeta> = {};
