package core

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The members and permissions page of /config edits admin_from, the chat
// platform's allow_from and disabled_commands. Changes refuse anything that
// would lock people out; see changeMemberList.

func isConfigAccessAction(name string) bool {
	return slices.Contains(configAccessActions, strings.ToLower(name))
}

// memberListError is a refused admin_from / allow_from change, shown to the
// user as key formatted with id.
type memberListError struct {
	key MsgKey
	id  string
}

func (err *memberListError) Error() string { return string(err.key) + ": " + err.id }

var errConfigAccessUsage = errors.New("config access usage")

// splitMemberList splits a comma-separated user list, dropping blanks.
func splitMemberList(list string) []string {
	var members []string
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			members = append(members, id)
		}
	}
	return members
}

// memberListUnrestricted reports whether list names no one in particular:
// "*", or "" which for allow_from means everyone and for admin_from nobody.
func memberListUnrestricted(list string) bool {
	list = strings.TrimSpace(list)
	return list == "" || list == "*"
}

func indexFold(list []string, id string) int {
	return slices.IndexFunc(list, func(s string) bool { return strings.EqualFold(s, id) })
}

// changeMemberList adds ids to or removes them from a comma-separated user
// list such as admin_from or allow_from. IDs compare case-insensitively, like
// AllowList. It refuses changes that could lock people out: editing an
// unrestricted list, removing self, and removing the last entry (lastKey says
// what an empty list would mean).
func changeMemberList(list string, add bool, ids []string, self string, lastKey MsgKey) (string, error) {
	if memberListUnrestricted(list) {
		shown := strings.TrimSpace(list)
		if shown == "" {
			shown = `""`
		}
		return "", &memberListError{key: MsgConfigListUnrestricted, id: shown}
	}
	members := splitMemberList(list)
	for _, id := range ids {
		if id == "*" || strings.ContainsAny(id, ", \t") {
			return "", &memberListError{key: MsgConfigInvalidUserID, id: id}
		}
	}
	if add {
		for _, id := range ids {
			if indexFold(members, id) < 0 {
				members = append(members, id)
			}
		}
		return strings.Join(members, ","), nil
	}
	for _, id := range ids {
		if strings.EqualFold(id, self) {
			return "", &memberListError{key: MsgConfigCannotRemoveSelf, id: id}
		}
		i := indexFold(members, id)
		if i < 0 {
			return "", &memberListError{key: MsgConfigNotListed, id: id}
		}
		members = slices.Delete(members, i, i+1)
	}
	if len(members) == 0 {
		return "", &memberListError{key: lastKey, id: ids[len(ids)-1]}
	}
	return strings.Join(members, ","), nil
}

// configMemberTargets resolves the users named in "/config admin|allow
// add|remove ...": "@Name" through the message's mentions, anything else as a
// user ID.
func configMemberTargets(actor *Message, args []string) ([]string, error) {
	rest := " " + strings.Join(args, " ") + " "
	mentions := slices.Clone(actor.Mentions)
	// Replace longer names first so "@Ann Lee" is not cut by "@Ann".
	sort.SliceStable(mentions, func(i, j int) bool { return len(mentions[i].Name) > len(mentions[j].Name) })
	var ids []string
	for _, m := range mentions {
		token := "@" + m.Name
		if m.Name == "" || m.ID == "" || !strings.Contains(rest, token) {
			continue
		}
		rest = strings.ReplaceAll(rest, token, " ")
		ids = append(ids, m.ID)
	}
	for _, f := range strings.Fields(rest) {
		if strings.HasPrefix(f, "@") {
			return nil, &memberListError{key: MsgConfigUnknownMention, id: f}
		}
		ids = append(ids, f)
	}
	if len(ids) == 0 {
		return nil, errConfigAccessUsage
	}
	return ids, nil
}

