// Options for <SearchSelect>. Values are strings ("" = nothing chosen).
export interface SelectOption {
  value: string
  label: string
  sub?: string // secondary text (e.g. an employee code), also searched
  disabled?: boolean
}

/** Options from a label map ({ ACTIVE: 'Active' } or { ACTIVE: { label: 'Active' } }), optionally only `keys`, in that order. */
export function optionsFrom(map: Record<string, string | { label: string }>, keys?: readonly string[]): SelectOption[] {
  return (keys ?? Object.keys(map)).map(value => {
    const v = map[value]
    return { value, label: typeof v === 'string' ? v : (v?.label ?? value) }
  })
}
