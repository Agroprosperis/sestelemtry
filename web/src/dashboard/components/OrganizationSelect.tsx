import { formatOrganizationLabel } from '../config'

type Props = {
  value: string
  options: string[]
  onChange: (next: string) => void
}

export function OrganizationSelect({ value, options, onChange }: Props) {
  return (
    <label className="org-select" htmlFor="org-select">
      <span>Організація</span>
      <select id="org-select" value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map((orgID) => (
          <option key={orgID} value={orgID}>
            {formatOrganizationLabel(orgID)}
          </option>
        ))}
      </select>
    </label>
  )
}