// configAccessChange applies "admin|allow add|remove <users>" or
// "disable|enable <command>" for actor, an admin, and returns the outcome.
func (e *Engine) configAccessChange(actor *Message, args []string) string {
	e.configAccessMu.Lock()
	defer e.configAccessMu.Unlock()

	if len(args) < 2 {
		return e.i18n.T(MsgConfigAccessUsage)
	}
	switch strings.ToLower(args[0]) {
	case "admin", "allow":
		op := strings.ToLower(args[1])
		if op != "add" && op != "remove" {
			return e.i18n.T(MsgConfigAccessUsage)
		}
		ids, err := configMemberTargets(actor, args[2:])
		if err != nil {
			return e.configAccessError(err)
		}
		if strings.EqualFold(args[0], "admin") {
			return e.changeAdminFrom(actor, op == "add", ids)
		}
		return e.changeAllowFrom(actor, op == "add", ids)
	case "disable":
		return e.changeDisabledCommand(args[1], true)
	case "enable":
		return e.changeDisabledCommand(args[1], false)
	}
	return e.i18n.T(MsgConfigAccessUsage)
}

func (e *Engine) configAccessError(err error) string {
	var listErr *memberListError
	if errors.As(err, &listErr) {
		return e.i18n.Tf(listErr.key, listErr.id)
	}
	if errors.Is(err, errConfigAccessUsage) {
		return e.i18n.T(MsgConfigAccessUsage)
	}
	return e.i18n.Tf(MsgError, err)
}

func (e *Engine) changeAdminFrom(actor *Message, add bool, ids []string) string {
	e.userRolesMu.RLock()
	current := e.adminFrom
	e.userRolesMu.RUnlock()
	next, err := changeMemberList(current, add, ids, actor.UserID, MsgConfigCannotRemoveLastAdmin)
	if err != nil {
		return e.configAccessError(err)
	}
	if err := e.saveProjectSettings(ProjectSettingsUpdate{AdminFrom: &next}); err != nil {
		return e.i18n.Tf(MsgError, err)
	}
	e.SetAdminFrom(next)
	return e.i18n.Tf(MsgConfigUpdated, "admin_from", next)
}

// allowFromUpdater returns this project's platform named platformName when
// it can change allow_from at runtime.
func (e *Engine) allowFromUpdater(platformName string) AllowFromUpdater {
	for _, p := range e.platforms {
		if strings.EqualFold(p.Name(), platformName) {
			if u, ok := p.(AllowFromUpdater); ok {
				return u
			}
		}
	}
	return nil
}

// changeAllowFrom edits allow_from of the platform the admin is using.
func (e *Engine) changeAllowFrom(actor *Message, add bool, ids []string) string {
	u := e.allowFromUpdater(actor.Platform)
	if u == nil {
		return e.i18n.T(MsgConfigAllowUnsupported)
	}
	next, err := changeMemberList(u.AllowFrom(), add, ids, actor.UserID, MsgConfigCannotRemoveLastAllowed)
	if err != nil {
		return e.configAccessError(err)
	}
	update := ProjectSettingsUpdate{PlatformAllowFrom: map[string]string{actor.Platform: next}}
	if err := e.saveProjectSettings(update); err != nil {
		return e.i18n.Tf(MsgError, err)
	}
	e.SetPlatformAllowFrom(actor.Platform, next)
	return e.i18n.Tf(MsgConfigUpdated, "allow_from", next)
}

// changeDisabledCommand disables or re-enables one command. /config itself
// cannot be disabled here, or the card could no longer be opened.
func (e *Engine) changeDisabledCommand(name string, disable bool) string {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if name == "" {
		return e.i18n.T(MsgConfigAccessUsage)
	}
	resolved := resolveDisabledCmds([]string{name})
	current := e.GetDisabledCommands()
	if disable {
		if resolved["config"] {
			return e.i18n.T(MsgConfigCannotDisableConfig)
		}
		for id := range resolved {
			if !slices.Contains(current, id) {
				current = append(current, id)
			}
		}
	} else {
		n := len(current)
		current = slices.DeleteFunc(current, func(id string) bool { return resolved[id] })
		if len(current) == n {
			return e.i18n.Tf(MsgConfigNotDisabled, name)
		}
	}
	sort.Strings(current)
	if err := e.saveProjectSettings(ProjectSettingsUpdate{DisabledCommands: current}); err != nil {
		return e.i18n.Tf(MsgError, err)
	}
	e.SetDisabledCommands(current)
	return e.i18n.Tf(MsgConfigUpdated, "disabled_commands", strings.Join(current, ", "))
}

