package dispatch

import "math"

// ValidateConstraints is the constraints part of the mockup's
// errorInputs. exportCeiling is the passport ceiling for the site's
// export regime (active_consumer_export_power_cap.md); nil = regime not
// chosen yet, so no export limit can be agreed. "" = valid.
func ValidateConstraints(site Site, cfg Constraints, exportCeiling *float64) string {
	if !finite(cfg.ReservePct) || cfg.ReservePct < site.SocMinPct || cfg.ReservePct > site.SocMaxPct {
		return "Резерв SOC має бути від " + Fmt(site.SocMinPct) + "% до " + Fmt(site.SocMaxPct) + "%."
	}
	if !finite(cfg.ImportCapKw) || cfg.ImportCapKw < 0 || cfg.ImportCapKw > site.ImportKw {
		return "Ліміт імпорту має бути від 0 до " + Fmt(site.ImportKw) + " кВт за паспортом об'єкта."
	}
	if cfg.ExportCapKw != nil {
		v := *cfg.ExportCapKw
		if !finite(v) || v < 0 {
			return "Введіть невід’ємний погоджений ліміт експорту."
		}
		if exportCeiling == nil {
			return "Для об'єкта не задано режим відпуску в мережу — ліміт експорту не можна встановити."
		}
		if v > *exportCeiling {
			return "Ліміт експорту перевищує дозволені паспортом " + Fmt(*exportCeiling) + " кВт."
		}
	}
	if !cfg.BlockExport && cfg.ExportCapKw == nil && cfg.EssSale {
		return "Вкажіть ліміт експорту в обмеженнях; без нього експорт вимкнений."
	}
	return ""
}

// ValidateCommand is the command part of errorInputs: a command that
// fails here cannot go into the draft. "" = valid.
func ValidateCommand(site Site, cfg Constraints, cmd Command) string {
	switch cmd.Type {
	case CmdCover, CmdSolar, CmdTarget, CmdCap, CmdExport, CmdFixed, CmdHold, CmdAuto:
	default:
		return "Невідома команда."
	}
	if cmd.Type == CmdExport && !cfg.BlockExport && cfg.ExportCapKw == nil {
		return "Вкажіть ліміт експорту в обмеженнях; без нього експорт вимкнений."
	}
	if cmd.Type == CmdHold || cmd.Type == CmdAuto {
		return ""
	}
	v := cmd.Value
	if !finite(v) || v < 0 {
		return "Введіть невід’ємне числове значення."
	}
	if cmd.Type == CmdTarget && (v < cfg.ReservePct || v > site.SocMaxPct) {
		return "Ціль SOC має бути між резервом та " + Fmt(site.SocMaxPct) + "%."
	}
	if cmd.Type == CmdExport && cfg.ExportCapKw != nil && v > *cfg.ExportCapKw {
		return "Ціль експорту не може перевищувати ліміт PCC " + Fmt(*cfg.ExportCapKw) + " кВт."
	}
	if cmd.Type == CmdCap && v > site.ImportKw {
		return "Ціль імпорту не може перевищувати паспортні " + Fmt(site.ImportKw) + " кВт."
	}
	if cmd.Type == CmdFixed && cmd.Direction != "charge" && cmd.Direction != "discharge" {
		return "Оберіть напрямок: заряд або розряд."
	}
	charging := cmd.Type == CmdSolar || (cmd.Type == CmdFixed && cmd.Direction == "charge")
	powerMax, what := site.DischargeKw, "розряду"
	if charging {
		powerMax, what = site.ChargeKw, "заряду"
	}
	if (cmd.Type == CmdCover || cmd.Type == CmdSolar || cmd.Type == CmdFixed) && v > powerMax {
		return "Паспортна межа " + what + " — " + Fmt(powerMax) + " кВт."
	}
	return ""
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
