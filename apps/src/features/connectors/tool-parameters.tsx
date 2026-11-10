import { Input, InputNumber, Select } from 'antd';

type ParameterType = 'string' | 'number' | 'integer' | 'boolean';
export interface ToolParameter {
  name: string;
  label: string;
  type: ParameterType;
  required: boolean;
  description?: string;
  options?: unknown[];
  schema: Record<string, unknown>;
}

function record(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

export function toolParameters(schema: Record<string, unknown>): ToolParameter[] | null {
  if (
    schema.type !== 'object' ||
    ['$ref', 'allOf', 'anyOf', 'oneOf', 'if', 'dependentRequired', 'dependentSchemas'].some(
      (key) => key in schema,
    ) ||
    !record(schema.properties ?? {})
  )
    return null;
  const properties = (schema.properties ?? {}) as Record<string, unknown>;
  if (
    (Array.isArray(schema.required) &&
      schema.required.some(
        (name) => typeof name !== 'string' || !Object.hasOwn(properties, name),
      )) ||
    (Object.keys(properties).length === 0 && schema.additionalProperties !== false)
  )
    return null;
  const parameters: ToolParameter[] = [];
  for (const [name, field] of Object.entries(properties)) {
    if (
      !record(field) ||
      !['string', 'number', 'integer', 'boolean'].includes(String(field.type)) ||
      ['$ref', 'allOf', 'anyOf', 'oneOf', 'const'].some((key) => key in field) ||
      (field.enum !== undefined &&
        (!Array.isArray(field.enum) ||
          field.enum.some((v) => !['string', 'number', 'boolean'].includes(typeof v))))
    )
      return null;
    parameters.push({
      name,
      label: typeof field.title === 'string' ? field.title : name,
      type: field.type as ParameterType,
      required: Array.isArray(schema.required) && schema.required.includes(name),
      description: typeof field.description === 'string' ? field.description : undefined,
      options: field.enum as unknown[] | undefined,
      schema: field,
    });
  }
  return parameters;
}

export function parameterErrors(parameters: ToolParameter[], values: Record<string, unknown>) {
  return Object.fromEntries(
    parameters.flatMap((field) => {
      const value = values[field.name];
      if (value === undefined) return field.required ? [[field.name, `请填写 ${field.label}`]] : [];
      if (
        (field.type === 'integer' ? !Number.isInteger(value) : typeof value !== field.type) ||
        (field.options && !field.options.includes(value))
      )
        return [[field.name, `${field.label} 的类型或取值不符合要求`]];
      return [];
    }),
  );
}

export function ToolParameterInput({
  field,
  id,
  value,
  onChange,
}: {
  field: ToolParameter;
  id: string;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const options = field.options ?? (field.type === 'boolean' ? [true, false] : undefined);
  if (options)
    return (
      <Select
        id={id}
        virtual={false}
        allowClear={!field.required}
        placeholder={`请选择 ${field.label}`}
        value={value === undefined ? undefined : JSON.stringify(value)}
        options={options.map((v) => ({ label: String(v), value: JSON.stringify(v) }))}
        onChange={(v) => onChange(v === undefined ? undefined : JSON.parse(v))}
      />
    );
  if (field.type === 'number' || field.type === 'integer')
    return (
      <InputNumber
        id={id}
        className="connector-parameter-number"
        value={typeof value === 'number' ? value : null}
        precision={field.type === 'integer' ? 0 : undefined}
        min={typeof field.schema.minimum === 'number' ? field.schema.minimum : undefined}
        max={typeof field.schema.maximum === 'number' ? field.schema.maximum : undefined}
        placeholder={`请输入 ${field.label}`}
        onChange={(v) => onChange(v ?? undefined)}
      />
    );
  return (
    <Input.TextArea
      id={id}
      autoSize={{ minRows: 3, maxRows: 8 }}
      placeholder={`请输入 ${field.label}`}
      value={typeof value === 'string' ? value : ''}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}