// renderConfigAccess draws the members and permissions page for viewer.
func (e *Engine) renderConfigAccess(cb *CardBuilder, viewer *Message) {
	if viewer == nil {
		return // admin pages are refused before rendering without a viewer
	}
	e.userRolesMu.RLock()
	admins := e.adminFrom
	e.userRolesMu.RUnlock()
	names := e.userNameResolver(viewer.Platform)

	cb.Markdown(e.i18n.T(MsgConfigAdminsTitle))
	e.renderMemberList(cb, admins, "admin", viewer.UserID, names, e.i18n.T(MsgConfigAdminsEveryone))
	cb.Note(e.i18n.T(MsgConfigAdminAddHint))

	cb.Markdown(e.i18n.Tf(MsgConfigAllowTitle, viewer.Platform))
	if u := e.allowFromUpdater(viewer.Platform); u != nil {
		e.renderMemberList(cb, u.AllowFrom(), "allow", viewer.UserID, names, e.i18n.T(MsgConfigAllowEveryone))
		cb.Note(e.i18n.T(MsgConfigAllowAddHint))
	} else {
		cb.Markdown(e.i18n.T(MsgConfigAllowUnsupported))
	}

	cb.Markdown(e.i18n.T(MsgConfigDisabledTitle))
	disabled := e.GetDisabledCommands()
	if len(disabled) == 0 {
		cb.Markdown(e.i18n.T(MsgConfigNoDisabledCommands))
	}
	for _, id := range disabled {
		cb.ListItemBtn("`/"+id+"`", e.i18n.T(MsgConfigEnable), "default", "act:/config enable "+id)
	}
	var options []CardSelectOption
	for _, c := range builtinCommands {
		if c.id != "config" && !slices.Contains(disabled, c.id) {
			options = append(options, CardSelectOption{Text: "/" + c.id, Value: "act:/config disable " + c.id})
		}
	}
	cb.Select(e.i18n.T(MsgConfigDisablePlaceholder), options, "")
}

// renderMemberList lists the users in list with a remove button for each one
// that may be removed: not the viewer, and not the last one.
func (e *Engine) renderMemberList(cb *CardBuilder, list, action, self string, names UserNameResolver, everyone string) {
	if memberListUnrestricted(list) {
		cb.Markdown(everyone)
		return
	}
	members := splitMemberList(list)
	for _, id := range members {
		label := "`" + id + "`"
		if names != nil {
			if name := names.ResolveUserName(id); name != "" && name != id {
				label = name + " " + label
			}
		}
		if strings.EqualFold(id, self) {
			cb.Markdown(label + " " + e.i18n.T(MsgConfigYou))
			continue
		}
		if len(members) == 1 {
			cb.Markdown(label)
			continue
		}
		cb.ListItemBtn(label, e.i18n.T(MsgConfigRemove), "danger", fmt.Sprintf("act:/config %s remove %s", action, id))
	}
}

func (e *Engine) userNameResolver(platformName string) UserNameResolver {
	for _, p := range e.platforms {
		if strings.EqualFold(p.Name(), platformName) {
			if r, ok := p.(UserNameResolver); ok {
				return r
			}
		}
	}
	return nil
}

// configAccessText is the members and permissions page for platforms
// without cards.
func (e *Engine) configAccessText(viewer *Message) string {
	e.userRolesMu.RLock()
	admins := e.adminFrom
	e.userRolesMu.RUnlock()
	var sb strings.Builder
	sb.WriteString(e.i18n.T(MsgConfigTitle))
	fmt.Fprintf(&sb, "`admin_from` = `%s`\n", admins)
	if u := e.allowFromUpdater(viewer.Platform); u != nil {
		fmt.Fprintf(&sb, "`allow_from` (%s) = `%s`\n", viewer.Platform, u.AllowFrom())
	}
	fmt.Fprintf(&sb, "`disabled_commands` = `%s`\n\n", strings.Join(e.GetDisabledCommands(), ", "))
	sb.WriteString(e.i18n.T(MsgConfigAccessUsage))
	return sb.String()
}
