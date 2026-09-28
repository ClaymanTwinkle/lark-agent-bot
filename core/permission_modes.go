package core

import "log/slog"

func (e *Engine) permissionModeText(mode PermissionModeInfo) (string, string) {
	name, desc := mode.Name, mode.Desc
	if e.i18n.IsZhLike() {
		name, desc = mode.NameZh, mode.DescZh
	}
	if mode.NameKey != "" {
		name = e.i18n.T(mode.NameKey)
	}
	if mode.DescKey != "" {
		desc = e.i18n.T(mode.DescKey)
	}
	return name, desc
}

func (e *Engine) modeValidationMessage(switcher ModeSwitcher, mode string) string {
	if validator, ok := switcher.(ModeValidator); ok {
		if err := validator.ValidateMode(mode); err != nil {
			slog.Warn("permission mode rejected", "error", err)
			return e.i18n.Tf(MsgModeInvalid, mode) + "\n\n" + e.modeUsageText(switcher.PermissionModes())
		}
	}
	return ""
}
