import { Desktop, Moon, Sun } from '@phosphor-icons/react'
import { useTheme, type ThemePreference } from './theme'
import './theme-toggle.css'

const OPTIONS: { id: ThemePreference; label: string; Icon: typeof Sun }[] = [
  { id: 'light', label: 'Світла', Icon: Sun },
  { id: 'dark', label: 'Темна', Icon: Moon },
  { id: 'system', label: 'Системна', Icon: Desktop },
]

export function ThemeToggle() {
  const { preference, setPreference } = useTheme()

  return (
    <div className="theme-toggle" role="group" aria-label="Тема оформлення">
      {OPTIONS.map(({ id, label, Icon }) => (
        <button
          key={id}
          type="button"
          className={preference === id ? 'active' : ''}
          aria-pressed={preference === id}
          title={label}
          aria-label={label}
          onClick={() => setPreference(id)}
        >
          <Icon size={15} weight={preference === id ? 'fill' : 'regular'} />
        </button>
      ))}
    </div>
  )
}
