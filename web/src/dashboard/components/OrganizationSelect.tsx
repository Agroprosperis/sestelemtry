type Props = {
  value: string
  options: string[]
  onChange: (next: string) => void
}

export function OrganizationSelect({ value, options, onChange }: Props) {
  return (
    <label className="org-select" htmlFor="org-select">
      <span>Organization</span>
      <select id="org-select" value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map((orgID) => (
          <option key={orgID} value={orgID}>
            {orgID}
          </option>
        ))}
      </select>
    </label>
  )
}
